// Package agent hosts AI CLI processes (Agents). Each Agent runs in its own
// PTY, with a vt emulator maintaining its screen. The package is intentionally
// decoupled from any UI: it exposes a coalesced change signal and read-only
// snapshots so a Bubble Tea program (or, later, a daemon-backed implementation)
// can drive it. See docs/adr/0001.
package agent

import (
	"os/exec"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// workingWindow is how long after the last PTY output an Agent is still
// considered "working" rather than "idle". This is the coarse, CLI-agnostic
// busy/idle heuristic; precise "waiting for input/approval" is left to future
// per-tool hooks (see CONTEXT.md "状态").
const workingWindow = 600 * time.Millisecond

// Status is an Agent's lifecycle state.
type Status string

const (
	StatusStarting Status = "starting" // spawned, not yet confirmed running
	StatusRunning  Status = "running"  // process alive
	StatusExited   Status = "exited"   // process exited (see ExitCode)
	StatusFailed   Status = "failed"   // failed to start or died abnormally
)

// Agent is a hosted AI CLI process instance: a Tool running in a working
// directory inside a PTY. Its zero value is not usable; create via the manager.
//
// Concurrency: the emulator is a plain (non-thread-safe) vt.Emulator guarded by
// emMu. Every stateful emulator call (Write, Render, SendKey, scrollback reads)
// holds emMu, EXCEPT the emulator->PTY reply pump's Read, which operates on the
// emulator's independently-synchronized pipe and must stay lock-free to avoid
// deadlocking a Write that is blocking to emit a query reply.
type Agent struct {
	ID   string
	Name string
	Tool string
	Cwd  string

	emMu sync.Mutex
	em   *vt.Emulator
	pty  xpty.Pty
	cmd  *exec.Cmd

	startedAt time.Time

	mu           sync.RWMutex
	status       Status
	exitCode     int
	exitErr      error
	lastOutput   time.Time
	unread       bool
	cols, rows   int
	hookActivity HookActivity // latest activity reported by the tool's hooks
	lastEvent    string       // name of the most recent hook event
	lastEventAt  time.Time
	mouseOn      bool   // child has enabled mouse reporting
	resumeID     string // id for precise resume; tracks the child's current session

	// notify is called (coalesced by the manager) whenever this Agent's screen
	// or status changes, so the UI knows to redraw.
	notify func()
	once   sync.Once
}

// Snapshot is an immutable view of an Agent for rendering. It never exposes the
// live PTY/emulator, so the UI cannot accidentally mutate Agent state.
type Snapshot struct {
	ID       string
	Name     string
	Tool     string
	Cwd      string
	Status   Status
	ExitCode int  // meaningful when Status == StatusExited
	Working  bool // busy; from hooks if available, else the output heuristic
	Waiting  bool // needs the user (from hooks: permission/notification)
	Unread   bool // has new output since last MarkRead
	Cols     int
	Rows     int

	Uptime    time.Duration // since the process started
	LastEvent string        // most recent hook event name (empty if none)
	ResumeID  string        // opaque id for precise resume (may be empty)
}

// start launches the process and wires up the I/O goroutines. The three
// goroutines mirror the validated spike:
//   - PTY -> emulator (child output)
//   - emulator -> PTY (query replies; MUST run concurrently or the child hangs)
//   - process exit watcher
func (a *Agent) start() error {
	a.startedAt = time.Now()
	if err := a.pty.Start(a.cmd); err != nil {
		a.setFailed(err)
		return err
	}
	_ = a.pty.Resize(a.cols, a.rows)

	a.mu.Lock()
	a.status = StatusRunning
	a.mu.Unlock()

	// PTY -> emulator.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := a.pty.Read(buf)
			if n > 0 {
				a.emMu.Lock()
				_, _ = a.em.Write(buf[:n])
				a.emMu.Unlock()
				a.markOutput()
			}
			if err != nil {
				return
			}
		}
	}()

	// emulator -> PTY: feed query replies (DA/DSR/OSC/in-band resize) back to
	// the child. Without this, full-screen apps block before drawing. The
	// emulator's Read blocks until a reply is pending and is lock-free by
	// design (see the type comment).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := a.em.Read(buf)
			if n > 0 {
				_, _ = a.pty.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// Process exit watcher.
	go func() {
		err := a.cmd.Wait()
		a.setExited(err)
		// Closing the PTY unblocks the PTY->emulator goroutine. We deliberately
		// do NOT call a.em.Close(): x/vt's Emulator.Read checks an unguarded
		// `closed` bool, so closing it while the reply pump is parked in Read is
		// a data race that cannot be fixed without locking the blocking Read
		// (which would deadlock) or patching upstream. The pump goroutine stays
		// parked on em.Read and is reaped when the process exits. The per-agent
		// goroutine leak is bounded and acceptable for the in-process MVP.
		_ = a.pty.Close()
		if a.notify != nil {
			a.notify()
		}
	}()

	return nil
}

