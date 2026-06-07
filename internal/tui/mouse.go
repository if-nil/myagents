package tui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// mouseKind distinguishes the mouse event variants we route.
type mouseKind int

const (
	mouseClick mouseKind = iota
	mouseRelease
	mouseWheel
	mouseMotion
)

// wheelLines is how many lines one wheel event scrolls. Kept small because a
// single physical scroll (especially a trackpad swipe) emits several events.
const wheelLines = 1

// inRoster reports whether a frame coordinate is in the agents panel.
func (m *Model) inRoster(x, y int) bool {
	return m.layout().agents.contains(x, y)
}

// rosterIndexAt maps a coordinate in the agents panel to an agent index (one
// line per agent, windowed to the selection).
func (m *Model) rosterIndexAt(x, y int) (int, bool) {
	a := m.layout().agents
	row := y - a.y
	if row < 0 || row >= a.h {
		return 0, false
	}
	idx := m.rosterWindowStart(a.h) + row
	if idx >= 0 && idx < m.entryCount() {
		return idx, true
	}
	return 0, false
}

// inStage reports whether a frame coordinate is inside the stage content area.
func (m *Model) inStage(x, y int) bool {
	return m.layout().stage.contains(x, y)
}

// handleMouse routes a mouse event. In operate mode every event is forwarded to
// the focused agent; in manage mode clicks select/focus and the wheel scrolls.
func (m *Model) handleMouse(kind mouseKind, mo tea.Mouse) {
	if m.launcher.active || m.confirmingQuit || m.renaming || m.settings {
		return
	}

	// A left-click anywhere in the roster returns to manage mode — even from
	// operate mode and even on empty space (no Ctrl-G needed). If it lands on
	// an agent, select it too.
	if kind == mouseClick && mo.Button == tea.MouseLeft && m.inRoster(mo.X, mo.Y) {
		m.mode = ManageMode
		if idx, ok := m.rosterIndexAt(mo.X, mo.Y); ok {
			m.selected = idx
			m.scroll = 0
		}
		m.resizeCurrent()
		return
	}

	if m.mode == OperateMode {
		// For a child that doesn't read the mouse (no alt screen, no mouse
		// reporting — e.g. codex), the wheel scrolls our scrollback, exactly as
		// a real terminal scrolls its buffer for such programs.
		if kind == mouseWheel {
			if a := m.current(); a != nil && !a.MouseEnabled() {
				switch mo.Button {
				case tea.MouseWheelUp:
					m.scrollBy(wheelLines)
				case tea.MouseWheelDown:
					m.scrollBy(-wheelLines)
				}
				return
			}
		}
		m.forwardMouse(kind, mo)
		return
	}

	switch kind {
	case mouseWheel:
		switch mo.Button {
		case tea.MouseWheelUp:
			m.scrollBy(wheelLines)
		case tea.MouseWheelDown:
			m.scrollBy(-wheelLines)
		}
	case mouseClick:
		// Click in the stage focuses the agent; forward the click so it lands
		// where you clicked inside the child UI.
		if mo.Button == tea.MouseLeft && m.inStage(mo.X, mo.Y) {
			m.enterOperate()
			m.forwardMouse(kind, mo)
		}
	}
}

// forwardMouse translates a mouse event into stage-local coordinates and sends
// it to the focused agent. Out-of-stage events are dropped.
func (m *Model) forwardMouse(kind mouseKind, mo tea.Mouse) {
	a := m.current()
	if a == nil {
		return
	}
	ox, oy := m.stageOrigin()
	x, y := mo.X-ox, mo.Y-oy
	if x < 0 || x >= m.stageWidth() || y < 0 || y >= m.stageHeight() {
		return
	}
	um := uv.Mouse(mo)
	um.X, um.Y = x, y

	var ev uv.MouseEvent
	switch kind {
	case mouseClick:
		ev = uv.MouseClickEvent(um)
	case mouseRelease:
		ev = uv.MouseReleaseEvent(um)
	case mouseWheel:
		ev = uv.MouseWheelEvent(um)
	case mouseMotion:
		ev = uv.MouseMotionEvent(um)
	}
	if ev != nil {
		a.SendMouse(ev)
	}
}
