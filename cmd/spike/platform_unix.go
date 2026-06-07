//go:build unix

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// setupPTYCommand makes the child the session leader with the PTY slave as its
// controlling terminal. xpty's Start wires the slave to stdio but does not set
// this up, so without it job control and full-screen apps misbehave. (See
// tuios internal/terminal/pty_unix.go.) Ctty:0 = the child's stdin fd, which
// xpty points at the PTY slave.
func setupPTYCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
}

// watchResize delivers a signal on every terminal size change via SIGWINCH.
func watchResize(ch chan<- struct{}) (stop func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	go func() {
		for range sig {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
	return func() { signal.Stop(sig) }
}
