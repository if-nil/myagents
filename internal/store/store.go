// Package store persists the list of sessions across runs so they can be
// resumed later. It saves only metadata (name, tool, working directory) — the
// conversation itself is kept by the AI CLI (e.g. claude --continue), and
// processes do not survive the app (see docs/adr/0001).
package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// SavedSession is a previously-seen session that can be resumed.
type SavedSession struct {
	Name     string `json:"name"`
	Tool     string `json:"tool"`
	Cwd      string `json:"cwd"`
	ResumeID string `json:"resume_id,omitempty"` // precise-resume id (e.g. claude session UUID)
}

// Path returns the absolute path to the sessions file (may not exist yet),
// ensuring its directory exists. Uses $XDG_STATE_HOME or ~/.local/state on all
// platforms, consistent with the config dir.
func Path() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	p := filepath.Join(dir, "myagents", "sessions.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// Load reads the saved sessions; a missing file yields an empty list.
func Load() ([]SavedSession, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ss []SavedSession
	if err := json.Unmarshal(data, &ss); err != nil {
		return nil, err
	}
	return ss, nil
}

// Save writes the sessions list, replacing any previous contents.
func Save(ss []SavedSession) error {
	path, err := Path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(ss, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
