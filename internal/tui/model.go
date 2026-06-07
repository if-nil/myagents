// Package tui is the Bubble Tea v2 front-end: a two-column layout with a roster
// of Agents (left) and a stage rendering the selected Agent's terminal (right).
package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/if-nil/myagents/internal/agent"
	"github.com/if-nil/myagents/internal/config"
	"github.com/if-nil/myagents/internal/store"
)

// Mode is the input mode. In ManageMode keys drive the roster; in OperateMode
// every key (except the escape key) is forwarded to the focused Agent's PTY.
// See CONTEXT.md "管理模式"/"操作模式".
type Mode int

const (
	ManageMode Mode = iota
	OperateMode
)

// operateEscape is the single key that always returns from OperateMode to
// ManageMode. It is intercepted before forwarding, so it never reaches the
// Agent. See docs interview: Ctrl-G chosen for minimal collision.
const operateEscape = "ctrl+g"

// minRosterCols / minStageWidth bound the horizontal split. The management
// column width is a fraction of the frame (roster_ratio); below the minimum
// usable stage width the layout flips to vertical instead.
const (
	minRosterCols = 22
	minStageWidth = 30
)

// rosterCols is the management column width in the horizontal layout: a
// fraction of the total width (roster_ratio), kept readable and leaving room
// for the stage.
func (m *Model) rosterCols() int {
	rw := int(float64(m.width) * m.cfg.RosterFraction())
	if rw < minRosterCols {
		rw = minRosterCols
	}
	if maxw := m.width - minStageWidth - 1; rw > maxw {
		rw = maxw
	}
	return clampMin(rw, 1)
}

// tickFPS is the idle refresh rate: fast enough to surface busy/idle and exit
// transitions, slow enough to stay cheap when nothing is happening. Output-
// driven redraws come from changesMsg, not the tick.
const tickFPS = 10

// frameInterval caps how often the (relatively expensive) frame content is
// rebuilt from the emulator, bounding CPU during output floods. View calls in
// between reuse the cached frame.
const frameInterval = time.Second / 30

// changesMsg signals that an Agent's screen or status changed.
type changesMsg struct{}

// tickMsg drives periodic redraws (so busy/idle and exit transitions surface
// even without new output) and coalesces bursts of output into frames.
type tickMsg struct{}

// listenForChanges blocks on the manager's coalesced notify channel and emits a
// changesMsg. It is re-issued each time it fires (the Bubble Tea channel idiom).
func listenForChanges(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return changesMsg{}
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second/tickFPS, func(time.Time) tea.Msg { return tickMsg{} })
}

// Model is the root Bubble Tea model.
type Model struct {
	mgr      agent.AgentManager
	cfg      *config.Config
	mode     Mode
	width    int
	height   int
	selected int // index into mgr.List()
	scroll         int // scrollback offset for the stage; 0 = live bottom
	launcher       launcher
	saved          []store.SavedSession // resumable sessions from previous runs
	confirmingQuit bool                 // showing the quit confirmation modal
	settings       bool                 // showing the settings modal
	settingsSel    int                  // selected settings row
	renaming       bool                 // inline-editing the selected agent's name
	renameBuf      string               // rename input buffer
	vertical       bool   // true = roster on top / stage below (tall or narrow)
	notice         string // transient message (e.g. a spawn error) shown in the footer

	frame      string    // cached rendered frame content
	lastRender time.Time // when frame was last rebuilt (for frameInterval throttle)
}

// New returns a Model driving the given manager with the given config and any
// saved (resumable) sessions from previous runs.
func New(mgr agent.AgentManager, cfg *config.Config, saved []store.SavedSession) *Model {
	return &Model{mgr: mgr, cfg: cfg, saved: saved}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(listenForChanges(m.mgr.Notify()), tick())
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.vertical = m.computeVertical()
		m.resizeCurrent()
		return m, nil

	case tea.KeyPressMsg:
		if m.confirmingQuit {
			return m, m.handleQuitConfirm(msg)
		}
		if m.settings {
			return m, m.handleSettingsKey(msg)
		}
		if m.renaming {
			return m, m.handleRenameKey(msg)
		}
		if m.launcher.active {
			return m, m.handleLauncherKey(msg)
		}
		if m.mode == OperateMode {
			return m, m.handleOperateKey(msg)
		}
		return m, m.handleManageKey(msg)

	case tea.PasteMsg:
		if m.mode == OperateMode {
			if a := m.current(); a != nil {
				a.Paste(msg.Content)
			}
		}
		return m, nil

	case tea.MouseClickMsg:
		m.handleMouse(mouseClick, msg.Mouse())
		return m, nil
	case tea.MouseReleaseMsg:
		m.handleMouse(mouseRelease, msg.Mouse())
		return m, nil
	case tea.MouseMotionMsg:
		m.handleMouse(mouseMotion, msg.Mouse())
		return m, nil
	case tea.MouseWheelMsg:
		m.handleMouse(mouseWheel, msg.Mouse())
		return m, nil

	case changesMsg:
		return m, listenForChanges(m.mgr.Notify())

	case tickMsg:
		// The Agent you are watching is always "read"; the unread badge then
		// only persists on Agents you are not looking at.
		if a := m.current(); a != nil {
			a.MarkRead()
		}
		return m, tick()
	}
	return m, nil
}