func (a *Agent) markOutput() {
	a.mu.Lock()
	a.lastOutput = time.Now()
	a.unread = true
	a.mu.Unlock()
	if a.notify != nil {
		a.notify()
	}
}

func (a *Agent) setExited(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.exitErr = err
	if exitErr, ok := err.(*exec.ExitError); ok {
		a.exitCode = exitErr.ExitCode()
		a.status = StatusExited
	} else if err != nil {
		a.exitCode = -1
		a.status = StatusFailed
	} else {
		a.exitCode = 0
		a.status = StatusExited
	}
}

func (a *Agent) setFailed(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = StatusFailed
	a.exitErr = err
	a.exitCode = -1
}

// Write forwards raw input bytes to the Agent's PTY. It is a no-op once the
// process has exited. Most input should go through SendKey/SendText instead, so
// the emulator can encode it according to the child's active modes.
func (a *Agent) Write(p []byte) (int, error) {
	if !a.alive() {
		return len(p), nil
	}
	return a.pty.Write(p)
}

// SendKey encodes a key press according to the emulator's current modes
// (application cursor keys, keypad, ...) and delivers it to the child via the
// emulator->PTY pump. Use this for special keys and Ctrl/Alt combinations.
func (a *Agent) SendKey(k uv.KeyEvent) {
	if !a.alive() {
		return
	}
	a.emMu.Lock()
	a.em.SendKey(k)
	a.emMu.Unlock()
}

// SendText delivers printable text to the child as-is. Use this for ordinary
// typed characters, where SendKey's Code-only encoding would drop shifted or
// uppercase input.
func (a *Agent) SendText(s string) {
	if !a.alive() {
		return
	}
	a.emMu.Lock()
	a.em.SendText(s)
	a.emMu.Unlock()
}

// SetMouseEnabled records whether the child has enabled mouse reporting (driven
// by the emulator's mode callbacks).
func (a *Agent) SetMouseEnabled(on bool) {
	a.mu.Lock()
	a.mouseOn = on
	a.mu.Unlock()
}

// MouseEnabled reports whether the child currently wants mouse events. When
// false, callers should fall back to alternate-scroll (wheel → arrow keys).
func (a *Agent) MouseEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mouseOn
}

// SendMouse forwards a mouse event to the child. The emulator encodes it per
// the child's active mouse mode and no-ops if the child has not enabled mouse
// reporting, so it is always safe to call.
func (a *Agent) SendMouse(ev uv.MouseEvent) {
	if !a.alive() {
		return
	}
	a.emMu.Lock()
	a.em.SendMouse(ev)
	a.emMu.Unlock()
}

// Paste delivers pasted text, wrapped in bracketed-paste markers if the child
// enabled that mode.
func (a *Agent) Paste(s string) {
	if !a.alive() {
		return
	}
	a.emMu.Lock()
	a.em.Paste(s)
	a.emMu.Unlock()
}

func (a *Agent) alive() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status == StatusRunning || a.status == StatusStarting
}

// Resize resizes both the PTY and the emulator to w columns by h rows.
func (a *Agent) Resize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	a.mu.Lock()
	a.cols, a.rows = w, h
	a.mu.Unlock()
	a.emMu.Lock()
	a.em.Resize(w, h)
	a.emMu.Unlock()
	_ = a.pty.Resize(w, h)
}

// ForceRepaint nudges the child to redraw its whole screen by briefly shrinking
// the PTY by one row and restoring it. ConPTY only re-emits the full screen on
// an actual size change (a same-size resize is a no-op), so jiggling the row
// count makes it replay its authoritative buffer through the normal
// PTY->emulator pump — flushing ghost cells that frozen Windows 10 ConPTY
// wide-char bugs leave in our emulator. On Unix it merely triggers a couple of
// harmless SIGWINCH redraws. The emulator is deliberately not resized here: the
// replayed output flows through Write like any other child output.
func (a *Agent) ForceRepaint() {
	if !a.alive() {
		return
	}
	a.mu.RLock()
	w, h := a.cols, a.rows
	a.mu.RUnlock()
	if w < 1 || h < 2 {
		return // need at least one row to give back after shrinking
	}
	_ = a.pty.Resize(w, h-1)
	_ = a.pty.Resize(w, h)
}

