//go:build unix

package agent

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestInjectClaudeHooks(t *testing.T) {
	argv, err := injectClaudeHooks("/opt/myagents", "a7", "127.0.0.1:6000", []string{"claude"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if len(argv) != 3 || argv[0] != "claude" || argv[1] != "--settings" {
		t.Fatalf("argv = %v, want [claude --settings <json>]", argv)
	}
	if !json.Valid([]byte(argv[2])) {
		t.Fatalf("settings is not valid JSON: %s", argv[2])
	}
	for _, want := range []string{"Notification", "Stop", "PreToolUse", "/opt/myagents", "a7", "127.0.0.1:6000", "hook"} {
		if !strings.Contains(argv[2], want) {
			t.Errorf("settings JSON missing %q:\n%s", want, argv[2])
		}
	}
}

func TestInjectClaudeHooksMergesUserSettings(t *testing.T) {
	// User supplies their own inline --settings with a hook and an unrelated key.
	user := `{"model":"opus","hooks":{"Notification":[{"hooks":[{"type":"command","command":"/usr/bin/mine"}]}]}}`
	argv, err := injectClaudeHooks("/opt/myagents", "a1", "127.0.0.1:7000",
		[]string{"claude", "--settings", user, "--verbose"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	// Exactly one --settings remains, and unrelated args are preserved.
	got := strings.Join(argv, " ")
	if strings.Count(got, "--settings") != 1 {
		t.Fatalf("want a single --settings, got: %v", argv)
	}
	if !contains(argv, "--verbose") || !contains(argv, "claude") {
		t.Errorf("lost original args: %v", argv)
	}

	// The merged JSON must keep the user's key+hook AND add ours.
	var merged map[string]any
	for i, a := range argv {
		if a == "--settings" {
			if err := json.Unmarshal([]byte(argv[i+1]), &merged); err != nil {
				t.Fatalf("merged not JSON: %v", err)
			}
		}
	}
	if merged["model"] != "opus" {
		t.Errorf("user key 'model' lost: %v", merged["model"])
	}
	hooks := merged["hooks"].(map[string]any)
	notif := hooks["Notification"].([]any)
	if len(notif) != 2 { // user's + ours
		t.Errorf("Notification should have user+ours = 2 entries, got %d", len(notif))
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Errorf("our Stop hook missing from merge")
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func sendReport(t *testing.T, addr, id, event string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(map[string]string{"agent": id, "event": event}); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func TestHookTracksSessionID(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()
	if m.hookAddr == "" {
		t.Skip("hook listener unavailable")
	}
	a, err := m.Spawn(SpawnSpec{Command: []string{"/bin/cat"}, ResumeID: "orig"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.Snapshot().Status == StatusRunning })
	if a.Snapshot().ResumeID != "orig" {
		t.Fatalf("initial ResumeID = %q, want orig", a.Snapshot().ResumeID)
	}

	// A hook reporting a new session id (as /resume would) updates resume.
	conn, err := net.Dial("tcp", m.hookAddr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = json.NewEncoder(conn).Encode(map[string]string{"agent": a.ID, "event": "Stop", "session": "switched-123"})
	conn.Close()

	waitFor(t, 2*time.Second, func() bool { return a.Snapshot().ResumeID == "switched-123" })
}

func TestHookReportsDriveStatus(t *testing.T) {
	m := NewInProcessManager()
	defer m.Close()
	if m.hookAddr == "" {
		t.Skip("hook listener unavailable")
	}

	a, err := m.Spawn(SpawnSpec{Command: []string{"/bin/cat"}}) // stays running
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.Snapshot().Status == StatusRunning })

	sendReport(t, m.hookAddr, a.ID, "Notification")
	waitFor(t, 2*time.Second, func() bool { return a.Snapshot().Waiting })

	sendReport(t, m.hookAddr, a.ID, "UserPromptSubmit")
	waitFor(t, 2*time.Second, func() bool {
		s := a.Snapshot()
		return s.Working && !s.Waiting
	})

	sendReport(t, m.hookAddr, a.ID, "Stop")
	waitFor(t, 2*time.Second, func() bool {
		s := a.Snapshot()
		return !s.Working && !s.Waiting
	})
}
