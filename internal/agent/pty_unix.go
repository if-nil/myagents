//go:build unix

package agent

import (
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
)

// newPty creates the platform PTY. On Unix this is the classic xpty PTY,
// unchanged from before the Windows ConPTY-backend split; the Windows build
// has its own newPty that can load a redistributable conpty.dll.
func newPty(cols, rows int) (xpty.Pty, error) {
	return xpty.NewPty(cols, rows)
}

// setupPTYCommand makes the child a session leader with the PTY slave as its
// controlling terminal. xpty wires the slave to stdio but does not set this up,
// and full-screen apps need it for job control and signals. Ctty:0 is the
// child's stdin fd, which xpty points at the PTY slave. (See the spike and
// tuios internal/terminal/pty_unix.go.)
func setupPTYCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
}
