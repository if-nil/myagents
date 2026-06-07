package tui

import (
	"testing"

	"github.com/if-nil/myagents/internal/config"
)

func TestToolSpawn(t *testing.T) {
	claude := config.Tool{Name: "claude", Command: []string{"claude"}, HookStyle: "claude"}
	codex := config.Tool{Name: "codex", Command: []string{"codex"}}

	// Fresh claude assigns a --session-id and returns its id.
	argv, rid := toolSpawn(claude, false, "")
	if rid == "" || !hasPair(argv, "--session-id", rid) {
		t.Fatalf("fresh claude argv=%v rid=%q; want --session-id <rid>", argv, rid)
	}

	// Resuming claude with a known id uses --resume <id> (precise).
	argv, rid = toolSpawn(claude, true, "abc-123")
	if rid != "abc-123" || !hasPair(argv, "--resume", "abc-123") {
		t.Fatalf("resume claude argv=%v rid=%q; want --resume abc-123", argv, rid)
	}

	// Resuming claude without an id falls back to --continue.
	argv, _ = toolSpawn(claude, true, "")
	if !contains(argv, "--continue") {
		t.Fatalf("resume claude w/o id argv=%v; want --continue", argv)
	}

	// Fresh codex is plain; resuming codex uses the subcommand.
	if argv, rid = toolSpawn(codex, false, ""); rid != "" || len(argv) != 1 {
		t.Fatalf("fresh codex argv=%v rid=%q; want [codex]", argv, rid)
	}
	if argv, _ = toolSpawn(codex, true, ""); !contains(argv, "resume") || !contains(argv, "--last") {
		t.Fatalf("resume codex argv=%v; want resume --last", argv)
	}
}

func hasPair(s []string, a, b string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == a && s[i+1] == b {
			return true
		}
	}
	return false
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
