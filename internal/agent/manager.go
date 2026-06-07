package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// modeDebugLog, when MYAGENTS_MODE_DEBUG names a file, receives a line per DEC
// mode the child enables/disables — useful for diagnosing scroll behavior.
var modeDebugLog = func() *os.File {
	if p := os.Getenv("MYAGENTS_MODE_DEBUG"); p != "" {
		f, _ := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		return f
	}
	return nil
}()

func modeDebug(action, tool string, mode ansi.Mode) {
	if modeDebugLog == nil {
		return
	}
	fmt.Fprintf(modeDebugLog, "%s tool=%s mode=%v\n", action, tool, mode)
}

// isMouseReportMode reports whether a DEC mode is one that makes the child
// expect mouse events (any of the X10/normal/button/any tracking modes).
func isMouseReportMode(mode ansi.Mode) bool {
	switch mode {
	case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent:
		return true
	}
	return false
}

// Default PTY size used when a spec omits dimensions.
const (
	defaultCols = 80
	defaultRows = 24
)

var (
	// ErrNotFound is returned when no Agent matches the given id.
	ErrNotFound = errors.New("agent not found")
	// ErrEmptyCommand is returned when a SpawnSpec has no command.
	ErrEmptyCommand = errors.New("spawn: empty command")
	// ErrStillRunning is returned when removing an Agent that is still alive.
	ErrStillRunning = errors.New("agent still running")
)

// SpawnSpec describes a new Agent to create.
type SpawnSpec struct {
	Name    string   // user-facing label; defaults to the tool name
	Tool    string   // configured tool name (claude, codex, ...)
	Command []string // argv to execute; Command[0] is the program
	Cwd     string   // working directory; empty means inherit
	Env     []string // extra environment, appended to os.Environ()
	Cols    int      // initial PTY width; 0 -> defaultCols
	Rows    int      // initial PTY height; 0 -> defaultRows
	// HookStyle selects how (if at all) to wire the tool's hooks back to this
	// process for precise status. "claude" injects a per-session --settings;
	// "" disables hooks (status falls back to the output heuristic).
	HookStyle string
	// ResumeID is an opaque session id (e.g. claude's --session-id UUID) carried
	// for precise resume; persisted with the saved session.
	ResumeID string
}

// AgentManager owns the set of Agents and their lifecycle. The interface is the
// seam that lets us swap the in-process implementation for a daemon-backed one
// later without touching the UI (see docs/adr/0001).
type AgentManager interface {
	Spawn(spec SpawnSpec) (*Agent, error)
	List() []*Agent
	Get(id string) (*Agent, bool)
	Kill(id string) error
	Remove(id string) error
	// Notify returns a coalesced signal channel; a value is sent whenever any
	// Agent's screen or status changes. Drain it to drive UI redraws.
	Notify() <-chan struct{}
	Close() error
}

// InProcessManager runs every Agent as a child of the current process. When the
// process exits, all Agents die with it (docs/adr/0001).
type InProcessManager struct {
	mu     sync.RWMutex
	order  []string
	byID   map[string]*Agent
	notify chan struct{}
	seq    atomic.Uint64

	hookLn   net.Listener
	hookAddr string
	selfPath string // this binary, used as the hook command (overridable in tests)
}

var _ AgentManager = (*InProcessManager)(nil)

// NewInProcessManager returns a ready-to-use in-process manager. It starts a
// loopback listener that receives hook reports from tools that support them
// (best-effort: if the listener cannot start, hooks are simply disabled).
func NewInProcessManager() *InProcessManager {
	m := &InProcessManager{
		byID:   make(map[string]*Agent),
		notify: make(chan struct{}, 1),
	}
	m.selfPath, _ = os.Executable()
	if ln, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
		m.hookLn = ln
		m.hookAddr = ln.Addr().String()
		go m.serveHooks(ln)
	}
	return m
}

// serveHooks accepts hook reports and dispatches them to the named Agent. Each
// connection carries one JSON report: {"agent":"a1","event":"Notification"}.
func (m *InProcessManager) serveHooks(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			data, _ := io.ReadAll(io.LimitReader(c, 4096))
			var rep struct {
				Agent   string `json:"agent"`
				Event   string `json:"event"`
				Session string `json:"session"`
			}
			if json.Unmarshal(data, &rep) != nil {
				return
			}
			if a, ok := m.Get(rep.Agent); ok {
				a.ReportHook(rep.Event, rep.Session)
			}
		}(conn)
	}
}