// handleManageKey processes keys while in ManageMode (roster navigation).
func (m *Model) handleManageKey(msg tea.KeyPressMsg) tea.Cmd {
	m.notice = "" // any manage action clears a transient notice
	switch msg.String() {
	case "ctrl+c", "q":
		m.confirmingQuit = true // confirm before terminating all agents
		return nil
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "enter", "right", "l":
		m.activate() // operate a live agent, or resume a saved session
	case "n":
		m.launcher.open(m.cfg.Tools, m.cfg.DefaultCwd)
	case "s":
		m.openSettings()
	case "x":
		if a := m.current(); a != nil {
			_ = m.mgr.Kill(a.ID)
		}
	case "d":
		m.removeCurrent()
	case "r":
		if e, ok := m.currentEntry(); ok {
			m.renaming = true
			m.renameBuf = entryName(e)
		}
	case "pgup":
		m.scrollBy(m.stageHeight() - 1)
	case "pgdown":
		m.scrollBy(-(m.stageHeight() - 1))
	case "home":
		m.scrollBy(1 << 30) // clamped to top of scrollback
	case "end":
		m.scroll = 0
	}
	return nil
}

// scrollBy moves the stage's scrollback offset up (positive) or down, clamped
// to the focused Agent's available history.
func (m *Model) scrollBy(delta int) {
	a := m.current()
	if a == nil {
		return
	}
	m.scroll += delta
	if m.scroll < 0 {
		m.scroll = 0
	}
	if max := a.ScrollbackLen(); m.scroll > max {
		m.scroll = max
	}
}

// handleQuitConfirm processes keys while the quit confirmation is showing.
// Only y/Enter quits; anything else cancels.
func (m *Model) handleQuitConfirm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y", "enter":
		m.persist() // remember the session list for next time
		return tea.Quit
	default:
		m.confirmingQuit = false
		return nil
	}
}

// handleRenameKey processes keys while inline-renaming the selected agent.
func (m *Model) handleRenameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.renaming = false
	case "enter":
		m.applyRename(strings.TrimSpace(m.renameBuf))
		m.renaming = false
	case "backspace":
		m.renameBuf = trimLastRune(m.renameBuf)
	default:
		k := tea.Key(msg)
		if k.Text != "" && k.Mod == 0 {
			m.renameBuf += k.Text
		}
	}
	return nil
}

// applyRename renames the selected entry (live agent or saved session).
func (m *Model) applyRename(name string) {
	if name == "" {
		return
	}
	e, ok := m.currentEntry()
	if !ok {
		return
	}
	if e.isAgent() {
		e.agent.Rename(name)
		return
	}
	e.saved.Name = name
	m.persist()
}

// removeCurrent drops the selected entry: a saved session is deleted from disk;
// a live agent is removed (the manager refuses a running one — kill it first).
func (m *Model) removeCurrent() {
	e, ok := m.currentEntry()
	if !ok {
		return
	}
	if !e.isAgent() {
		m.removeSaved(*e.saved)
		if m.selected > 0 {
			m.selected--
		}
		return
	}
	if err := m.mgr.Remove(e.agent.ID); err == nil && m.selected > 0 {
		m.selected--
	}
}

// handleOperateKey processes keys while in OperateMode: the escape key returns
// to ManageMode; everything else is forwarded to the focused Agent.
func (m *Model) handleOperateKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == operateEscape {
		m.mode = ManageMode
		return nil
	}
	a := m.current()
	if a == nil {
		return nil
	}
	m.scroll = 0 // any input returns to the live screen
	k := tea.Key(msg)
	// Printable text with no control modifiers goes through as-is; SendKey's
	// Code-only encoding would drop shifted/uppercase characters.
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta) == 0 {
		a.SendText(k.Text)
		return nil
	}
	a.SendKey(uv.KeyPressEvent(uv.Key(k)))
	return nil
}

// enterOperate focuses the selected Agent and switches to OperateMode.
func (m *Model) enterOperate() {
	a := m.current()
	if a == nil {
		return
	}
	a.MarkRead()
	m.mode = OperateMode
	m.scroll = 0 // operate always works on the live screen
	m.resizeCurrent()
}

// move changes the selected roster entry by delta, clamped, and resizes the new
// one if it is a live agent.
func (m *Model) move(delta int) {
	n := m.entryCount()
	if n == 0 {
		return
	}
	m.selected += delta
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= n {
		m.selected = n - 1
	}
	m.scroll = 0 // reset scrollback when switching
	m.resizeCurrent()
}

// resizeCurrent sizes the selected Agent's PTY/emulator to the stage area.
func (m *Model) resizeCurrent() {
	a := m.current()
	if a == nil || m.width == 0 {
		return
	}
	a.Resize(m.stageWidth(), m.stageHeight())
}

// cellAspect is the approximate height:width ratio of a terminal cell. Cells are
// roughly twice as tall as they are wide, so a physically portrait screen has
// width(cols) < height(rows) * cellAspect even though cols usually exceeds rows.
const cellAspect = 2

// computeVertical decides the split orientation from config and dimensions.
func (m *Model) computeVertical() bool {
	switch m.cfg.Layout {
	case "vertical":
		return true
	case "horizontal":
		return false
	default: // auto: vertical when physically portrait, or too narrow for a side stage
		return m.width < m.height*cellAspect || m.width < minRosterCols+1+minStageWidth
	}
}

func (m *Model) stageWidth() int  { return m.layout().stage.w }
func (m *Model) stageHeight() int { return m.layout().stage.h }

// stageOrigin is the top-left frame coordinate of the stage content area.
func (m *Model) stageOrigin() (x, y int) {
	s := m.layout().stage
	return s.x, s.y
}

func clampMin(v, lo int) int {
	if v < lo {
		return lo
	}
	return v
}
