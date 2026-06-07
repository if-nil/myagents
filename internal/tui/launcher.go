package tui

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/if-nil/myagents/internal/agent"
	"github.com/if-nil/myagents/internal/config"
)

// launcherStep is the current step of the new-agent flow.
type launcherStep int

const (
	stepTool launcherStep = iota // choosing which Tool to launch
	stepCwd                      // entering the working directory
	stepName                     // naming the agent (so it is distinguishable)
)

// launcher is the modal new-agent flow: pick a Tool, choose a working
// directory, and name the agent.
type launcher struct {
	active bool
	step   launcherStep
	tools  []config.Tool
	sel    int
	cwd    string // editable working-directory buffer
	name   string // editable agent-name buffer
}

// open starts the launcher, pre-filling the working directory.
func (l *launcher) open(tools []config.Tool, defaultCwd string) {
	cwd := defaultCwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	*l = launcher{active: true, step: stepTool, tools: tools, cwd: cwd}
}

func (l *launcher) close() { l.active = false }

// handleLauncherKey processes keys while the launcher modal is open.
func (m *Model) handleLauncherKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.launcher.step {
	case stepTool:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.launcher.close()
		case "up", "k":
			if m.launcher.sel > 0 {
				m.launcher.sel--
			}
		case "down", "j":
			if m.launcher.sel < len(m.launcher.tools)-1 {
				m.launcher.sel++
			}
		case "enter":
			if len(m.launcher.tools) > 0 {
				m.launcher.step = stepCwd
			}
		}

	case stepCwd:
		switch msg.String() {
		case "esc":
			m.launcher.step = stepTool
		case "ctrl+c":
			m.launcher.close()
		case "enter":
			// Default the name to the project (cwd basename), falling back to
			// the tool name — a sensible distinguishing label.
			m.launcher.name = defaultAgentName(m.launcher.tools[m.launcher.sel], m.launcher.cwd)
			m.launcher.step = stepName
		case "backspace":
			m.launcher.cwd = trimLastRune(m.launcher.cwd)
		default:
			k := tea.Key(msg)
			if k.Text != "" && k.Mod == 0 {
				m.launcher.cwd += k.Text
			}
		}

	case stepName:
		switch msg.String() {
		case "esc":
			m.launcher.step = stepCwd
		case "ctrl+c":
			m.launcher.close()
		case "enter":
			m.spawnFromLauncher()
			m.launcher.close()
		case "backspace":
			m.launcher.name = trimLastRune(m.launcher.name)
		default:
			k := tea.Key(msg)
			if k.Text != "" && k.Mod == 0 {
				m.launcher.name += k.Text
			}
		}
	}
	return nil
}

// defaultAgentName proposes a distinguishing name: the cwd basename, or the
// tool name when there is no meaningful basename.
func defaultAgentName(t config.Tool, cwd string) string {
	if b := baseName(cwd); b != "" && b != "/" && b != "." {
		return b
	}
	return t.Name
}

func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// spawnFromLauncher creates an Agent from the selected Tool and entered cwd,
// sized to the stage, and focuses it.
func (m *Model) spawnFromLauncher() {
	if m.launcher.sel >= len(m.launcher.tools) {
		return
	}
	t := m.launcher.tools[m.launcher.sel]
	name := strings.TrimSpace(m.launcher.name)
	if name == "" {
		name = t.Name
	}
	argv, rid := toolSpawn(t, false, "") // fresh launch; assigns a resume id where supported
	_, err := m.mgr.Spawn(agent.SpawnSpec{
		Name:      name,
		Tool:      t.Name,
		Command:   argv,
		Cwd:       m.launcher.cwd,
		Env:       t.Env,
		Cols:      m.stageWidth(),
		Rows:      m.stageHeight(),
		HookStyle: t.HookStyle,
		ResumeID:  rid,
	})
	if err != nil {
		m.notice = "launch " + t.Name + " failed: " + err.Error()
		return
	}
	m.selected = len(m.mgr.List()) - 1
}

func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}
