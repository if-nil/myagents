package tui

import (
	"crypto/rand"
	"fmt"

	"github.com/if-nil/myagents/internal/agent"
	"github.com/if-nil/myagents/internal/config"
	"github.com/if-nil/myagents/internal/store"
)

// toolSpawn builds the argv and resume id for launching a tool. When resume is
// false it is a fresh launch (claude gets a fresh --session-id so it can be
// resumed precisely later). When resume is true it continues a prior session:
// claude with a known id resumes that exact session (--resume id), otherwise it
// falls back to the tool's ResumeFlags (claude --continue / codex resume --last).
func toolSpawn(t config.Tool, resume bool, resumeID string) (argv []string, rid string) {
	argv = append([]string(nil), t.Command...)
	claude := len(t.Command) > 0 && baseName(t.Command[0]) == "claude"
	switch {
	case resume && claude && resumeID != "":
		argv = append(argv, "--resume", resumeID)
		rid = resumeID
	case resume:
		argv = append(argv, t.ResumeFlags()...)
	case claude:
		rid = newUUID()
		argv = append(argv, "--session-id", rid)
	}
	return argv, rid
}

// newUUID returns a random RFC-4122 v4 UUID string.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// entry is a roster row: either a live Agent or a saved (resumable) session.
// Live agents are listed first, saved sessions after.
type entry struct {
	agent *agent.Agent
	saved *store.SavedSession
}

func (e entry) isAgent() bool { return e.agent != nil }

// entryName returns the display name of a roster entry.
func entryName(e entry) string {
	if e.isAgent() {
		return e.agent.Snapshot().Name
	}
	return e.saved.Name
}

// entries returns the merged roster: live agents followed by saved sessions.
func (m *Model) entries() []entry {
	live := m.mgr.List()
	es := make([]entry, 0, len(live)+len(m.saved))
	for _, a := range live {
		es = append(es, entry{agent: a})
	}
	for i := range m.saved {
		es = append(es, entry{saved: &m.saved[i]})
	}
	return es
}

func (m *Model) entryCount() int { return len(m.mgr.List()) + len(m.saved) }

// currentEntry returns the selected roster entry, clamping the selection.
func (m *Model) currentEntry() (entry, bool) {
	es := m.entries()
	if len(es) == 0 {
		return entry{}, false
	}
	if m.selected >= len(es) {
		m.selected = len(es) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	return es[m.selected], true
}

// current returns the selected Agent, or nil when the selection is a saved
// session (or the roster is empty).
func (m *Model) current() *agent.Agent {
	if e, ok := m.currentEntry(); ok {
		return e.agent
	}
	return nil
}

// activate handles Enter/→ on the selection: operate a live agent, or resume a
// saved session.
func (m *Model) activate() {
	e, ok := m.currentEntry()
	if !ok {
		return
	}
	if e.isAgent() {
		m.enterOperate()
		return
	}
	m.resume(*e.saved)
}

// resume relaunches a saved session from its tool config (with resume flags so
// the conversation continues) and focuses it.
func (m *Model) resume(ss store.SavedSession) {
	var tool *config.Tool
	for i := range m.cfg.Tools {
		if m.cfg.Tools[i].Name == ss.Tool {
			tool = &m.cfg.Tools[i]
			break
		}
	}
	if tool == nil {
		m.notice = "resume failed: tool '" + ss.Tool + "' not in config"
		return
	}
	argv, rid := toolSpawn(*tool, true, ss.ResumeID)
	_, err := m.mgr.Spawn(agent.SpawnSpec{
		Name:      ss.Name,
		Tool:      tool.Name,
		Command:   argv,
		Cwd:       ss.Cwd,
		Env:       tool.Env,
		Cols:      m.stageWidth(),
		Rows:      m.stageHeight(),
		HookStyle: tool.HookStyle,
		ResumeID:  rid,
	})
	if err != nil {
		m.notice = "resume failed: " + err.Error()
		return
	}
	m.removeSaved(ss)
	m.selected = len(m.mgr.List()) - 1
	m.enterOperate()
}

// removeSaved drops a saved session (matched by name+tool+cwd) and persists.
func (m *Model) removeSaved(ss store.SavedSession) {
	for i, s := range m.saved {
		if s == ss {
			m.saved = append(m.saved[:i], m.saved[i+1:]...)
			break
		}
	}
	m.persist()
}

// persist writes the current sessions (live agents + remaining saved) to disk.
func (m *Model) persist() {
	var out []store.SavedSession
	for _, a := range m.mgr.List() {
		s := a.Snapshot()
		out = append(out, store.SavedSession{Name: s.Name, Tool: s.Tool, Cwd: s.Cwd, ResumeID: s.ResumeID})
	}
	out = append(out, m.saved...)
	_ = store.Save(out)
}
