package store

import (
	"testing"

)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if got, err := Load(); err != nil || len(got) != 0 {
		t.Fatalf("empty load = %v, %v; want nil, nil", got, err)
	}

	want := []SavedSession{
		{Name: "frontend", Tool: "claude", Cwd: "/work/web"},
		{Name: "api", Tool: "codex", Cwd: "/work/api"},
	}
	if err := Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}
