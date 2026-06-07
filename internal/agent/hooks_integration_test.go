//go:build unix

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestClaudeHooksEndToEnd exercises the real production path: Spawn a claude
// agent with HookStyle "claude" (which injects --settings) and confirm claude
// actually invokes our hook subcommand and reports an activity back. Requires
// claude on PATH and network; opt in with MYAGENTS_E2E=1.
func TestClaudeHooksEndToEnd(t *testing.T) {
	if os.Getenv("MYAGENTS_E2E") == "" {
		t.Skip("set MYAGENTS_E2E=1 to run the live claude hook test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	// Build the real myagents binary so the injected hook command has the
	// `hook` subcommand (under `go test`, os.Executable() is the test binary).
	bin := filepath.Join(t.TempDir(), "myagents")
	build := exec.Command("go", "build", "-o", bin, "github.com/if-nil/myagents/cmd/myagents")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build myagents: %v\n%s", err, out)
	}

	m := NewInProcessManager()
	defer m.Close()
	if m.hookAddr == "" {
		t.Fatal("no hook listener")
	}
	m.selfPath = bin // use the real binary as the hook command

	a, err := m.Spawn(SpawnSpec{
		Tool:      "claude",
		Command:   []string{"claude", "-p", "say hi in one word"},
		HookStyle: "claude",
		Cols:      80,
		Rows:      24,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Within the turn we expect at least one hook report to flip activity away
	// from HookUnknown.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.RLock()
		got := a.hookActivity
		a.mu.RUnlock()
		if got != HookUnknown {
			t.Logf("received hook activity: %q", got)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no hook activity reported by claude within timeout (production path broken)")
}
