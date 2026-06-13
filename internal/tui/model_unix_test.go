//go:build unix

package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/if-nil/myagents/internal/agent"
	"github.com/if-nil/myagents/internal/config"
)

// waitForTUI polls until cond is true or the deadline passes.
func waitForTUI(t *testing.T, d time.Duration, cond func() bool) {
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

// newHistoryModel returns a model with one live agent that has scrollback (60
// lines through an 8-row PTY) and stays alive for forwarding tests.
func newHistoryModel(t *testing.T) (*Model, *agent.Agent) {
	t.Helper()
	mgr := agent.NewInProcessManager()
	t.Cleanup(func() { mgr.Close() })
	a, err := mgr.Spawn(agent.SpawnSpec{
		Tool:    "sh",
		Name:    "hist",
		Command: []string{"/bin/sh", "-c", `for i in $(seq 1 60); do echo "line$i"; done; sleep 30`},
		Cols:    40,
		Rows:    8,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// Wait for the output burst to land AND settle, so Render() comparisons in
	// the forwarding test are stable.
	last := -1
	waitForTUI(t, 3*time.Second, func() bool {
		n := a.ScrollbackLen()
		stable := n > 0 && n == last
		last = n
		return stable
	})
	m := New(mgr, config.Default(), nil)
	m.width, m.height = 100, 30
	m.vertical = m.computeVertical()
	return m, a
}

func TestScrollByNotice(t *testing.T) {
	// No scrollback (Win10 ConPTY repaints in place; here simply a fresh
	// agent): an upward scroll that goes nowhere must explain itself.
	m := newTestModel(t, 1)
	m.width, m.height = 100, 30
	m.vertical = m.computeVertical()
	m.scrollBy(5)
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want 0 (no history)", m.scroll)
	}
	if m.notice != "no scrollback captured for this agent" {
		t.Errorf("notice = %q, want the no-scrollback message", m.notice)
	}

	// Downward scrolls at the bottom are not an error and set no notice.
	m.notice = ""
	m.scrollBy(-5)
	if m.notice != "" {
		t.Errorf("notice after downward no-op = %q, want empty", m.notice)
	}

	// With history, a scroll that moves the offset clears a stale notice.
	m2, _ := newHistoryModel(t)
	m2.notice = "no scrollback captured for this agent"
	m2.scrollBy(3)
	if m2.scroll != 3 {
		t.Fatalf("scroll = %d, want 3", m2.scroll)
	}
	if m2.notice != "" {
		t.Errorf("notice after effective scroll = %q, want empty", m2.notice)
	}
}

func TestCtrlLInvalidatesFrameCache(t *testing.T) {
	m := newTestModel(t, 1)
	m.width, m.height = 100, 30
	m.vertical = m.computeVertical()
	m.resizeCurrent() // give the agent real cols/rows for ForceRepaint

	m.frame = "stale frame"
	cmd := m.handleManageKey(keyPress('l', tea.ModCtrl))
	if m.frame != "" {
		t.Errorf("frame cache = %q, want empty (invalidated)", m.frame)
	}
	if cmd == nil {
		t.Error("ctrl+l returned nil cmd, want tea.ClearScreen")
	}
}

func TestOperateShiftPageScrolls(t *testing.T) {
	m, a := newHistoryModel(t)
	m.mode = OperateMode

	before := a.Render()
	if cmd := m.handleOperateKey(keyPress(tea.KeyPgUp, tea.ModShift)); cmd != nil {
		t.Errorf("shift+pgup returned a cmd, want nil")
	}
	if want := m.stageHeight() - 1; m.scroll != want {
		t.Errorf("scroll = %d, want %d (one page up)", m.scroll, want)
	}

	// Not forwarded: the child saw no input, so its screen is unchanged (a
	// forwarded page-up would echo an escape sequence through /bin/sh).
	time.Sleep(50 * time.Millisecond)
	if after := a.Render(); after != before {
		t.Errorf("screen changed after shift+pgup; key was forwarded:\n%q", after)
	}

	if cmd := m.handleOperateKey(keyPress(tea.KeyPgDown, tea.ModShift)); cmd != nil {
		t.Errorf("shift+pgdown returned a cmd, want nil")
	}
	if m.scroll != 0 {
		t.Errorf("scroll = %d, want 0 (back to live)", m.scroll)
	}
}
