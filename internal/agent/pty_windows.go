//go:build windows

package agent

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/windows"

	"github.com/if-nil/myagents/internal/conpty"
)

// windowsPty adapts internal/conpty.ConPty to the xpty.Pty interface the agent
// uses. The embedded *conpty.ConPty already supplies Fd/Read/Write/Close/
// Resize/Size; only Name and Start need adapting. We route through
// internal/conpty (rather than xpty's own ConPTY) so the process can load a
// newer redistributable conpty.dll on old Windows builds — see internal/conpty.
type windowsPty struct {
	*conpty.ConPty
}

var _ xpty.Pty = (*windowsPty)(nil)

// newPty creates the platform PTY. On Windows it is backed by internal/conpty,
// whose backend (kernel32 or a redistributable conpty.dll) is chosen once per
// process; see internal/conpty/dll_windows.go.
func newPty(cols, rows int) (xpty.Pty, error) {
	c, err := conpty.New(cols, rows)
	if err != nil {
		return nil, err
	}
	return &windowsPty{c}, nil
}

// Name identifies the PTY kind, matching xpty's ConPty.
func (p *windowsPty) Name() string { return "windows-pty" }

// Start spawns cmd attached to the pseudo-console. It mirrors xpty's ConPty
// Start exactly (xpty@v0.1.3 conpty_windows.go): Spawn returns the raw pid and
// process handle; we wrap the pid in cmd.Process so the manager's existing
// cmd.Wait()/Process.Kill() paths keep working, and terminate the spawned
// process if FindProcess fails (the only handle we have on it).
func (p *windowsPty) Start(cmd *exec.Cmd) error {
	pid, proc, err := p.ConPty.Spawn(cmd.Path, cmd.Args, &syscall.ProcAttr{
		Dir: cmd.Dir,
		Env: cmd.Env,
		Sys: cmd.SysProcAttr,
	})
	if err != nil {
		return err
	}

	cmd.Process, err = os.FindProcess(pid)
	if err != nil {
		// We could not adopt the process; terminate it via the raw handle so it
		// does not leak, since cmd.Process (our only later handle on it) is nil.
		_ = windows.TerminateProcess(windows.Handle(proc), 1)
		return err
	}
	return nil
}

// setupPTYCommand is a no-op on Windows; ConPTY (used by internal/conpty)
// manages the pseudo-console and controlling-terminal semantics itself.
func setupPTYCommand(cmd *exec.Cmd) {}
