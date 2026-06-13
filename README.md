# myAgents

[![CI](https://github.com/if-nil/myagents/actions/workflows/ci.yml/badge.svg)](https://github.com/if-nil/myagents/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/if-nil/myagents.svg)](https://pkg.go.dev/github.com/if-nil/myagents)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

**English** · [简体中文](./README.zh.md)

A terminal UI for running and managing multiple AI CLI tools (claude, codex, …)
side by side. The screen splits into a **roster** of your sessions and a
**stage** that renders the selected session's terminal — so you can watch
several agents at once, see which one needs you, and jump in to interact.

```
┌──────────────┬─────────────────────────────────────────┐
│▎▶ cc web   ● │  (the selected agent's live terminal,    │
│   frontend   │   a full embedded VT emulator — claude   │
│ ! cx api     │   /codex render and interact here)       │
│   backend    │                                          │
│              │                                          │
├──────────────┴─────────────────────────────────────────┤
│ MANAGE  ↑/↓ select · enter operate · n new · q quit     │
└─────────────────────────────────────────────────────────┘
```

> _A demo GIF goes here — record a short session: launch claude + codex,
> switch between them, see the `!` waiting badge, resume a saved session._

## Features

- **Embedded terminals** — each agent runs in its own PTY rendered by a real VT
  emulator, so full-screen TUIs (claude/codex) work normally.
- **Precise status** — via the tool's own hooks (claude) myAgents knows when an
  agent is *working*, *idle*, or *waiting for you* — the `!` badge tells you
  which session needs attention.
- **Two-mode input** — like vim: manage the roster, or operate an agent with
  every key forwarded; `Ctrl-G` switches back.
- **Sessions persist** — quit and resume later; claude resumes the exact
  conversation by session id.
- **Multiple backends** — one tool per backend (official / Vertex / Bedrock).
- **Adaptive layout**, mouse support, in-app settings, cross-platform
  (macOS · Linux · Windows).

## Install

```sh
go install github.com/if-nil/myagents/cmd/myagents@latest
```

Or build from source (requires Go 1.26+):

```sh
git clone https://github.com/if-nil/myagents
cd myagents
go build -o myagents ./cmd/myagents
```

## Run

```sh
myagents          # start with an empty roster (press n to launch)
myagents claude   # start with one agent already running
```

Cross-platform: PTYs go through `charmbracelet/x/xpty` (creack/pty on Unix,
ConPTY on Windows).

## Usage

Two input modes, like vim's normal/insert:

- **MANAGE mode** — keys drive the roster.
- **OPERATE mode** — every key is forwarded to the focused agent's terminal,
  except `Ctrl-G`, which returns to MANAGE mode.

### MANAGE mode keys

| Key | Action |
| --- | --- |
| `↑`/`↓` or `k`/`j` | select an agent |
| `Enter` / `→` / `l` | focus the selected agent and enter OPERATE mode |
| `n` | new agent (pick a tool, choose a working directory, name it) |
| `r` | rename the selected session (inline) |
| `x` | kill the selected agent's process (kept in the roster as *exited*) |
| `d` | remove an exited agent / forget a saved session |
| `s` | open settings (layout, roster ratio) |
| `PgUp`/`PgDn`, `Home`/`End`, mouse wheel | scroll the stage's scrollback |
| `Ctrl-L` | force a full redraw (clears rendering artifacts; see [Windows 10 rendering quality](#windows-10-rendering-quality)) |
| click an agent | select it |
| click the stage | focus the agent and enter OPERATE mode |
| `q` / `Ctrl-C` | quit |

### Saved sessions

Quitting saves the session list (name, tool, working directory) to
`$XDG_STATE_HOME/myagents/sessions.json`. On the next run they appear in the
roster as saved (`◌`); press `Enter` to **resume** — myAgents relaunches the
tool in the same directory and continues the prior conversation. For claude this
is precise: each session is launched with an assigned `--session-id`, persisted,
and resumed with `--resume <id>` (the exact conversation, not just the most
recent). codex uses `codex resume --last`, and other tools use their configured
`resume_args`. Processes do not survive the app; the conversation
history is kept by the AI CLI. Use `d` to forget a saved session, `r` to rename.

### OPERATE mode

Type as if you were in the AI CLI directly. `Ctrl-G` returns to MANAGE mode.
Mouse clicks, drags, and wheel are forwarded to the agent, so tools that support
the mouse (e.g. scrolling their own history) work as usual. `Shift+PgUp` /
`Shift+PgDn` page through the stage's scrollback without leaving OPERATE mode —
a keyboard fallback for when the wheel is unavailable (e.g. some Windows
consoles).

### Roster status glyphs

| Glyph | Meaning |
| --- | --- |
| `!` | needs you (waiting for approval/input — from tool hooks) |
| `▶` | running, producing output (working) |
| `○` | running, quiet (idle) |
| `✓` | exited (footer shows the exit code) |
| `✗` | failed to start / died abnormally |
| `●` | unread output (an agent you are not currently watching) |

For tools with `hook_style = "claude"`, myAgents wires Claude Code's hooks (via
a per-session `--settings`, touching no config files) to report
precise activity, so `!` reliably marks agents waiting on you. Other tools fall
back to a coarse output-activity heuristic (`▶`/`○`).

## Configuration

On first run, a default config is written to
`$XDG_CONFIG_HOME/myagents/config.toml` (usually
`~/.config/myagents/config.toml`). Edit it to define your tools:

```toml
default_cwd = ""           # pre-filled working directory in the launcher
layout = "auto"            # "auto" | "horizontal" | "vertical"
roster_ratio = 0.33        # management area as a fraction of the frame
                           #   (height when vertical, width when horizontal)

[[tools]]
name = "claude"
command = ["claude"]
hook_style = "claude"   # precise status via Claude Code hooks
# icon = "🤖"           # optional roster badge; defaults to a colored cc/cx tag

[[tools]]
name = "codex"
command = ["codex"]
```

Each `[[tools]]` entry is a launchable tool; `command[0]` must be on your
`PATH`. The launcher marks tools whose binary it cannot find as *not found*.

### Multiple backends (official / Vertex / Bedrock)

Define one tool per backend with its own `env`; they run side by side and the
launcher lists them separately:

```toml
[[tools]]
name = "claude"            # official Anthropic
command = ["claude"]
hook_style = "claude"

[[tools]]
name = "claude-vertex"     # Google Vertex AI
command = ["claude"]
hook_style = "claude"
env = ["CLAUDE_CODE_USE_VERTEX=1", "ANTHROPIC_VERTEX_PROJECT_ID=your-gcp-project", "CLOUD_ML_REGION=us-east5"]

[[tools]]
name = "claude-bedrock"    # AWS Bedrock
command = ["claude"]
hook_style = "claude"
env = ["CLAUDE_CODE_USE_BEDROCK=1", "AWS_REGION=us-east-1"]
```

`env` entries are `KEY=value` and override the inherited environment for that
agent only.

## Design

- Agents are hosted **in-process** (they die with the app); an `AgentManager`
  interface is the seam for a future daemon.
- The embedded terminal reuses `charmbracelet/x/vt` (+ `xpty`) rather than
  building a VT emulator from scratch.
- Each agent owns its emulator behind a mutex for race-free scrollback
  snapshots.
- Precise status comes from the tool's own hooks, wired per session via
  `claude --settings` so no user config files are touched.

## Known limitations (v1)

- Quitting the app terminates all agents (no detach/reattach yet).
- Full-screen apps (claude/codex) run in the alternate screen, which has no
  emulator scrollback; scrollback browsing mainly helps shell-like agents.
- Precise status currently covers Claude Code (`hook_style = "claude"`); other
  tools use the coarse output-activity heuristic until their hooks are wired.

## Windows 10 rendering quality

On Windows, each agent's terminal is hosted by **ConPTY**, which is frozen at
your OS build. On Windows 10 (e.g. build 19044) that built-in ConPTY has two
well-known problems that show up in the stage:

- **Ghost characters** — wide-character (CJK) bugs leave stray characters that
  the real input no longer contains and that backspace cannot delete.
- **Dead scrollback** — it often repaints in place instead of scrolling, so the
  emulator never accumulates history and the wheel appears to do nothing.

**Quick mitigation:** press `Ctrl-L` (MANAGE mode) to force a full redraw — this
makes the child replay its authoritative screen and flushes ghost cells.

**Full fix — ship a newer ConPTY:** myAgents can load a redistributable
`conpty.dll` instead of the OS one (the same approach VS Code / node-pty use).

1. Download the MIT-licensed NuGet package
   [`Microsoft.Windows.Console.ConPTY`](https://www.nuget.org/packages/Microsoft.Windows.Console.ConPTY)
   (a `.nupkg` is a zip — rename and extract it).
2. Copy two files **next to `myagents.exe`**:
   - `conpty.dll` from `runtimes/win-<arch>/native/conpty.dll`
   - `OpenConsole.exe` from `build/native/runtimes/<arch>/OpenConsole.exe`

   where `<arch>` matches `myagents.exe` (`x64` or `arm64`). They are a matched
   pair — use the same package version for both.

   > ⚠️ If `OpenConsole.exe` is missing beside `conpty.dll`, the dll **silently**
   > falls back to the inbox `conhost.exe` and the old bugs return. There is no
   > error — verify with the command below.

3. Verify it took effect:

   ```sh
   myagents version
   # myagents <version>
   # conpty: C:\path\to\conpty.dll      ← redistributable active
   # conpty: kernel32                   ← still the OS built-in
   ```

   A parenthesized note (e.g. `kernel32 (dll fallback: …)`) explains any
   fallback.

**Overrides** (environment variables):

| Variable | Effect |
| --- | --- |
| `MYAGENTS_CONPTY=system` | force the OS built-in ConPTY (ignore any dll) |
| `MYAGENTS_CONPTY_DLL=<path>` | load a specific `conpty.dll` |

Requires Windows 10 1809+ (ConPTY's own minimum). The newer dll fixes the
ghost-character bugs and improves scroll synthesis, but ConPTY still re-composes
output rather than passing it through, so scrollback is not identical to a Unix
PTY.

## Contributing

Issues and PRs welcome. `go vet ./... && go test ./...` should pass; the UI keeps
an invariant that the rendered frame is exactly `width × height` (see the tests
in `internal/tui`). Cross-platform builds (`GOOS=windows go build ./...`) must
stay green.

## License

[MIT](./LICENSE) © if-nil
