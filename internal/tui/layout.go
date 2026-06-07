package tui

const (
	headerHeight = 1 // top summary bar
	footerHeight = 1 // bottom status bar
	detailsLines = 6 // inner lines of the Details panel (horizontal layout)
)

// rect is an inner content area (the region inside a panel's border), in
// absolute frame coordinates.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// frameLayout holds the inner content rects of each panel for the current size
// and orientation. A panel's border occupies one cell outside each edge.
type frameLayout struct {
	agents  rect
	details rect
	stage   rect
}

// layout computes panel rectangles for the current dimensions and orientation.
func (m *Model) layout() frameLayout {
	bodyTop := headerHeight
	bodyH := clampMin(m.height-headerHeight-footerHeight, 3)

	if m.vertical {
		innerW := clampMin(m.width-2, 1)
		// The management area (roster_ratio of the body) splits into the agents
		// list on top and the details panel below; the stage fills the rest.
		topOuter := clampRange(int(float64(bodyH)*m.cfg.RosterFraction()), 6, bodyH-3)
		detailsOuter := clampRange(detailsLines+2, 3, topOuter-3)
		agentsOuter := topOuter - detailsOuter
		return frameLayout{
			agents:  rect{1, bodyTop + 1, innerW, clampMin(agentsOuter-2, 1)},
			details: rect{1, bodyTop + agentsOuter + 1, innerW, clampMin(detailsOuter-2, 1)},
			stage:   rect{1, bodyTop + topOuter + 1, innerW, clampMin(bodyH-topOuter-2, 1)},
		}
	}

	detailsOuter := clampRange(detailsLines+2, 3, bodyH-3)
	agentsOuter := bodyH - detailsOuter
	rw := m.rosterCols()
	innerW := clampMin(rw-2, 1)
	return frameLayout{
		agents:  rect{1, bodyTop + 1, innerW, clampMin(agentsOuter-2, 1)},
		details: rect{1, bodyTop + agentsOuter + 1, innerW, clampMin(detailsOuter-2, 1)},
		stage:   rect{rw + 1, bodyTop + 1, clampMin(m.width-rw-2, 1), clampMin(bodyH-2, 1)},
	}
}

// rosterListRows is the number of agent lines the agents panel can show.
func (m *Model) rosterListRows() int {
	return clampMin(m.layout().agents.h, 1)
}

// rosterWindowStart returns the index of the first agent shown in the agents
// panel, scrolled to keep the selected agent visible (one line per agent).
func (m *Model) rosterWindowStart(visible int) int {
	n := m.entryCount()
	if n <= visible || m.selected < visible {
		return 0
	}
	return m.selected - visible + 1
}

func clampRange(v, lo, hi int) int {
	if v < lo {
		v = lo
	}
	if hi >= lo && v > hi {
		v = hi
	}
	return v
}
