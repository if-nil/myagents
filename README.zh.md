# myagents

[![CI](https://github.com/if-nil/myagents/actions/workflows/ci.yml/badge.svg)](https://github.com/if-nil/myagents/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/if-nil/myagents.svg)](https://pkg.go.dev/github.com/if-nil/myagents)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

[English](./README.md) · **简体中文**

一个用来**同时运行和管理多个 AI CLI 工具**(claude、codex……)的终端 UI。界面分成
两块:左边(或上方)是会话**列表(roster)**,另一块是**操作区(stage)**——渲染当前
选中会话的终端。于是你可以同屏盯着多个 agent、一眼看出谁在等你,随时切进去交互。

```
┌──────────────┬─────────────────────────────────────────┐
│▎▶ cc web   ● │  (当前选中会话的实时终端,                │
│   frontend   │   一个内嵌的完整 VT 模拟器 —— claude      │
│ ! cx api     │   /codex 在这里渲染、交互)               │
│   backend    │                                          │
│              │                                          │
├──────────────┴─────────────────────────────────────────┤
│ MANAGE  ↑/↓ 选择 · enter 操作 · n 新建 · q 退出          │
└─────────────────────────────────────────────────────────┘
```

> _这里放一段演示 GIF —— 录一小段:起 claude + codex、来回切换、看 `!` 等待提醒、恢复一个已保存会话。_

## 特性

- **内嵌终端** —— 每个 agent 跑在自己的 PTY 里,由真正的 VT 模拟器渲染,所以全屏 TUI
  (claude/codex)能正常工作。
- **精确状态** —— 借助工具自身的 hooks(claude),myagents 能知道一个 agent 是在
  *干活*、*空闲* 还是 *等你处理*;`!` 标记直接告诉你哪个会话需要你。
- **双模式输入** —— 像 vim:管理模式操作列表,操作模式把所有按键转发给 agent;
  `Ctrl-G` 切回。
- **会话持久化** —— 退出后还能恢复;claude 通过 session id 精确恢复到原来那段对话。
- **多后端** —— 一个后端一个工具(官方 / Vertex / Bedrock)。
- **自适应布局**、鼠标支持、应用内设置、跨平台(macOS · Linux · Windows)。

## 安装

```sh
go install github.com/if-nil/myagents/cmd/myagents@latest
```

或从源码构建(需要 Go 1.26+):

```sh
git clone https://github.com/if-nil/myagents
cd myagents
go build -o myagents ./cmd/myagents
```

也可以在 [Releases](https://github.com/if-nil/myagents/releases) 下载预编译二进制。

## 运行

```sh
myagents          # 空列表启动(按 n 新建)
myagents claude   # 启动时直接起一个 agent
```

跨平台:PTY 走 `charmbracelet/x/xpty`(Unix 用 creack/pty,Windows 用 ConPTY)。

## 使用

两种输入模式,类似 vim 的 normal/insert:

- **管理模式(MANAGE)** —— 按键操作列表。
- **操作模式(OPERATE)** —— 除 `Ctrl-G` 外所有按键都转发给当前 agent 的终端;
  `Ctrl-G` 切回管理模式。

### 管理模式按键

| 按键 | 作用 |
| --- | --- |
| `↑`/`↓` 或 `k`/`j` | 选择会话 |
| `Enter` / `→` / `l` | 聚焦选中会话并进入操作模式 |
| `n` | 新建 agent(选工具、选目录、起名字) |
| `r` | 就地重命名选中会话 |
| `x` | 杀掉选中 agent 的进程(在列表里保留为 *exited*) |
| `d` | 移除已退出的 agent / 删除一个已保存会话 |
| `s` | 打开设置(布局、管理区比例) |
| `PgUp`/`PgDn`、`Home`/`End`、鼠标滚轮 | 滚动操作区的历史 |
| 点击会话 | 选中它 |
| 点击操作区 | 聚焦该 agent 并进入操作模式 |
| `q` / `Ctrl-C` | 退出 |

### 已保存会话

退出时会把会话清单(名字、工具、工作目录)存到
`$XDG_STATE_HOME/myagents/sessions.json`。下次启动它们以已保存状态(`◌`)出现在列表里;
按 `Enter` **恢复** —— myagents 在原目录重新启动该工具并继续之前的对话。claude 是
**精确**的:每个会话启动时被分配一个 `--session-id` 并持久化,恢复时用 `--resume <id>`
(精确到那一段对话,而不是"最近一次")。codex 用 `codex resume --last`,其它工具用各自配置的
`resume_args`。进程不会随应用存活;对话历史由 AI CLI 自己保存。用 `d` 删除已保存会话,`r` 重命名。

### 操作模式

就当你直接在 AI CLI 里一样打字。`Ctrl-G` 返回管理模式。鼠标点击、拖动、滚轮都会转发给
agent,所以支持鼠标的工具(比如滚自己的历史)照常可用。

### 列表状态符

| 符号 | 含义 |
| --- | --- |
| `!` | 等你处理(等待审批/输入 —— 来自工具 hooks) |
| `▶` | 运行中、正在产出(working) |
| `○` | 运行中、安静(idle) |
| `✓` | 已退出(底部显示退出码) |
| `✗` | 启动失败 / 异常退出 |
| `●` | 未读输出(你当前没在看的 agent) |

对于 `hook_style = "claude"` 的工具,myagents 通过按会话注入的 `--settings`
(不碰任何配置文件)接上 Claude Code 的 hooks 来上报精确状态,所以 `!` 能可靠地标出在等你的
agent。其它工具回退到基于输出活跃度的粗略启发式(`▶`/`○`)。

## 配置

首次运行会在 `$XDG_CONFIG_HOME/myagents/config.toml`(通常是
`~/.config/myagents/config.toml`)写入默认配置。编辑它来定义你的工具:

```toml
default_cwd = ""           # launcher 里预填的工作目录
layout = "auto"            # "auto" | "horizontal" | "vertical"
roster_ratio = 0.33        # 管理区占整个画面的比例
                           #   (竖屏按高度,横屏按宽度)

[[tools]]
name = "claude"
command = ["claude"]
hook_style = "claude"   # 通过 Claude Code hooks 获取精确状态
# icon = "🤖"           # 可选的列表图标;不写则用彩色的 cc/cx 字母标签

[[tools]]
name = "codex"
command = ["codex"]
```

每个 `[[tools]]` 是一个可启动的工具;`command[0]` 必须在你的 `PATH` 里。launcher 会把找不到
可执行文件的工具标为 *not found*。

### 多后端(官方 / Vertex / Bedrock)

每个后端定义成一个工具,各带各的 `env`;它们能并排运行、在 launcher 里分开列出:

```toml
[[tools]]
name = "claude"            # 官方 Anthropic
command = ["claude"]
hook_style = "claude"

[[tools]]
name = "claude-vertex"     # 谷歌 Vertex AI
command = ["claude"]
hook_style = "claude"
env = ["CLAUDE_CODE_USE_VERTEX=1", "ANTHROPIC_VERTEX_PROJECT_ID=你的GCP项目", "CLOUD_ML_REGION=us-east5"]

[[tools]]
name = "claude-bedrock"    # AWS Bedrock
command = ["claude"]
hook_style = "claude"
env = ["CLAUDE_CODE_USE_BEDROCK=1", "AWS_REGION=us-east-1"]
```

`env` 是 `KEY=value`,只对该 agent 生效,覆盖继承的环境。

## 设计

- agent **进程内托管**(随应用一起退出);用 `AgentManager` 接口预留了将来 daemon 的接缝。
- 内嵌终端复用 `charmbracelet/x/vt`(+ `xpty`),没有从零写 VT 模拟器。
- 每个 agent 用一把互斥锁独占自己的模拟器,保证 scrollback 快照无数据竞争。
- 精确状态来自工具自身的 hooks,通过按会话的 `claude --settings` 接入,不碰任何用户配置文件。

## 已知限制

- 退出应用会终止所有 agent(暂无 detach/重连)。
- 全屏应用(claude/codex)跑在备用屏,没有模拟器 scrollback;滚屏看历史主要对 shell 类 agent 有用。
- 精确状态目前覆盖 Claude Code(`hook_style = "claude"`);其它工具在接入各自 hooks 前使用粗略的输出活跃度启发式。

## 贡献

欢迎 issue 和 PR。`go vet ./... && go test ./...` 需要通过;UI 有一个不变量:渲染出来的画面必须
正好是 `宽 × 高`(见 `internal/tui` 里的测试)。跨平台构建(`GOOS=windows go build ./...`)也要保持绿色。

## 许可证

[MIT](./LICENSE) © if-nil