// Render returns the Agent's current live screen as an ANSI string.
func (a *Agent) Render() string {
	a.emMu.Lock()
	defer a.emMu.Unlock()
	return a.em.Render()
}

// ScrollbackLen returns the number of lines currently in the scrollback buffer.
func (a *Agent) ScrollbackLen() int {
	a.emMu.Lock()
	defer a.emMu.Unlock()
	return a.em.ScrollbackLen()
}

// RenderView returns height lines of the Agent's screen scrolled up by offset
// lines into the scrollback (offset 0 = the live screen). It composes
// scrollback history above the live screen and snapshots both atomically under
// emMu, so it is race-free against concurrent PTY output.
func (a *Agent) RenderView(offset, height int) string {
	a.emMu.Lock()
	defer a.emMu.Unlock()

	screen := strings.Split(a.em.Render(), "\n")
	sb := a.em.Scrollback()
	sbLen := sb.Len()
	if offset <= 0 || sbLen == 0 || height <= 0 {
		return strings.Join(screen, "\n")
	}
	if offset > sbLen {
		offset = sbLen
	}

	// Combined coordinate space: [0..sbLen) scrollback, then the live screen.
	start := sbLen - offset
	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		idx := start + i
		switch {
		case idx < sbLen:
			if l := sb.Line(idx); l != nil {
				lines = append(lines, l.Render())
			} else {
				lines = append(lines, "")
			}
		case idx-sbLen < len(screen):
			lines = append(lines, screen[idx-sbLen])
		default:
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

// CursorPosition returns the Agent's emulator cursor position.
func (a *Agent) CursorPosition() uv.Position {
	a.emMu.Lock()
	defer a.emMu.Unlock()
	return a.em.CursorPosition()
}

// Rename changes the Agent's display name. Empty names are ignored.
func (a *Agent) Rename(name string) {
	if name == "" {
		return
	}
	a.mu.Lock()
	a.Name = name
	a.mu.Unlock()
	if a.notify != nil {
		a.notify()
	}
}

// MarkRead clears the unread-output flag, e.g. when the Agent becomes focused.
func (a *Agent) MarkRead() {
	a.mu.Lock()
	a.unread = false
	a.mu.Unlock()
}

// ReportHook records a hook event reported by the tool, refining the Agent's
// activity (working/waiting/idle) and tracking the child's current session id
// (so /resume or /clear inside the session keep resume precise). A blank
// sessionID leaves the id unchanged.
func (a *Agent) ReportHook(event, sessionID string) {
	act, known := hookEventActivity[event]
	a.mu.Lock()
	if known {
		a.hookActivity = act
		a.lastEvent = event
		a.lastEventAt = time.Now()
	}
	if sessionID != "" {
		a.resumeID = sessionID
	}
	a.mu.Unlock()
	if (known || sessionID != "") && a.notify != nil {
		a.notify()
	}
}

// Snapshot returns an immutable view of the Agent's current state.
func (a *Agent) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	working := a.status == StatusRunning && time.Since(a.lastOutput) < workingWindow
	waiting := false
	// Hook data, when present, is authoritative over the output heuristic.
	if a.status == StatusRunning && a.hookActivity != HookUnknown {
		working = a.hookActivity == HookWorking
		waiting = a.hookActivity == HookWaiting
	}

	return Snapshot{
		ID:       a.ID,
		Name:     a.Name,
		Tool:     a.Tool,
		Cwd:      a.Cwd,
		Status:   a.status,
		ExitCode: a.exitCode,
		Working:   working,
		Waiting:   waiting,
		Unread:    a.unread,
		Cols:      a.cols,
		Rows:      a.rows,
		Uptime:    time.Since(a.startedAt),
		LastEvent: a.lastEvent,
		ResumeID:  a.resumeID,
	}
}

// close terminates the process (if running) and releases resources. Safe to
// call multiple times.
func (a *Agent) close() {
	a.once.Do(func() {
		if a.cmd != nil && a.cmd.Process != nil {
			_ = a.cmd.Process.Kill()
		}
		// See the exit watcher: a.em.Close() is intentionally not called.
		_ = a.pty.Close()
	})
}
