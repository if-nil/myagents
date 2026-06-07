// Command spike validates the core technical stack before any real UI is
// built: xpty (cross-platform PTY) + x/vt (terminal emulator) + raw stdin
// passthrough. It launches a child program in a PTY, feeds the PTY output into
// a vt emulator, renders the emulator's screen to the real terminal at a capped
// frame rate, and forwards keystrokes back to the PTY.
//
// Usage:
//
//	go run ./cmd/spike            # launches $SHELL
//	go run ./cmd/spike claude     # launches claude
//	go run ./cmd/spike vim x.txt  # launches vim
//
// Acceptance: the child's full-screen interactive UI (colors, borders, cursor,
// input box, streaming output) is legible and usable, and resizing the window
// does not corrupt the display. The child exiting ends the spike.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
	"golang.org/x/term"
)

// ctrlQ is the force-quit byte (Ctrl-Q). It is intercepted before stdin is
// forwarded to the PTY so the user can always escape the spike.
const ctrlQ = 0x11

// dbg writes diagnostics to /tmp/myagents-spike.log so we can debug a blank or
// stuck screen without a TTY. It is best-effort and never fails the run.
var dbg = func() *log.Logger {
	f, err := os.Create("/tmp/myagents-spike.log")
	if err != nil {
		return log.New(io.Discard, "", 0)
	}
	return log.New(f, "", log.Ltime|log.Lmicroseconds)
}()

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "spike:", err)
		os.Exit(1)
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// snippet returns a short, printable prefix of b for logging.
func snippet(b []byte) string {
	const max = 60
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}

func run() error {
	// Pick the command to launch.
	args := os.Args[1:]
	if len(args) == 0 {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		args = []string{sh}
	}

	// Put the real terminal into raw mode so every keystroke is forwarded
	// verbatim to the child PTY.
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("make raw: %w", err)
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	w, h, err := term.GetSize(fd)
	if err != nil || w <= 0 || h <= 0 {
		w, h = 80, 24
	}

	// Create the PTY and emulator at the current size.
	pty, err := xpty.NewPty(w, h)
	if err != nil {
		return fmt.Errorf("new pty: %w", err)
	}
	defer pty.Close() //nolint:errcheck

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	setupPTYCommand(cmd) // platform-specific controlling-tty setup

	if err := pty.Start(cmd); err != nil {
		return fmt.Errorf("start %q: %w", args[0], err)
	}
	_ = pty.Resize(w, h)
	dbg.Printf("started %v size=%dx%d pid=%v", args, w, h, cmd.Process.Pid)

	em := vt.NewSafeEmulator(w, h)

	quit := make(chan struct{})

	// Coalesced redraw signal: rapid PTY writes collapse into a single pending
	// redraw, drained by the frame ticker. This mirrors tuios's PTYDataChan.
	redraw := make(chan struct{}, 1)
	signalRedraw := func() {
		select {
		case redraw <- struct{}{}:
		default:
		}
	}

	// PTY -> emulator.
	go func() {
		buf := make([]byte, 32*1024)
		var total int
		for {
			n, rerr := pty.Read(buf)
			if n > 0 {
				_, _ = em.Write(buf[:n])
				total += n
				dbg.Printf("pty read n=%d total=%d first=%q", n, total, snippet(buf[:n]))
				signalRedraw()
			}
			if rerr != nil {
				dbg.Printf("pty read error after total=%d: %v", total, rerr)
				signalRedraw()
				return
			}
		}
	}()

	// Emulator -> PTY: the emulator answers terminal queries (DA, DSR cursor
	// position, OSC color, in-band resize, ...) via its Read side. Full-screen
	// apps like vim/claude BLOCK on these replies before drawing, so we must
	// pump them back into the PTY. Without this the child hangs with a blank
	// screen. (This is the subtlety to carry into M1.)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := em.Read(buf)
			if n > 0 {
				_, _ = pty.Write(buf[:n])
				dbg.Printf("emu reply n=%d %q", n, snippet(buf[:n]))
			}
			if rerr != nil {
				dbg.Printf("emu read error: %v", rerr)
				return
			}
		}
	}()

	// Real stdin -> PTY (key passthrough). Ctrl-Q force-quits the spike.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if i := indexByte(buf[:n], ctrlQ); i >= 0 {
					dbg.Printf("Ctrl-Q force quit")
					close(quit)
					return
				}
				dbg.Printf("stdin n=%d %q", n, snippet(buf[:n]))
				_, _ = pty.Write(buf[:n])
			}
			if rerr != nil {
				dbg.Printf("stdin read error: %v", rerr)
				return
			}
		}
	}()

	// Watch for child exit.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	// Platform-specific terminal resize notifications.
	resize := make(chan struct{}, 1)
	stopResize := watchResize(resize)
	defer stopResize()

	// Use the alternate screen so we don't clobber the user's scrollback.
	out := os.Stdout
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l")        // alt screen, hide our own cursor
	defer fmt.Fprint(out, "\x1b[?25h\x1b[?1049l") //nolint:errcheck

	frame := 0
	render := func() {
		content := strings.ReplaceAll(em.Render(), "\n", "\r\n")
		pos := em.CursorPosition()
		frame++
		if frame <= 5 || frame%60 == 0 {
			dbg.Printf("frame=%d render_len=%d cursor=%d,%d", frame, len(content), pos.X, pos.Y)
		}
		var b strings.Builder
		b.WriteString("\x1b[?2026h") // begin synchronized update (atomic frame, no flicker)
		b.WriteString("\x1b[H\x1b[2J")
		b.WriteString(content)
		fmt.Fprintf(&b, "\x1b[%d;%dH", pos.Y+1, pos.X+1)
		b.WriteString("\x1b[?2026l") // end synchronized update
		_, _ = out.WriteString(b.String())
	}

	ticker := time.NewTicker(time.Second / 60)
	defer ticker.Stop()
	dirty := true

	for {
		select {
		case <-quit:
			_ = cmd.Process.Kill()
			return nil
		case <-done:
			return nil
		case <-redraw:
			dirty = true
		case <-resize:
			if nw, nh, e := term.GetSize(fd); e == nil && nw > 0 && nh > 0 {
				w, h = nw, nh
				_ = pty.Resize(w, h)
				em.Resize(w, h)
				dirty = true
			}
		case <-ticker.C:
			if dirty {
				render()
				dirty = false
			}
		}
	}
}
