package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/if-nil/myagents/internal/agent"
)

// Palette.
var (
	cAccent  = lipgloss.Color("39")  // blue: focus, titles, selection
	cWaiting = lipgloss.Color("214") // orange: needs you
	cWorking = lipgloss.Color("42")  // green: working
	cIdle    = lipgloss.Color("245") // grey: idle
	cFailed  = lipgloss.Color("203") // red: failed
	cDim     = lipgloss.Color("240")
	cText    = lipgloss.Color("252")
)

var (
	panelBorder       = lipgloss.NewStyle().Foreground(cDim)
	panelBorderActive = lipgloss.NewStyle().Foreground(cAccent)
	panelTitle        = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	panelTitleActive  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)

	headerName = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	selBar     = lipgloss.NewStyle().Foreground(cAccent)
	rowSel     = lipgloss.NewStyle().Foreground(cText).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(cDim)
	labelStyle = lipgloss.NewStyle().Foreground(cDim)
	waitStyle   = lipgloss.NewStyle().Foreground(cWaiting).Bold(true)
	unreadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("45")) // cyan: new output

	manageBadgeStyle  = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("0")).Background(cAccent)
	operateBadgeStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("0")).Background(cWorking)
	footerStyle       = lipgloss.NewStyle().Foreground(cDim)
	scrollBadgeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(cWaiting)
	errorStyle        = lipgloss.NewStyle().Foreground(cFailed).Bold(true)

	boxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(cAccent).Padding(1, 2).Width(48)
	boxTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	inputStyle    = lipgloss.NewStyle().Foreground(cText)
	selItemStyle  = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
)

// View implements tea.Model.
func (m *Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if m.width == 0 || m.height == 0 {
		v.SetContent("starting…")
		return v
	}

	if !m.launcher.active && !m.confirmingQuit && !m.settings {
		v.MouseMode = tea.MouseModeCellMotion
	}

	// Rebuild the frame at most once per frameInterval; reuse the cache between
	// so output floods cannot drive unbounded re-renders.
	if m.frame == "" || time.Since(m.lastRender) >= frameInterval {
		m.frame = m.buildFrame()
		m.lastRender = time.Now()
	}
	v.SetContent(m.frame)

	// Cursor (cheap, live every call): show the child's cursor in the stage
	// while operating, mapped to absolute frame coordinates.
	if m.mode == OperateMode && !m.launcher.active && !m.confirmingQuit && !m.settings {
		if a := m.current(); a != nil {
			pos := a.CursorPosition()
			ox, oy := m.stageOrigin()
			v.Cursor = tea.NewCursor(ox+pos.X, oy+pos.Y)
		}
	}
	return v
}

// buildFrame renders the full frame: header, panels, footer (or a modal).
func (m *Model) buildFrame() string {
	if m.confirmingQuit {
		return m.renderQuitConfirm()
	}
	if m.settings {
		return m.renderSettings()
	}
	if m.launcher.active {
		return m.renderLauncher()
	}

	lay := m.layout()
	manageFocus := m.mode == ManageMode
	stage := renderPanel(m.stageTitle(), m.renderStageContent(lay.stage), lay.stage, !manageFocus)

	var body string
	if m.vertical {
		agents := renderPanel(m.agentsTitle(), m.renderAgentsContent(lay), lay.agents, manageFocus)
		details := renderPanel("Details", m.renderDetails(lay.details), lay.details, false)
		body = lipgloss.JoinVertical(lipgloss.Left, agents, details, stage)
	} else {
		agents := renderPanel(m.agentsTitle(), m.renderAgentsContent(lay), lay.agents, manageFocus)
		details := renderPanel("Details", m.renderDetails(lay.details), lay.details, false)
		left := lipgloss.JoinVertical(lipgloss.Left, agents, details)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, stage)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
}

// renderPanel draws a rounded box with the title embedded in the top edge. The
// content is clipped/padded to the rect's inner dimensions (ANSI-aware), so the
// borders always align regardless of content width.
func renderPanel(title, content string, r rect, active bool) string {
	border, ts := panelBorder, panelTitle
	if active {
		border, ts = panelBorderActive, panelTitleActive
	}

	label := ""
	if title != "" {
		label = " " + truncate(title, clampMin(r.w-2, 1)) + " "
	}
	fill := r.w - lipgloss.Width(label)
	if fill < 0 {
		fill = 0
	}

	var b strings.Builder
	b.WriteString(border.Render("╭") + ts.Render(label) + border.Render(strings.Repeat("─", fill)+"╮") + "\n")

	vbar := border.Render("│")
	lines := strings.Split(content, "\n")
	for i := 0; i < r.h; i++ {
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		if w := lipgloss.Width(line); w > r.w {
			line = ansi.Truncate(line, r.w, "")
		} else if w < r.w {
			line += strings.Repeat(" ", r.w-w)
		}
		b.WriteString(vbar + line + vbar + "\n")
	}
	b.WriteString(border.Render("╰" + strings.Repeat("─", r.w) + "╯"))
	return b.String()
}

