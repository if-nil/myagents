package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/if-nil/myagents/internal/agent"
	"github.com/if-nil/myagents/internal/config"
)

// keyPress builds a KeyPressMsg for a special key with modifiers, matching the
// msg.String() forms the handlers switch on (e.g. "ctrl+l", "shift+pgup").
func keyPress(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod}
}

// Ctrl+L must work with an empty roster too: no agent to repaint, but the
// frame cache is still dropped and the host terminal still cleared.
func TestCtrlLWithEmptyRoster(t *testing.T) {
	mgr := agent.NewInProcessManager()
	t.Cleanup(func() { mgr.Close() })
	m := New(mgr, config.Default(), nil)
	m.width, m.height = 100, 30
	m.vertical = m.computeVertical()

	m.frame = "stale frame"
	cmd := m.handleManageKey(keyPress('l', tea.ModCtrl))
	if m.frame != "" {
		t.Errorf("frame cache = %q, want empty (invalidated)", m.frame)
	}
	if cmd == nil {
		t.Error("ctrl+l returned nil cmd, want tea.ClearScreen")
	}
}
