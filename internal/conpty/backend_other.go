//go:build !windows

package conpty

// Backend reports which ConPTY implementation is active. There is no ConPTY
// on non-Windows platforms, so it is always empty; callers use that to skip
// the diagnostic entirely.
func Backend() string { return "" }
