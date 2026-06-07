// Package config loads the user's myagents configuration: the set of Tools
// (launchable AI CLIs) and defaults. It lives as TOML under the XDG config dir.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// Tool is a configured, launchable AI CLI. An Agent is an instance of a Tool
// running in a working directory (see CONTEXT.md).
type Tool struct {
	// Name is the user-facing label (e.g. "claude").
	Name string `toml:"name"`
	// Command is the argv to execute; Command[0] is the program.
	Command []string `toml:"command"`
	// HookStyle wires the tool's hooks back to myagents for precise status.
	// "claude" injects a per-session --settings (no user files touched); ""
	// disables hooks and falls back to the output-activity heuristic.
	HookStyle string `toml:"hook_style"`
	// Icon is a short badge (e.g. an emoji) shown next to the agent in the
	// roster. Empty falls back to a tool-colored letter tag.
	Icon string `toml:"icon"`
	// Env is extra environment ("KEY=value") for the launched process,
	// overriding the inherited environment. Use it to point one tool at a
	// different backend (e.g. CLAUDE_CODE_USE_VERTEX=1 / CLAUDE_CODE_USE_BEDROCK=1).
	Env []string `toml:"env"`
	// ResumeArgs are appended to the command when resuming a saved session, to
	// continue the prior conversation. Defaults to ["--continue"] for the
	// claude hook style when unset.
	ResumeArgs []string `toml:"resume_args"`
}

// ResumeFlags returns the args to append after the command when resuming this
// tool so its previous conversation continues. Configured resume_args win;
// otherwise known tools get a built-in default (claude: --continue, codex:
// resume --last as a subcommand).
func (t Tool) ResumeFlags() []string {
	if len(t.ResumeArgs) > 0 {
		return t.ResumeArgs
	}
	if len(t.Command) > 0 {
		switch baseName(t.Command[0]) {
		case "claude":
			return []string{"--continue"}
		case "codex":
			return []string{"resume", "--last"}
		}
	}
	if t.HookStyle == "claude" {
		return []string{"--continue"}
	}
	return nil
}

// Config is the top-level configuration.
type Config struct {
	// Tools are the launchable AI CLIs shown in the new-agent launcher.
	Tools []Tool `toml:"tools"`
	// DefaultCwd is the working directory pre-filled in the launcher. Empty
	// means the process's current directory at launch time.
	DefaultCwd string `toml:"default_cwd"`
	// Layout selects the split orientation: "auto" (default; horizontal on
	// wide screens, vertical on tall/narrow ones), "horizontal", or "vertical".
	Layout string `toml:"layout"`
	// RosterRatio is the fraction of the height the roster occupies in the
	// vertical layout (e.g. 0.33 for a third, 0.5 for half). Default 0.33.
	RosterRatio float64 `toml:"roster_ratio"`
}

// RosterFraction returns the configured fraction of the frame the management
// area occupies (height in the vertical layout, width in the horizontal one),
// defaulting and clamping to a sane range.
func (c *Config) RosterFraction() float64 {
	r := c.RosterRatio
	if r <= 0 {
		r = 0.33
	}
	if r < 0.1 {
		r = 0.1
	}
	if r > 0.6 {
		r = 0.6
	}
	return r
}

// Default returns the built-in configuration used when no config file exists.
func Default() *Config {
	return &Config{
		Layout:      "auto",
		RosterRatio: 0.33,
		Tools: []Tool{
			{Name: "claude", Command: []string{"claude"}, HookStyle: "claude"},
			{Name: "codex", Command: []string{"codex"}},
		},
	}
}

// Path returns the absolute path to the config file (it may not exist yet),
// ensuring its directory exists. It uses $XDG_CONFIG_HOME or ~/.config on every
// platform — including macOS, where it deliberately does not use
// ~/Library/Application Support, matching the convention developer CLIs expect.
func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	p := filepath.Join(dir, "myagents", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// Load reads the config file, returning its path. If the file does not exist, a
// default config is written to that path and returned, so first-run users get
// an editable starting point.
func Load() (*Config, string, error) {
	path, err := Path()
	if err != nil {
		return nil, "", fmt.Errorf("resolve config path: %w", err)
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg := Default()
		if werr := write(path, cfg); werr != nil {
			// Non-fatal: fall back to in-memory defaults.
			return cfg, path, nil
		}
		return cfg, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, path, fmt.Errorf("parse config %s: %w", path, err)
	}
	if len(cfg.Tools) == 0 {
		cfg.Tools = Default().Tools
	}
	cfg.normalize()
	return &cfg, path, nil
}

// normalize fills in inferable defaults so older or hand-written configs still
// get tool integrations. A tool that runs `claude` gets the claude hook style
// even if it predates the hook_style field.
func (c *Config) normalize() {
	for i := range c.Tools {
		t := &c.Tools[i]
		if t.HookStyle == "" && len(t.Command) > 0 && baseName(t.Command[0]) == "claude" {
			t.HookStyle = "claude"
		}
	}
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// Save writes the configuration back to its file (used by the in-app settings).
func Save(c *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return write(path, c)
}

func write(path string, cfg *Config) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
