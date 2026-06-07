//go:build windows

package main

import (
	"os"
	"os/exec"
	"time"

	"golang.org/x/term"
)

// setupPTYCommand is a no-op on Windows; ConPTY (used by xpty) manages the
// pseudo-console and controlling-terminal semantics itself.
func setupPTYCommand(cmd *exec.Cmd) {}

// watchResize polls the console size, since Windows has no SIGWINCH. It emits a
// signal whenever the dimensions change.
func watchResize(ch chan<- struct{}) (stop func()) {
	done := make(chan struct{})
	go func() {
		fd := int(os.Stdin.Fd())
		lastW, lastH, _ := term.GetSize(fd)
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				w, h, err := term.GetSize(fd)
				if err == nil && (w != lastW || h != lastH) {
					lastW, lastH = w, h
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return func() { close(done) }
}
