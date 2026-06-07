//go:build windows

package agent

import "os/exec"

// setupPTYCommand is a no-op on Windows; ConPTY (used by xpty) manages the
// pseudo-console and controlling-terminal semantics itself.
func setupPTYCommand(cmd *exec.Cmd) {}
