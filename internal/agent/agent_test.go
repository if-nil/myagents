//go:build unix

package agent

import (
	"strings"
	"testing"
	"time"
)

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within " + d.String())
}

func TestSpawnCapturesOutputAndExitCode(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()

	a, err := m.Spawn(SpawnSpec{
		Tool:    "echo-test",
		Command: []string{"/bin/sh", "-c", `printf 'AGENT-OUTPUT'; exit 0`},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		return a.Snapshot().Status == StatusExited
	})

	snap := a.Snapshot()
	if snap.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", snap.ExitCode)
	}
	if !strings.Contains(a.Render(), "AGENT-OUTPUT") {
		t.Errorf("render missing output:\n%q", a.Render())
	}
	if snap.Name != "echo-test" {
		t.Errorf("name = %q, want echo-test", snap.Name)
	}
}

func TestSpawnNonZeroExit(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()

	a, err := m.Spawn(SpawnSpec{Command: []string{"/bin/sh", "-c", "exit 7"}})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		return a.Snapshot().Status == StatusExited
	})
	if got := a.Snapshot().ExitCode; got != 7 {
		t.Errorf("exit code = %d, want 7", got)
	}
}

func TestKillKeepsAgentAsExited(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()

	a, err := m.Spawn(SpawnSpec{Command: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.Snapshot().Status == StatusRunning })

	if err := m.Kill(a.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		return a.Snapshot().Status != StatusRunning
	})

	// Killed process stays in the roster (not auto-removed).
	if _, ok := m.Get(a.ID); !ok {
		t.Error("agent removed after kill; should be kept as exited")
	}
	if len(m.List()) != 1 {
		t.Errorf("roster size = %d, want 1", len(m.List()))
	}
}

func TestRemoveRefusesRunning(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()

	a, err := m.Spawn(SpawnSpec{Command: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.Snapshot().Status == StatusRunning })

	if err := m.Remove(a.ID); err != ErrStillRunning {
		t.Fatalf("remove running = %v, want ErrStillRunning", err)
	}
	_ = m.Kill(a.ID)
	waitFor(t, 3*time.Second, func() bool { return a.Snapshot().Status != StatusRunning })
	if err := m.Remove(a.ID); err != nil {
		t.Fatalf("remove after kill: %v", err)
	}
	if len(m.List()) != 0 {
		t.Errorf("roster size = %d, want 0", len(m.List()))
	}
}

func TestNotifyFiresOnOutput(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()

	if _, err := m.Spawn(SpawnSpec{Command: []string{"/bin/sh", "-c", "printf hi; sleep 0.2"}}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	select {
	case <-m.Notify():
	case <-time.After(2 * time.Second):
		t.Fatal("no notify signal on output")
	}
}

func TestSendTextReachesChild(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()

	// `cat` echoes stdin back to stdout; with PTY echo on, our input round-trips
	// through the child and shows up on the emulator screen.
	a, err := m.Spawn(SpawnSpec{Command: []string{"/bin/cat"}})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.Snapshot().Status == StatusRunning })

	a.SendText("ROUNDTRIP")
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(a.Render(), "ROUNDTRIP")
	})
}

func TestScrollbackViewShowsHistory(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()

	// A short screen forces early lines into scrollback.
	a, err := m.Spawn(SpawnSpec{
		Command: []string{"/bin/sh", "-c", `for i in $(seq 1 60); do echo "line$i"; done`},
		Rows:    8,
		Cols:    40,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool { return a.Snapshot().Status == StatusExited })
	waitFor(t, 2*time.Second, func() bool { return a.ScrollbackLen() > 0 })

	// The live screen shows only the tail; early lines are reachable only via
	// the scrolled view.
	live := a.Render()
	if strings.Contains(live, "line1\n") || strings.Contains(live, "line2\n") {
		t.Logf("note: early lines unexpectedly on live screen:\n%s", live)
	}
	scrolled := a.RenderView(a.ScrollbackLen(), 8)
	if !strings.Contains(scrolled, "line1") {
		t.Errorf("scrolled view missing early history; got:\n%q", scrolled)
	}
}

func TestEmptyCommandRejected(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()
	if _, err := m.Spawn(SpawnSpec{}); err != ErrEmptyCommand {
		t.Fatalf("err = %v, want ErrEmptyCommand", err)
	}
}
