package config

import (
	"os"
	"testing"

)

func TestLoadWritesDefaultOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, path, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Tools) != 2 || cfg.Tools[0].Name != "claude" {
		t.Errorf("default tools = %+v, want claude+codex", cfg.Tools)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config not written to %s: %v", path, err)
	}

	// Second load reads the file back rather than the in-memory default.
	cfg2, _, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg2.Tools) != 2 {
		t.Errorf("reloaded tools = %d, want 2", len(cfg2.Tools))
	}
}

func TestLoadParsesCustomConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	custom := `
default_cwd = "/tmp/work"
[[tools]]
name = "aider"
command = ["aider", "--no-auto-commits"]
`
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, _, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultCwd != "/tmp/work" {
		t.Errorf("default_cwd = %q", cfg.DefaultCwd)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0].Name != "aider" ||
		len(cfg.Tools[0].Command) != 2 {
		t.Errorf("tools = %+v, want single aider with 2 args", cfg.Tools)
	}
}
