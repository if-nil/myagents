//go:build windows

package conpty

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fakeFS builds an exists func from a set of paths, so the decision matrix
// can be tested without touching the real filesystem.
func fakeFS(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDecideBackend(t *testing.T) {
	exeDir := `C:\app`
	sideDLL := filepath.Join(exeDir, "conpty.dll")
	sideOC := filepath.Join(exeDir, "OpenConsole.exe")
	envDLL := `D:\redist\conpty.dll`
	envOC := `D:\redist\OpenConsole.exe`

	cases := []struct {
		name     string
		env      map[string]string
		files    []string
		wantDLL  bool
		wantPath string
		wantNote string // substring; empty means no note
	}{
		{
			name:  "default no dll anywhere -> kernel32",
			env:   nil,
			files: nil,
		},
		{
			name:    "dll beside exe -> that dll",
			files:   []string{sideDLL, sideOC},
			wantDLL: true, wantPath: sideDLL,
		},
		{
			name:     "dll beside exe without OpenConsole -> dll with warning",
			files:    []string{sideDLL},
			wantDLL:  true,
			wantPath: sideDLL,
			wantNote: "OpenConsole.exe missing",
		},
		{
			name:  "MYAGENTS_CONPTY=system wins over dll beside exe",
			env:   map[string]string{"MYAGENTS_CONPTY": "system"},
			files: []string{sideDLL, sideOC},
		},
		{
			name: "MYAGENTS_CONPTY=system wins over MYAGENTS_CONPTY_DLL",
			env: map[string]string{
				"MYAGENTS_CONPTY":     "system",
				"MYAGENTS_CONPTY_DLL": envDLL,
			},
			files: []string{envDLL, envOC},
		},
		{
			name:    "MYAGENTS_CONPTY_DLL exists -> exactly that dll",
			env:     map[string]string{"MYAGENTS_CONPTY_DLL": envDLL},
			files:   []string{envDLL, envOC, sideDLL, sideOC},
			wantDLL: true, wantPath: envDLL,
		},
		{
			name:     "MYAGENTS_CONPTY_DLL missing -> kernel32 with note",
			env:      map[string]string{"MYAGENTS_CONPTY_DLL": envDLL},
			files:    []string{sideDLL, sideOC}, // side dll must NOT be picked up
			wantNote: "MYAGENTS_CONPTY_DLL not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideBackend(fakeEnv(tc.env), exeDir, fakeFS(tc.files...))
			if got.useDLL != tc.wantDLL {
				t.Fatalf("useDLL = %v, want %v (choice %+v)", got.useDLL, tc.wantDLL, got)
			}
			if got.dllPath != tc.wantPath {
				t.Fatalf("dllPath = %q, want %q", got.dllPath, tc.wantPath)
			}
			if tc.wantNote == "" && got.note != "" {
				t.Fatalf("unexpected note %q", got.note)
			}
			if tc.wantNote != "" && !strings.Contains(got.note, tc.wantNote) {
				t.Fatalf("note = %q, want substring %q", got.note, tc.wantNote)
			}
		})
	}
}

func TestOpenConsoleNoteArchSubdir(t *testing.T) {
	// OpenConsole.exe in an arch-named subfolder beside the dll also counts
	// (the dll's own secondary lookup location).
	dll := `C:\app\conpty.dll`
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}
	sub, ok := arch[archForTest()]
	if !ok {
		t.Skipf("no arch subdir mapping for %s", archForTest())
	}
	exists := fakeFS(dll, filepath.Join(`C:\app`, sub, "OpenConsole.exe"))
	if note := openConsoleNote(dll, exists); note != "" {
		t.Fatalf("unexpected note %q", note)
	}
}

func TestLoadKernel32Backend(t *testing.T) {
	b := loadBackend(backendChoice{})
	if b.err != nil {
		t.Fatalf("kernel32 backend unavailable: %v", b.err)
	}
	if b.name != "kernel32" {
		t.Fatalf("name = %q, want kernel32", b.name)
	}
	if b.create == nil || b.resize == nil || b.close == nil {
		t.Fatal("kernel32 backend has nil procs")
	}
	if b.release != nil {
		t.Fatal("kernel32 must not expose a release proc")
	}
	testBackendRoundTrip(t, b)
}

