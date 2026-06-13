//go:build windows

package conpty

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// This file decides where the three ConPTY entry points (create/resize/close)
// come from. By default that is kernel32, i.e. the ConPTY frozen into the OS
// build. When a redistributable conpty.dll sits next to myagents.exe (or is
// named via MYAGENTS_CONPTY_DLL) we load that instead, the same way VS Code's
// node-pty does with useConptyDll, so users on old Windows builds get current
// ConPTY fixes without an OS upgrade.

// apiProc abstracts over windows.LazyProc (kernel32) and raw GetProcAddress
// addresses (loaded dll) so the rest of the package calls them uniformly.
type apiProc interface {
	Call(args ...uintptr) (r1, r2 uintptr, lastErr error)
}

// rawProc is a proc address obtained via GetProcAddress on a loaded dll.
type rawProc uintptr

// Call invokes the proc. Like windows.(*LazyProc).Call, the returned error is
// not meaningful for HRESULT-style APIs; callers must inspect r1.
//
//go:uintptrescapes
func (p rawProc) Call(args ...uintptr) (uintptr, uintptr, error) {
	r1, r2, errno := syscall.SyscallN(uintptr(p), args...)
	return r1, r2, errno
}

// backend holds the resolved ConPTY entry points plus diagnostics.
type backend struct {
	name string // "kernel32" or the absolute dll path
	note string // short human-readable note when something fell back
	err  error  // non-nil when no usable ConPTY API exists at all

	create apiProc // (Conpty)CreatePseudoConsole
	resize apiProc // (Conpty)ResizePseudoConsole
	close  apiProc // (Conpty)ClosePseudoConsole
	// release is ConptyReleasePseudoConsole, dll-only (nil on kernel32). It
	// closes the \Reference handle so OpenConsole exits once its last client
	// does, which turns child exit into EOF on our output pipe (node-pty
	// useConptyDll semantics).
	release apiProc
}

// backendChoice is the outcome of the pure decision step: which dll to try
// (if any), plus a note explaining any decision-time fallback. Loading can
// still fall back to kernel32 afterwards (missing exports, load failure).
type backendChoice struct {
	useDLL  bool
	dllPath string
	note    string
}

// decideBackend picks the candidate backend from the environment and the
// filesystem. It is pure (no LoadLibrary, no globals) so tests can table-test
// the decision matrix. Resolution order:
//
//  1. MYAGENTS_CONPTY=system        -> kernel32 unconditionally.
//  2. MYAGENTS_CONPTY_DLL=<path>    -> exactly that dll (kernel32 if missing).
//  3. conpty.dll next to the binary -> that dll.
//  4. otherwise                     -> kernel32.
func decideBackend(getenv func(string) string, exeDir string, exists func(string) bool) backendChoice {
	if getenv("MYAGENTS_CONPTY") == "system" {
		return backendChoice{}
	}
	if p := getenv("MYAGENTS_CONPTY_DLL"); p != "" {
		if !exists(p) {
			return backendChoice{note: "MYAGENTS_CONPTY_DLL not found: " + p}
		}
		return backendChoice{useDLL: true, dllPath: p, note: openConsoleNote(p, exists)}
	}
	if exeDir != "" {
		if p := filepath.Join(exeDir, "conpty.dll"); exists(p) {
			return backendChoice{useDLL: true, dllPath: p, note: openConsoleNote(p, exists)}
		}
	}
	return backendChoice{}
}

// openConsoleNote warns when OpenConsole.exe is not placed where conpty.dll
// will look for it. The dll resolves its console host once: OpenConsole.exe
// beside the dll, then <arch>\OpenConsole.exe, then — silently — the inbox
// System32\conhost.exe, which would quietly bring back every frozen ConPTY
// bug this backend exists to avoid. There is no error from the dll, so this
// note is the only signal users get.
func openConsoleNote(dllPath string, exists func(string) bool) string {
	dir := filepath.Dir(dllPath)
	if exists(filepath.Join(dir, "OpenConsole.exe")) {
		return ""
	}
	// The dll's arch-subfolder lookup uses the native machine architecture;
	// the process arch is a close-enough approximation for a diagnostic.
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}[runtime.GOARCH]
	if arch != "" && exists(filepath.Join(dir, arch, "OpenConsole.exe")) {
		return ""
	}
	return "OpenConsole.exe missing beside dll; conpty.dll silently falls back to the inbox conhost"
}

