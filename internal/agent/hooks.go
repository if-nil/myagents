package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// HookActivity is an Agent's activity as reported by the tool's own hooks
// (e.g. Claude Code hooks). It is more precise than the output-activity
// heuristic: it can tell "working" apart from "waiting for you".
type HookActivity string

const (
	HookUnknown HookActivity = ""        // no hook data (non-hook tool, or none yet)
	HookWorking HookActivity = "working" // mid-turn / running a tool
	HookWaiting HookActivity = "waiting" // needs the user (permission / notification)
	HookIdle    HookActivity = "idle"    // turn finished, ready for input
)

// hookEventActivity maps a Claude Code hook event name to an activity. Events
// not listed are ignored. See https://code.claude.com/docs/en/hooks.
var hookEventActivity = map[string]HookActivity{
	"UserPromptSubmit":  HookWorking,
	"PreToolUse":        HookWorking,
	"PostToolUse":       HookWorking,
	"Notification":      HookWaiting,
	"PermissionRequest": HookWaiting,
	"Elicitation":       HookWaiting,
	"Stop":              HookIdle,
	"StopFailure":       HookIdle,
}

// hookEvents is the set of events we register in the per-session settings.
var hookEvents = []string{
	"UserPromptSubmit", "PreToolUse", "PostToolUse",
	"Notification", "PermissionRequest", "Elicitation",
	"Stop", "StopFailure",
}

// injectClaudeHooks returns argv with Claude Code's hooks wired to report back
// to this process via a per-session --settings, without touching any of the
// user's settings files. If argv already contains a --settings (a file path or
// inline JSON), it is loaded and our hooks are MERGED into it, so a single
// merged --settings is passed (no conflict). See ADR 0004.
func injectClaudeHooks(self, agentID, addr string, argv []string) ([]string, error) {
	val, rest, found := extractSettings(argv)

	base := map[string]any{}
	if found {
		loaded, err := loadSettings(val)
		if err != nil {
			return nil, fmt.Errorf("merge --settings %q: %w", val, err)
		}
		base = loaded
	}
	mergeOurHooks(base, self, agentID, addr)

	data, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	return append(rest, "--settings", string(data)), nil
}

// extractSettings pulls a --settings value out of argv (supporting both
// "--settings v" and "--settings=v"), returning the value and the remaining
// args. found is false when there is no --settings.
func extractSettings(argv []string) (val string, rest []string, found bool) {
	rest = make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--settings" && i+1 < len(argv):
			val, found = argv[i+1], true
			i++ // skip the value
		case strings.HasPrefix(a, "--settings="):
			val, found = strings.TrimPrefix(a, "--settings="), true
		default:
			rest = append(rest, a)
		}
	}
	return val, rest, found
}

// loadSettings parses a --settings value, which is either inline JSON (starts
// with '{') or a path to a JSON file.
func loadSettings(val string) (map[string]any, error) {
	data := []byte(val)
	if t := strings.TrimSpace(val); !strings.HasPrefix(t, "{") {
		b, err := os.ReadFile(expandPath(val))
		if err != nil {
			return nil, err
		}
		data = b
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// mergeOurHooks appends our reporting hook to base["hooks"] for each event,
// preserving any hooks the user already defined.
func mergeOurHooks(base map[string]any, self, agentID, addr string) {
	hooks, _ := base["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, ev := range hookEvents {
		entry := map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": self,
				"args":    []any{"hook", "--agent", agentID, "--addr", addr, "--event", ev},
			}},
		}
		existing, _ := hooks[ev].([]any)
		hooks[ev] = append(existing, entry)
	}
	base["hooks"] = hooks
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}
