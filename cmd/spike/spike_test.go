//go:build unix

package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// TestEmulatorAnswersQueries proves the emulator replies to terminal queries
// via its Read side. This is the mechanism whose absence made vim/claude hang
// with a blank screen: a DSR cursor-position request (ESC [ 6 n) must produce a
// cursor-position report (ESC [ row ; col R) that we feed back to the PTY.
func TestEmulatorAnswersQueries(t *testing.T) {
	em := vt.NewSafeEmulator(80, 24)

	// The reader MUST run concurrently with Write: Emulator.Write blocks while
	// pushing a query reply into its pipe until something drains it. (This is
	// why the real code runs the emulator->PTY pump as its own goroutine.)
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := em.Read(buf)
		got <- string(buf[:n])
	}()

	if _, err := em.Write([]byte("\x1b[6n")); err != nil {
		t.Fatalf("write query: %v", err)
	}

	select {
	case reply := <-got:
		if !strings.HasPrefix(reply, "\x1b[") || !strings.HasSuffix(reply, "R") {
			t.Fatalf("expected cursor-position report ESC[...R, got %q", reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("emulator did not answer DSR query (would hang real apps)")
	}
}

// TestPTYToEmulator is a headless sanity check for the core plumbing: launch a
// command in a PTY, pump its output into a vt emulator, and confirm the
// rendered screen contains the expected text (with color). This does not need
// a real TTY, so it runs in CI; the full interactive experience (claude/codex)
// must still be eyeballed via `go run ./cmd/spike claude`.
func TestPTYToEmulator(t *testing.T) {
	pty, err := xpty.NewPty(40, 10)
	if err != nil {
		t.Fatalf("new pty: %v", err)
	}
	defer pty.Close()

	cmd := exec.Command("/bin/sh", "-c", `printf '\033[31mHELLO-VT\033[0m'`)
	setupPTYCommand(cmd)
	if err := pty.Start(cmd); err != nil {
		t.Fatalf("start: %v", err)
	}

	em := vt.NewSafeEmulator(40, 10)
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := pty.Read(buf)
			if n > 0 {
				_, _ = em.Write(buf[:n])
			}
			if rerr != nil {
				close(done)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for PTY output")
	}
	// Give the emulator a beat to apply the final writes.
	time.Sleep(50 * time.Millisecond)

	rendered := em.Render()
	if !strings.Contains(rendered, "HELLO-VT") {
		t.Fatalf("rendered screen missing expected text; got:\n%q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Errorf("rendered screen missing ANSI styling; got:\n%q", rendered)
	}
}