// signal performs a non-blocking, coalescing send on the notify channel.
func (m *InProcessManager) signal() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// Notify implements AgentManager.
func (m *InProcessManager) Notify() <-chan struct{} { return m.notify }

// Spawn implements AgentManager.
func (m *InProcessManager) Spawn(spec SpawnSpec) (*Agent, error) {
	if len(spec.Command) == 0 || spec.Command[0] == "" {
		return nil, ErrEmptyCommand
	}
	cols, rows := spec.Cols, spec.Rows
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}

	pty, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("new pty: %w", err)
	}

	id := "a" + strconv.FormatUint(m.seq.Add(1), 10)

	// Build argv, injecting per-session hook wiring for supporting tools. This
	// never modifies the user's settings files (see ADR 0004).
	argv := append([]string(nil), spec.Command...)
	if spec.HookStyle == "claude" && m.hookAddr != "" && m.selfPath != "" {
		// Merge our hooks into any user-provided --settings; on error keep the
		// original argv (the user's --settings still applies, just no hooks).
		if merged, err := injectClaudeHooks(m.selfPath, id, m.hookAddr, argv); err == nil {
			argv = merged
		}
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.Env = append(cmd.Env, spec.Env...)
	setupPTYCommand(cmd)

	name := spec.Name
	if name == "" {
		name = spec.Tool
	}
	if name == "" {
		name = spec.Command[0]
	}

	a := &Agent{
		ID:       id,
		Name:     name,
		Tool:     spec.Tool,
		Cwd:      spec.Cwd,
		resumeID: spec.ResumeID,
		em:     vt.NewEmulator(cols, rows),
		pty:    pty,
		cmd:    cmd,
		status: StatusStarting,
		cols:   cols,
		rows:   rows,
		notify: m.signal,
	}

	// Track the child's mouse-reporting mode (used by the UI for scroll
	// handling). Optional debug logging records every DEC mode the child
	// toggles, to diagnose how a given tool scrolls.
	a.em.SetCallbacks(vt.Callbacks{
		EnableMode: func(mode ansi.Mode) {
			modeDebug("enable", spec.Tool, mode)
			if isMouseReportMode(mode) {
				a.SetMouseEnabled(true)
			}
		},
		DisableMode: func(mode ansi.Mode) {
			modeDebug("disable", spec.Tool, mode)
			if isMouseReportMode(mode) {
				a.SetMouseEnabled(false)
			}
		},
	})

	if err := a.start(); err != nil {
		_ = pty.Close()
		return nil, fmt.Errorf("start %q: %w", spec.Command[0], err)
	}

	m.mu.Lock()
	m.byID[id] = a
	m.order = append(m.order, id)
	m.mu.Unlock()
	m.signal()
	return a, nil
}

// List implements AgentManager, returning Agents in creation order.
func (m *InProcessManager) List() []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Agent, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.byID[id])
	}
	return out
}

// Get implements AgentManager.
func (m *InProcessManager) Get(id string) (*Agent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.byID[id]
	return a, ok
}

// Kill implements AgentManager: terminates the process but keeps the Agent in
// the roster as exited (docs design: 退出处理 = 保留为 exited).
func (m *InProcessManager) Kill(id string) error {
	a, ok := m.Get(id)
	if !ok {
		return ErrNotFound
	}
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
	return nil
}

// Remove implements AgentManager: drops the Agent from the roster. Refuses to
// remove a still-running Agent; Kill it first.
func (m *InProcessManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	if s := a.Snapshot().Status; s == StatusRunning || s == StatusStarting {
		return ErrStillRunning
	}
	a.close()
	delete(m.byID, id)
	for i, oid := range m.order {
		if oid == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.signal()
	return nil
}

// Close implements AgentManager: kills and releases every Agent and stops the
// hook listener.
func (m *InProcessManager) Close() error {
	if m.hookLn != nil {
		_ = m.hookLn.Close()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.byID {
		a.close()
	}
	m.byID = make(map[string]*Agent)
	m.order = nil
	return nil
}