// TestConPtyLifecycle exercises the package-level ConPty (whatever backend
// the test environment resolves to — kernel32 unless overridden).
func TestConPtyLifecycle(t *testing.T) {
	if Backend() == "" {
		t.Fatal("Backend() must be non-empty on Windows")
	}
	c, err := New(80, 25)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if w, h, _ := c.Size(); w != 80 || h != 25 {
		t.Fatalf("Size = %dx%d, want 80x25", w, h)
	}
	if err := c.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if w, h, _ := c.Size(); w != 100 || h != 30 {
		t.Fatalf("Size after resize = %dx%d, want 100x30", w, h)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestConPtySpawn(t *testing.T) {
	c, err := New(80, 25)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	const marker = "myagents-spawn-ok"
	pid, handle, err := c.Spawn("cmd.exe", []string{"cmd.exe", "/c", "echo " + marker}, nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if pid <= 0 || handle == 0 {
		t.Fatalf("Spawn returned pid=%d handle=%#x", pid, handle)
	}
	defer windows.CloseHandle(windows.Handle(handle))

	// Drain output in the background; the pipe only sees EOF once the
	// pseudo-console is closed (kernel32) or OpenConsole exits (dll).
	out := make(chan string, 1)
	var buf strings.Builder
	go func() {
		p := make([]byte, 4096)
		for {
			n, err := c.Read(p)
			if n > 0 {
				buf.WriteString(string(p[:n]))
				if strings.Contains(buf.String(), marker) {
					out <- buf.String()
					return
				}
			}
			if err != nil {
				out <- buf.String()
				return
			}
		}
	}()

	if ev, err := windows.WaitForSingleObject(windows.Handle(handle), 10000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("child did not exit: ev=%d err=%v", ev, err)
	}
	select {
	case got := <-out:
		if !strings.Contains(got, marker) {
			t.Fatalf("output missing %q: %q", marker, got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for child output")
	}
}

// TestRedistributableDLL loads a real redistributable conpty.dll when one can
// be found on this machine (VS Code's bundled node-pty, or an explicit
// MYAGENTS_CONPTY_TEST_DLL) and runs a create/resize/close round trip through
// it. Skipped when no dll is available.
func TestRedistributableDLL(t *testing.T) {
	dll := findTestDLL()
	if dll == "" {
		t.Skip("no redistributable conpty.dll found (VS Code node-pty not installed; set MYAGENTS_CONPTY_TEST_DLL to point at one)")
	}
	b, err := loadDLLBackend(dll)
	if err != nil {
		t.Fatalf("loadDLLBackend(%s): %v", dll, err)
	}
	if b.name == "kernel32" {
		t.Fatalf("expected dll backend, got %q", b.name)
	}
	if b.release == nil {
		t.Error("redistributable dll should export ConptyReleasePseudoConsole")
	}
	testBackendRoundTrip(t, b)
}

// findTestDLL looks in the places a redistributable conpty.dll plausibly
// lives on a dev machine.
func findTestDLL() string {
	if p := os.Getenv("MYAGENTS_CONPTY_TEST_DLL"); p != "" && fileExists(p) {
		return p
	}
	const nodePty = `resources\app\node_modules.asar.unpacked\node-pty\build\Release\conpty\conpty.dll`
	var roots []string
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		roots = append(roots,
			filepath.Join(d, `Programs\Microsoft VS Code`),
			filepath.Join(d, `Programs\Microsoft VS Code Insiders`),
		)
	}
	if d := os.Getenv("ProgramFiles"); d != "" {
		roots = append(roots,
			filepath.Join(d, `Microsoft VS Code`),
			filepath.Join(d, `Microsoft VS Code Insiders`),
		)
	}
	for _, r := range roots {
		if p := filepath.Join(r, nodePty); fileExists(p) {
			return p
		}
	}
	return ""
}

// testBackendRoundTrip creates, resizes, and closes a pseudo-console through
// the given backend's raw procs.
func testBackendRoundTrip(t *testing.T, b *backend) {
	t.Helper()
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		t.Fatalf("CreatePipe: %v", err)
	}
	defer windows.CloseHandle(inR)
	defer windows.CloseHandle(inW)
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		t.Fatalf("CreatePipe: %v", err)
	}
	defer windows.CloseHandle(outR)
	defer windows.CloseHandle(outW)

	var hpc windows.Handle
	r0, _, _ := b.create.Call(packCoord(windows.Coord{X: 80, Y: 25}), uintptr(inR), uintptr(outW), 0, uintptr(unsafe.Pointer(&hpc)))
	if err := hresult(r0); err != nil {
		t.Fatalf("create: %v", err)
	}
	r0, _, _ = b.resize.Call(uintptr(hpc), packCoord(windows.Coord{X: 100, Y: 30}))
	if err := hresult(r0); err != nil {
		t.Errorf("resize: %v", err)
	}
	_, _, _ = b.close.Call(uintptr(hpc))
}

// archForTest mirrors the GOARCH value openConsoleNote maps to a subfolder.
func archForTest() string { return runtime.GOARCH }
