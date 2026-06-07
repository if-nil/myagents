//go:build unix

package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/if-nil/myagents/internal/agent"
	"github.com/if-nil/myagents/internal/config"
	"github.com/if-nil/myagents/internal/store"
)

// assertFrame checks the rendered frame is an exact width×height rectangle, the
// invariant that keeps the bordered panels aligned.
func assertFrame(t *testing.T, m *Model) {
	t.Helper()
	frame := m.buildFrame()
	lines := strings.Split(frame, "\n")
	if len(lines) != m.height {
		t.Errorf("frame has %d lines, want %d", len(lines), m.height)
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w != m.width {
			t.Errorf("line %d width = %d, want %d: %q", i, w, m.width, ln)
		}
	}
}

func newTestModel(t *testing.T, agents int) *Model {
	t.Helper()
	mgr := agent.NewInProcessManager()
	t.Cleanup(func() { mgr.Close() })
	for i := 0; i < agents; i++ {
		if _, err := mgr.Spawn(agent.SpawnSpec{Tool: "sh", Name: "agent", Command: []string{"/bin/sh", "-c", "sleep 30"}}); err != nil {
			t.Fatalf("spawn: %v", err)
		}
	}
	time.Sleep(50 * time.Millisecond) // let them reach running
	return New(mgr, config.Default(), nil)
}

func TestFrameDimensions(t *testing.T) {
	sizes := []struct{ w, h int }{
		{120, 40}, // wide -> horizontal
		{80, 24},  // standard -> horizontal
		{50, 60},  // tall/narrow -> vertical
		{40, 30},  // narrow -> vertical
	}
	for _, n := range []int{0, 1, 3} {
		for _, s := range sizes {
			m := newTestModel(t, n)
			m.width, m.height = s.w, s.h
			m.vertical = m.computeVertical()
			assertFrame(t, m)
		}
	}
}

func TestFrameModesAndModals(t *testing.T) {
	m := newTestModel(t, 2)
	m.width, m.height = 100, 30
	m.vertical = m.computeVertical()

	m.mode = OperateMode
	assertFrame(t, m)

	m.mode = ManageMode
	m.confirmingQuit = true
	assertFrame(t, m)
	m.confirmingQuit = false

	m.launcher.close()
	m.renaming = true
	m.renameBuf = "a-very-long-name-that-should-be-clipped-to-the-panel-width"
	assertFrame(t, m)
	m.renaming = false

	m.openSettings()
	assertFrame(t, m)
}

func TestFrameWithSavedSessions(t *testing.T) {
	m := newTestModel(t, 1)
	m.saved = []store.SavedSession{
		{Name: "frontend", Tool: "claude", Cwd: "/work/web"},
		{Name: "api", Tool: "codex", Cwd: "/work/api"},
	}
	for _, s := range []struct{ w, h int }{{120, 40}, {50, 60}} {
		m.width, m.height = s.w, s.h
		m.vertical = m.computeVertical()
		// Select a saved (not-live) entry: index past the single live agent.
		m.selected = 2
		assertFrame(t, m)
	}
}