// renderHeader draws the top summary bar: app name and agent counts.
func (m *Model) renderHeader() string {
	agents := m.mgr.List()
	waiting := 0
	for _, a := range agents {
		if a.Snapshot().Waiting {
			waiting++
		}
	}
	left := " " + headerName.Render("myagents")
	stat := dimStyle.Render(fmt.Sprintf("%d agents", len(agents)))
	if waiting > 0 {
		stat += dimStyle.Render(" · ") + waitStyle.Render(fmt.Sprintf("%d waiting", waiting))
	}
	if len(m.saved) > 0 {
		stat += dimStyle.Render(fmt.Sprintf(" · %d saved", len(m.saved)))
	}
	right := stat + " "
	gap := clampMin(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return fitLine(left+strings.Repeat(" ", gap)+right, m.width)
}

func (m *Model) agentsTitle() string {
	return fmt.Sprintf("Sessions (%d)", m.entryCount())
}

// renderAgentsContent draws one line per roster entry, windowed to the selection.
func (m *Model) renderAgentsContent(lay frameLayout) string {
	es := m.entries()
	if len(es) == 0 {
		return dimStyle.Render("(empty)") + "\n" + dimStyle.Render("press n to launch")
	}
	rows := m.rosterListRows()
	start := m.rosterWindowStart(rows)

	var lines []string
	for i := start; i < start+rows && i < len(es); i++ {
		lines = append(lines, m.renderEntryRow(es[i], i == m.selected, lay.agents.w))
	}
	return strings.Join(lines, "\n")
}

// renderEntryRow renders a live agent or a saved (resumable) session.
func (m *Model) renderEntryRow(e entry, selected bool, w int) string {
	if e.isAgent() {
		return m.renderAgentRow(e.agent.Snapshot(), selected, w)
	}
	ss := e.saved
	bar, name := " ", dimStyle.Render(ss.Name)
	switch {
	case selected && m.renaming:
		bar, name = selBar.Render("▎"), inputStyle.Render(m.renameBuf)+"▏"
	case selected:
		bar, name = selBar.Render("▎"), rowSel.Render(ss.Name)
	}
	line := bar + dimStyle.Render("◌") + " " + m.toolTag(ss.Tool) + " " + name
	if dir := baseName(ss.Cwd); dir != "" {
		line += "  " + dimStyle.Render(dir)
	}
	return truncate(line, w)
}

// renderAgentRow formats a single roster line. The unread dot is pinned to the
// right edge (so it never shifts the name/dir) and is hidden for the selected
// agent, which you are already watching.
func (m *Model) renderAgentRow(s agent.Snapshot, selected bool, w int) string {
	bar := " "
	name := s.Name
	switch {
	case selected && m.renaming:
		bar = selBar.Render("▎")
		name = inputStyle.Render(m.renameBuf) + "▏" // inline edit with a cursor
	case selected:
		bar = selBar.Render("▎")
		name = rowSel.Render(name)
	case s.Waiting:
		name = waitStyle.Render(name)
	}
	line := bar + coloredGlyph(s) + " " + m.toolTag(s.Tool) + " " + name
	if dir := baseName(s.Cwd); dir != "" {
		line += "  " + dimStyle.Render(dir)
	}
	if s.Unread && !selected {
		return fitLine(line, w-2) + " " + unreadStyle.Render("●")
	}
	return truncate(line, w)
}

// renderDetails draws the selected entry's detail fields.
func (m *Model) renderDetails(r rect) string {
	e, ok := m.currentEntry()
	if !ok {
		return dimStyle.Render("no session selected")
	}
	var rows [][2]string
	if e.isAgent() {
		s := e.agent.Snapshot()
		rows = [][2]string{
			{"Tool", orDash(s.Tool)},
			{"Dir", abbrevHome(orDash(s.Cwd))},
			{"Status", statusText(s)},
			{"Uptime", fmtDuration(s.Uptime)},
		}
		if s.LastEvent != "" {
			rows = append(rows, [2]string{"Last", s.LastEvent})
		}
	} else {
		rows = [][2]string{
			{"Tool", orDash(e.saved.Tool)},
			{"Dir", abbrevHome(orDash(e.saved.Cwd))},
			{"Status", "saved"},
			{"", "⏎ to resume"},
		}
	}
	var lines []string
	for _, kv := range rows {
		lines = append(lines, labelStyle.Render(pad(kv[0], 7))+truncate(kv[1], clampMin(r.w-7, 1)))
	}
	return strings.Join(lines, "\n")
}

// stageTitle is the stage panel title: the focused entry and its status.
func (m *Model) stageTitle() string {
	e, ok := m.currentEntry()
	if !ok {
		return "Stage"
	}
	if !e.isAgent() {
		return e.saved.Name + " · " + orDash(e.saved.Tool) + " · saved"
	}
	s := e.agent.Snapshot()
	t := s.Name
	if s.Tool != "" && s.Tool != s.Name {
		t += " · " + s.Tool
	}
	return t + " · " + statusText(s)
}

// renderStageContent draws the selected agent's terminal, or a hint for a saved
// session / empty roster.
func (m *Model) renderStageContent(r rect) string {
	e, ok := m.currentEntry()
	if !ok || !e.isAgent() {
		msg := "no session selected\npress n to launch one"
		if ok {
			msg = "saved session\npress enter to resume"
		}
		return lipgloss.NewStyle().Width(r.w).Height(r.h).Align(lipgloss.Center, lipgloss.Center).
			Render(dimStyle.Render(msg))
	}
	if m.scroll > 0 {
		return e.agent.RenderView(m.scroll, r.h)
	}
	return e.agent.Render()
}

// renderFooter draws the bottom status bar: mode badge, hints, scroll badge.
func (m *Model) renderFooter() string {
	var badge, hints string
	switch m.mode {
	case OperateMode:
		badge = operateBadgeStyle.Render("OPERATE")
		hints = footerStyle.Render(" ctrl+g back · keys → agent")
	default:
		badge = manageBadgeStyle.Render("MANAGE")
		if m.renaming {
			hints = footerStyle.Render(" rename: type · ⏎ save · esc cancel")
		} else {
			hints = footerStyle.Render(" ↑↓ select · ⏎ operate · n new · r rename · x kill · d remove · s settings · q quit")
		}
	}
	if m.notice != "" {
		hints = errorStyle.Render(" " + m.notice)
	}
	left := badge + hints
	if m.scroll > 0 {
		left += scrollBadgeStyle.Render(fmt.Sprintf(" ▲ %d ", m.scroll))
	}
	return fitLine(left, m.width)
}

// renderSettings draws the centered settings editor.
func (m *Model) renderSettings() string {
	var b strings.Builder
	b.WriteString(boxTitleStyle.Render("Settings"))
	b.WriteString("\n\n")
	for i, kv := range m.settingsRows() {
		cursor := "  "
		val := kv[1]
		if i == m.settingsSel {
			cursor = selItemStyle.Render("› ")
			val = selItemStyle.Render("‹ " + val + " ›")
		} else {
			val = inputStyle.Render(val)
		}
		b.WriteString(cursor + labelStyle.Render(pad(kv[0], 14)) + val + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ select · ←/→ change · esc save"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, boxStyle.Render(b.String()))
}

// renderQuitConfirm draws the centered quit confirmation modal.
func (m *Model) renderQuitConfirm() string {
	n := len(m.mgr.List())
	var b strings.Builder
	b.WriteString(boxTitleStyle.Render("Quit myagents?"))
	b.WriteString("\n\n")
	if n > 0 {
		b.WriteString(fmt.Sprintf("This terminates %d running agent(s).\n\n", n))
	} else {
		b.WriteString("No agents are running.\n\n")
	}
	b.WriteString(selItemStyle.Render("[y]") + " quit    " + dimStyle.Render("[n] cancel"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, boxStyle.Render(b.String()))
}

// renderLauncher draws the modal new-agent flow centered on the frame.
func (m *Model) renderLauncher() string {
	var b strings.Builder
	b.WriteString(boxTitleStyle.Render("New Agent"))
	b.WriteString("\n\n")

	switch m.launcher.step {
	case stepTool:
		b.WriteString("Select tool:\n\n")
		for i, t := range m.launcher.tools {
			label := t.Name + "  " + dimStyle.Render(strings.Join(t.Command, " "))
			if len(t.Command) > 0 {
				if _, err := exec.LookPath(t.Command[0]); err != nil {
					label += errorStyle.Render(" not found")
				}
			}
			if i == m.launcher.sel {
				b.WriteString(selItemStyle.Render("› " + label))
			} else {
				b.WriteString("  " + label)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n" + dimStyle.Render("↑/↓ select · enter next · esc cancel"))

	case stepCwd:
		b.WriteString(dimStyle.Render("Tool: ") + m.launcher.tools[m.launcher.sel].Name + "\n\n")
		b.WriteString("Working directory:\n")
		b.WriteString(inputStyle.Render(m.launcher.cwd) + "▏")
		b.WriteString("\n\n" + dimStyle.Render("enter next · esc back"))

	case stepName:
		b.WriteString(dimStyle.Render("Tool: ") + m.launcher.tools[m.launcher.sel].Name + "\n")
		b.WriteString(dimStyle.Render("Dir:  ") + m.launcher.cwd + "\n\n")
		b.WriteString("Name:\n")
		b.WriteString(inputStyle.Render(m.launcher.name) + "▏")
		b.WriteString("\n\n" + dimStyle.Render("enter launch · esc back"))
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, boxStyle.Render(b.String()))
}

// toolColors maps known tools to distinct colors; others are hashed onto a
// small palette so every tool gets a stable, distinguishable tag color.
var (
	toolColors = map[string]string{
		"claude": "173", // Anthropic coral / terracotta
		"codex":  "36",  // OpenAI teal / green
	}
	// toolLabels override the default first-two-letters tag for known tools.
	toolLabels = map[string]string{
		"claude": "cc",
		"codex":  "cx",
	}
	toolPalette = []string{"75", "215", "114", "180", "147", "209"}
)

// toolTag returns the roster badge for a tool: its configured icon if set,
// otherwise a tool-colored letter tag (CC / CX) — so agents are
// distinguishable at a glance.
func (m *Model) toolTag(tool string) string {
	for _, t := range m.cfg.Tools {
		if t.Name == tool && t.Icon != "" {
			return t.Icon
		}
	}
	return letterTag(tool)
}

// letterTag is the fallback: a short, tool-colored uppercase code (CC / CX).
func letterTag(tool string) string {
	if tool == "" {
		return dimStyle.Render("··")
	}
	c, ok := toolColors[tool]
	if !ok {
		h := 0
		for _, r := range tool {
			h = h*31 + int(r)
		}
		c = toolPalette[(h%len(toolPalette)+len(toolPalette))%len(toolPalette)]
	}
	label, ok := toolLabels[tool]
	if !ok {
		r := []rune(tool)
		n := 2
		if len(r) < 2 {
			n = len(r)
		}
		label = strings.ToLower(string(r[:n]))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render(label)
}

// coloredGlyph returns a status-colored glyph for the roster.
func coloredGlyph(s agent.Snapshot) string {
	switch s.Status {
	case agent.StatusRunning:
		switch {
		case s.Waiting:
			return lipgloss.NewStyle().Foreground(cWaiting).Render("!")
		case s.Working:
			return lipgloss.NewStyle().Foreground(cWorking).Render("▶")
		default:
			return lipgloss.NewStyle().Foreground(cIdle).Render("○")
		}
	case agent.StatusExited:
		return lipgloss.NewStyle().Foreground(cIdle).Render("✓")
	case agent.StatusFailed:
		return lipgloss.NewStyle().Foreground(cFailed).Render("✗")
	default:
		return dimStyle.Render("·")
	}
}

func statusText(s agent.Snapshot) string {
	switch s.Status {
	case agent.StatusRunning:
		switch {
		case s.Waiting:
			return "waiting for you"
		case s.Working:
			return "working"
		default:
			return "idle"
		}
	case agent.StatusExited:
		return fmt.Sprintf("exited (%d)", s.ExitCode)
	case agent.StatusFailed:
		return "failed"
	default:
		return "starting"
	}
}

// truncate clips s to max display cells (ANSI-aware), adding an ellipsis.
func truncate(s string, max int) string {
	if max < 1 {
		max = 1
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	return ansi.Truncate(s, max, "…")
}

// fitLine clips s to exactly w cells (truncating or right-padding), ANSI-aware.
func fitLine(s string, w int) string {
	if cur := lipgloss.Width(s); cur > w {
		return ansi.Truncate(s, w, "")
	} else if cur < w {
		return s + strings.Repeat(" ", w-cur)
	}
	return s
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func abbrevHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
