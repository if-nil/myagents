// Command myagents is a TUI for managing AI CLI tools (claude, codex, ...).
// The screen is split into a roster of running Agents and a stage that renders
// the selected Agent's terminal. See CONTEXT.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/if-nil/myagents/internal/agent"
	"github.com/if-nil/myagents/internal/config"
	"github.com/if-nil/myagents/internal/store"
	"github.com/if-nil/myagents/internal/tui"
)

// version is overridden at build time via -ldflags by goreleaser.
var version = "dev"

func main() {
	// `myagents hook ...` is the reporter invoked by a tool's hooks; it is not
	// the interactive UI. Keep it first and fast.
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		runHook(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("myagents", version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "myagents:", err)
		os.Exit(1)
	}
}

// runHook reports a single hook event back to the running myagents instance and
// exits. It always exits 0 so it never blocks or fails the host tool.
func runHook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentID := fs.String("agent", "", "agent id")
	addr := fs.String("addr", "", "listener address")
	event := fs.String("event", "", "hook event name")
	_ = fs.Parse(args)

	// Read the event JSON the host tool writes to stdin and pull out the
	// current session id, so myagents tracks the conversation even across
	// /resume or /clear inside the session.
	payload, _ := io.ReadAll(os.Stdin)
	var in struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(payload, &in)

	// Opt-in debug: confirms the host tool is actually invoking this hook.
	if p := os.Getenv("MYAGENTS_HOOK_DEBUG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%s agent=%s event=%s addr=%s\n", time.Now().Format(time.RFC3339), *agentID, *event, *addr)
			_ = f.Close()
		}
	}

	if *addr == "" || *agentID == "" || *event == "" {
		return
	}
	conn, err := net.DialTimeout("tcp", *addr, 500*time.Millisecond)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	_ = json.NewEncoder(conn).Encode(map[string]string{
		"agent": *agentID, "event": *event, "session": in.SessionID,
	})
}

func run() error {
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}

	mgr := agent.NewInProcessManager()
	defer mgr.Close()

	// If a command is given on the CLI, spawn it up front. Otherwise start with
	// an empty roster; press `n` to launch a configured Tool.
	if cmd := os.Args[1:]; len(cmd) > 0 {
		if _, err := mgr.Spawn(agent.SpawnSpec{Tool: cmd[0], Command: cmd}); err != nil {
			return fmt.Errorf("spawn initial agent: %w", err)
		}
	}

	saved, _ := store.Load() // resumable sessions from previous runs

	p := tea.NewProgram(tui.New(mgr, cfg, saved))
	_, err = p.Run()
	return err
}
