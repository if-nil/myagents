package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/if-nil/myagents/internal/config"
)

// layoutChoices are the cyclable values for the Layout setting.
var layoutChoices = []string{"auto", "horizontal", "vertical"}

const (
	ratioMin  = 0.10
	ratioMax  = 0.60
	ratioStep = 0.05
)

// openSettings starts the in-app settings editor, seeding live defaults.
func (m *Model) openSettings() {
	m.settings = true
	m.settingsSel = 0
	if m.cfg.RosterRatio <= 0 {
		m.cfg.RosterRatio = m.cfg.RosterFraction()
	}
	if m.cfg.Layout == "" {
		m.cfg.Layout = "auto"
	}
}

// handleSettingsKey processes keys while the settings modal is open. Changes
// apply live; the file is saved when the modal closes.
func (m *Model) handleSettingsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "s", "enter", "ctrl+c", "q":
		m.settings = false
		if err := config.Save(m.cfg); err != nil {
			m.notice = "save settings failed: " + err.Error()
		}
	case "up", "k":
		if m.settingsSel > 0 {
			m.settingsSel--
		}
	case "down", "j":
		if m.settingsSel < 1 {
			m.settingsSel++
		}
	case "left", "h":
		m.adjustSetting(-1)
	case "right", "l":
		m.adjustSetting(1)
	}
	return nil
}

// adjustSetting changes the selected setting by dir and re-applies the layout.
func (m *Model) adjustSetting(dir int) {
	switch m.settingsSel {
	case 0: // Layout
		i := indexOf(layoutChoices, m.cfg.Layout)
		i = (i + dir + len(layoutChoices)) % len(layoutChoices)
		m.cfg.Layout = layoutChoices[i]
	case 1: // Roster ratio
		r := m.cfg.RosterRatio + float64(dir)*ratioStep
		r = float64(int(r*100+0.5)) / 100 // round to 2 decimals
		if r < ratioMin {
			r = ratioMin
		}
		if r > ratioMax {
			r = ratioMax
		}
		m.cfg.RosterRatio = r
	}
	m.vertical = m.computeVertical()
	m.resizeCurrent()
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return 0
}

// settingsRows returns the (label, value) pairs shown in the settings modal.
func (m *Model) settingsRows() [][2]string {
	return [][2]string{
		{"Layout", m.cfg.Layout},
		{"Roster ratio", fmt.Sprintf("%.2f", m.cfg.RosterRatio)},
	}
}