// loadBackend turns a decision into resolved entry points, falling back to
// kernel32 (with the reason recorded in note) when the chosen dll cannot be
// loaded or lacks required exports.
func loadBackend(c backendChoice) *backend {
	note := c.note
	if c.useDLL {
		b, err := loadDLLBackend(c.dllPath)
		if err == nil {
			b.note = note
			return b
		}
		note = fmt.Sprintf("dll fallback: %v", err)
	}
	b := loadKernel32Backend()
	b.note = note
	return b
}

// loadDLLBackend loads a redistributable conpty.dll and resolves its exports.
// New consumers are expected to use the Conpty-prefixed names (per the dll's
// own .def file); the unprefixed kernel32-compatible aliases are a fallback.
func loadDLLBackend(path string) (*backend, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("conpty dll path: %w", err)
	}
	h, err := windows.LoadLibraryEx(abs, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", abs, err)
	}

	var missing string
	lookup := func(names ...string) apiProc {
		for _, n := range names {
			if addr, err := windows.GetProcAddress(h, n); err == nil && addr != 0 {
				return rawProc(addr)
			}
		}
		if missing == "" {
			missing = names[0]
		}
		return nil
	}

	b := &backend{name: abs}
	b.create = lookup("ConptyCreatePseudoConsole", "CreatePseudoConsole")
	b.resize = lookup("ConptyResizePseudoConsole", "ResizePseudoConsole")
	b.close = lookup("ConptyClosePseudoConsole", "ClosePseudoConsole")
	if missing != "" {
		_ = windows.FreeLibrary(h)
		return nil, fmt.Errorf("%s lacks export %s", abs, missing)
	}
	// Optional: only the redistributable dll exports release. Its absence is
	// fine (we just keep kernel32-style lifetime semantics for that dll).
	if addr, err := windows.GetProcAddress(h, "ConptyReleasePseudoConsole"); err == nil && addr != 0 {
		b.release = rawProc(addr)
	}
	return b, nil
}

// loadKernel32Backend resolves the OS-provided ConPTY API. The exports exist
// on Windows 10 1809+; on anything older the backend carries an error and
// New refuses to construct a ConPty.
func loadKernel32Backend() *backend {
	k32 := windows.NewLazySystemDLL("kernel32.dll")
	b := &backend{name: "kernel32"}
	for _, p := range []struct {
		name string
		dst  *apiProc
	}{
		{"CreatePseudoConsole", &b.create},
		{"ResizePseudoConsole", &b.resize},
		{"ClosePseudoConsole", &b.close},
	} {
		proc := k32.NewProc(p.name)
		if err := proc.Find(); err != nil {
			b.err = fmt.Errorf("conpty: kernel32 lacks %s (Windows 10 1809+ required): %w", p.name, err)
			return b
		}
		*p.dst = proc
	}
	return b
}

var (
	backendOnce sync.Once
	active      *backend
)

// getBackend resolves the process-wide backend exactly once. The decision
// depends only on the environment and the executable's directory, neither of
// which usefully changes mid-process.
func getBackend() *backend {
	backendOnce.Do(func() {
		exeDir := ""
		if exe, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(exe)
		}
		active = loadBackend(decideBackend(os.Getenv, exeDir, fileExists))
	})
	return active
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// Backend reports which ConPTY implementation is in use: "kernel32" or the
// loaded dll path, suffixed with a short note when a fallback happened.
// Diagnostics only — shown by `myagents version`.
func Backend() string {
	b := getBackend()
	if b.note != "" {
		return b.name + " (" + b.note + ")"
	}
	return b.name
}

// hresult converts a proc.Call first return value to an error the way x/sys
// zsyscall wrappers do: zero is S_OK, anything else is the raw HRESULT as a
// syscall.Errno.
func hresult(r0 uintptr) error {
	if r0 != 0 {
		return syscall.Errno(r0)
	}
	return nil
}
