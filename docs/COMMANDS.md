<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Command System

Goa commands are the primary way users interact with the agent. They are prefixed with `/` and self-register via `init()` in individual files under `core/commands/`.

## Architecture

```
User types "/command arg1 arg2"
                      │
    ▼
┌─────────────────────┐
│  CommandRouter      │
│  (core/router.go)   │
│                     │
│  1. Parse "/cmd"    │
│  2. Check suffix:   │
│     /cmd → execute  │
│     /cmd? → short   │
│     /cmd?? → long   │
│  3. Resolve in      │
│     CommandRegistry │
└────────┬────────────┘
                      │
         ▼
┌─────────────────────┐
│  Command.Run()      │
│  (core/command.go)  │
│                     │
│  Receives:          │
│  • Context          │
│    - Config         │
│    - AgentManager   │
│    - SessionStore   │
│    - ExecutionCtrl  │
│    - ToolRegistry   │
│  • args []string    │
└─────────────────────┘
```

## Command Interface

Every command implements:

```go
type Command interface {
    Name() string
    Aliases() []string
    ShortHelp() string      // ≤100 chars, for /help listing
    LongHelp() string       // ≤2000 chars, for /help <cmd>
    Run(ctx Context, args []string) error
}
```

Commands register themselves:

```go
func init() {
    core.RegisterCommand(&ModeCommand{})
}
```

## Routing Rules

| Input | Behavior |
|-------|----------|
| `/cmd` | Execute the command with arguments |
| `/cmd?` | Show short help (one-line description) |
| `/cmd??` | Show long help (full documentation + examples) |
| `/cmd arg1 arg2` | Pass `["arg1", "arg2"]` to `Run()` |

## Command Reference

### `/clone` — Branch session tree

```
Usage: /clone:<parent-node-id>
Aliases: (none)

Create a new branch in the session tree from the given parent node.
See also `/tree`, `/fork`.
  /clone:node-abc    → Branch from node-abc
```

### `/companion` — Toggle companion sub-agent

```
Usage: /companion [:on|:off|:agent|:framework]
Aliases: (none)

Enable or disable the companion sub-agent for code review.
  /companion              → Show current status
  /companion:on           → Enable companion (agent-driven mode)
  /companion:agent        → Enable agent-driven mode (default)
  /companion:framework    → Enable framework-driven mode (auto-review)
  /companion:off          → Disable companion entirely
```

See [USER-GUIDE.md](USER-GUIDE.md#3-companion--sub-agent-code-review) for full documentation.

### `/compress` — Force context compression

```
Usage: /compress[:strategy][:force]
Aliases: (none)

Trigger context compression on the active agent session. Reduces token
usage by summarizing older conversation turns or eliding tool results.
A manual invocation always forces compression, bypassing configured thresholds.

Available strategies:
  tool_elision  → Replace old tool args/results with placeholders
  selective     → Drop oldest messages, keep system + recent turns
  summarize     → Ask the LLM to summarize older turns
  hybrid        → Tool elision → selective → summarize
  micro         → Truncate old tool result bodies (default, after 1h idle)

Examples:
  /compress              Compress with configured strategy (forced)
  /compress:micro        Force micro compaction
  /compress:summarize    Force summarization
```

### `/copy` — Copy last response to clipboard

```
Usage: /copy
Aliases: (none)

Copies the most recent agent response text to your system clipboard
using platform-native tools or OSC 52 escape sequence.
```

### `/debug` — Toggle debug mode

```
Usage: /debug
Aliases: /dbg

Toggle debug mode or show debug information about the current session.
Useful for diagnosing agent behavior and tool execution flow.
```

### `/docs` — Browse embedded documentation

```
Usage: /docs:topic
Aliases: (none)

List all available embedded documentation, or show a specific document.
Documents cover architecture, commands, configuration, tools, profiles,
orchestrator, workflows, and more.

Examples:
  /docs                  List all available documents
  /docs:ARCHITECTURE     Show the architecture document
  /docs:TOOLS            Show the tools reference
```

### `/exchange` — Show LLM exchange details

```
Usage: /exchange[:turn-number]
Aliases: (none)

Dump the complete LLM exchange for a turn, including stats, user input,
thinking blocks, tool calls/results, and assistant responses.

Examples:
  /exchange         Show last turn
  /exchange:3       Show turn 3
```

### `/export` — Export diagnostic bundle

```
Usage: /export [path] [description]
Aliases: (none)

/export                         → Export to .goa/exports/goa-export-<timestamp>.zip
/export /tmp/my-debug-session   → Export to custom path
/export:/tmp/my-debug-session   → Same, using colon syntax
/export /tmp/debug.zip "agent hangs" → Export with inline issue description
```

Create a self-contained ZIP bundle with everything needed to investigate
the current session. If no description is provided on the command line,
`/export` asks for one on the **main input line** (the regular prompt at the
bottom of the screen) so an agent can understand what went wrong.

The bundle contains:
- `manifest.json` — structured metadata (session id, Goa version, Go version,
  OS/arch, workspace, config dir, active profile/model/provider, bundled files,
  missing files)
- `issue.md` — the issue description you entered
- `session/events.jsonl` — raw agent event stream
- `logs/goa.log` — agent/LLM debug log
- `logs/keys.log` — raw TUI keystroke trace (when available)
- `config/project.yaml` — project `.goa/config.yaml`
- `config/user.yaml` — user `~/.goa/config.yaml`
- `config/local.yaml` — project `.goa/config.local.yaml`
- `system/info.json` — runtime environment (shell, terminal, Go version, etc.)
- `session.md` — human-readable session summary
- `README.md` — agent-facing investigation guide

Sensitive values are redacted in-memory before being written to the bundle.
API keys, tokens, secrets, and passwords in config files are replaced with
`[REDACTED]`. Bearer tokens in logs are also redacted. Original files on disk
are left untouched.

`goa` can also export from the command line:

```bash
# Export the current session
echo "The agent hangs after bash" | goa -export-output ./debug.zip

# Export a saved session by ID
echo "Wrong model used" | goa -export-session s_abc123 -export-output ./debug.zip

# Exclude the global agent log
echo "TUI input issue" | goa -export-output ./debug.zip -include-global-log=false
```

### `/fork` — Branch session tree from node

```
Usage: /fork:<parent-node-id>
Aliases: (none)

Create a new branch in the session tree from a specific parent node.
Similar to `/clone` but explicitly selects a branching operation.
See `/tree` to list available nodes.

Examples:
  /fork:node-abc    → Create branch from node-abc
```

### `/go` — Continue paused pipeline

```
Usage: /go
Aliases: (none)

Continues a paused pipeline by approving the current gate.
Only works when a pipeline is paused at an approval gate.
See also `/pipeline`.

Examples:
  /go    → Approve current gate and continue pipeline
```

### `/goa` — Show Goa information

```
Usage: /goa
Aliases: (none)

Show information about the Goa agent itself, including version,
configuration, and available subsystems.
```

### `/goal` — Manage coding goals

```
Usage: /goal <subcommand> [args]
Subcommands: create, status, pause, resume, cancel, list
```

Track long-running coding tasks. Goals persist across session restarts.

```
/goal create "Refactor the auth module"  → Create a new goal
/goal status                              → Show active goal
/goal pause                               → Pause current goal
/goal resume goal-1719000000              → Resume a paused goal
/goal cancel                              → Cancel active goal
/goal list                                → List all goals
```

When a goal is active, it is injected into the system prompt so the LLM
tracks progress.

### `/help` — Show help

```
Usage: /help [command]
Aliases: /h, /?

Show all available commands, or detailed help for a specific command.
  /help       → List all commands
  /help mode  → Show /mode documentation
```

### `/mode` — Switch agent mode

```
Usage: /mode [coder|planner|reviewer|<custom>]
Aliases: /m (built-in), /profile (compat alias)

Switch the active agent mode. Changes system prompt, default autonomy, and sub-agent tool access.
Without arguments opens an interactive mode picker.
  /mode               → Open interactive mode picker
  /mode:coder         → Switch to coder mode (full coding)
  /mode:planner       → Switch to planner mode (architecture)
  /mode:reviewer      → Switch to reviewer mode (code review)
```

### `/autonomy` — Set autonomy level

```
Usage: /autonomy [yolo|solo|confirm|review]
Aliases: (none)

Change the autonomy level for the current mode. The change persists to the
project configuration.
  /autonomy           → Show current autonomy (or open picker)
  /autonomy:yolo      → Execute all tool calls automatically
  /autonomy:solo      → Auto-run tools constrained to the codebase
  /autonomy:confirm   → Pause before each tool call
  /autonomy:review    → Queue writes for batch approval at turn end
```

### `/profile` — Alias for /mode

```
Usage: /profile [coder|planner|reviewer|<custom>]
Aliases: /p

Compatibility alias for /mode. All mode operations should use /mode.
  /profile           → Open interactive mode picker
  /profile:coder     → Switch to coder mode

### `/model` — Switch model

```
Usage: /model [name]
Aliases: (none)

Switch the active LLM model at runtime. Without arguments opens an
interactive picker. If auto_save_model is enabled, the change persists
to config. Use backspace/delete on a selected item in the picker to
remove the model (with confirmation).
  /model               → Open interactive model picker
  /model gpt-4o        → Switch to GPT-4o
  /model llama-3.2-1b  → Switch to local model
```

### `/orchestrate` — Run multi-agent orchestration

```
Usage: /orchestrate <subcommand> [args]
Aliases: /orch

Subcommands:
  new [hub|fanout|pipeline] [goal <objective>] <objective>
  list
  resume <run-id>
  steer <agent-id|all|orchestrator> <text>
```

Run a multi-agent orchestration with per-run topology selection (hub/fanout/
pipeline), optional goal binding, and live observability. Orchestrations are
fully event-sourced under `.goa/orchestrator/<run-id>/` and resumable.

  /orchestrate new hub "Research X and summarize"
  /orchestrate new fanout goal <obj> <obj>
  /orchestrate list
  /orchestrate resume run-1234
  /orchestrate steer all "consider edge cases"
```

### `/provider` — Manage providers

```
Usage: /provider [list|set <id>]
Aliases: /prov

Manage LLM providers.
  /provider        → Show current provider
  /provider list   → List all configured providers
  /provider set local → Switch to "local" provider
```

### `/session` — Session management

```
Usage: /session [list | save [name] | restore [name] | delete [name] | new | import <path>]
Aliases: (none)

Manage saved sessions and session lifecycle.
  /session                        → Show interactive restore picker (default)
  /session list                   → List all saved sessions
  /session save my-work           → Save the current session as "my-work"
  /session restore my-work        → Restore the "my-work" session
  /session delete my-work         → Delete the "my-work" session
  /session:new                    → Start a fresh session (clears history, stats)
  /session:import:./debug.zip     → Import a session from an export ZIP
```

The `new` subcommand replaces the former `/new`, `/reset`, `/clear` commands.
The `import` subcommand reads a ZIP file produced by `/export` and adds its
session to the saved sessions list, using the ZIP filename as the session name.

### `/undo` — Undo last turn

```
Usage: /undo
Aliases: /u

Remove the last conversation turn from history. Only works if the
previous turn was a tool execution.
```

### `/stop` — Stop agent

```
Usage: /stop
Aliases: (none)

Signal the agent to stop its current operation and return control to the user.
```

### `/retry` — Retry last agent turn

```
Usage: /retry
Aliases: (none)

Re-run the last agent turn. Useful if a tool failed transiently.
```

### `/memory` — Manage memory

```
Usage: /memory <list|read|write|append|delete> [name] [content]
Aliases: /mem

Manage persistent memory files stored in .goa/memory/.
  /memory list         → List all memory files
  /memory read foo     → Read the "foo" memory
  /memory write foo ...  → Write/replace "foo" memory
  /memory append foo ... → Append to "foo" memory
  /memory delete foo   → Delete "foo" memory
```

### `/dream` — Consolidate memories

```
Usage: /dream [apply|review|status]
Aliases: (none)

Consolidate raw memory files into a single, cleaned memory file.
  /dream            → Run a dream and write a review file to .goa/memory.dream/
  /dream apply      → Run a dream and apply the result to .goa/memory.consolidated/
  /dream review     → Run a dream and show a diff summary
  /dream status     → Show dream configuration
```

Dreams are non-destructive: the input `.goa/memory/` files are never
modified. When applied, the original memories are backed up to
`.goa/memory.backup/<timestamp>/` before the consolidated file is written.

### `/config` — View/edit config

```
Usage: /config [set|add|remove|reload] [key] [value]
Aliases: /cfg

Inspect and modify configuration at runtime.
  /config                    → Interactive settings menu
  /config set <key> <value>  → Set a config value
  /config add provider <id> <endpoint> [api-key]  → Add provider
  /config add model <id> <provider-id> <model-name> → Add model
  /config remove provider <id>  → Remove a provider
  /config remove model <id>     → Remove a model
  /config reload       → Reload config from disk
```

Settings menu entries include:
  Profile, Active model, Provider, Manage models, Execution mode,
  Compression, Theme, Spinner, Thinking level, Thinking blocks,
  Show thinking, Multi-agent, Tools

### `/skill` — Manage skills

```
Usage: /skill [run <name> [args...] | show <name>]
Aliases: /sk

Manage and execute skills.
  /skill            → List all available skills
  /skill run review src/auth.go  → Run a skill
```

### `/tools` — Tool management and execution

```
Usage: /tools [<name>] [:on|:off] [:<key>=<value>,...]
Aliases: (none)

List, inspect, toggle, or directly execute tools.
  /tools                          → List all registered tools
  /tools:read                     → Show detailed info about a tool
  /tools:memento:on                → Enable a configurable tool
  /tools:bg_exec:off               → Disable a configurable tool
  /tools:python:on                 → Enable the python tool
  /tools:python:off                → Disable the python tool
  /tools:search:pattern=TODO       → Execute a tool directly
  /tools:search:glob=*.go,pattern=test  → With multiple params

Tool execution runs the tool immediately and displays output in the
conversation (not sent to the model). Parameters use comma-separated
key=value syntax. Supported types:
  strings:    pattern=test
  booleans:   case_sensitive=true, recursive=false
  integers:   max_results=50, context_lines=3
```

### `/plugin` — Plugin management

```
Usage: /plugin[:<subcommand>[:<arg>]]
Aliases: /plugins

Manage JS plugins — install, list, enable, disable, remove.

  /plugin                          Interactive selector (Enter to toggle on/off)
  /plugin:list                     List installed plugins with status and hash
  /plugin:install:<git-url>        Install a plugin from a git URL
  /plugin:remove:<id>              Uninstall a plugin
  /plugin:enable:<id>              Activate a disabled plugin (trust-gated)
  /plugin:disable:<id>             Deactivate an enabled plugin
```

Installation workflow:
```
/plugin:install:https://github.com/user/my-plugin.git   → clone + validate + hash
/trust:my-plugin                                          → approve (first time only)
/plugin:enable:my-plugin                                  → activate
/reload                                                   → load the JS runtime
```

See [PLUGINS.md](PLUGINS.md) for the full plugin development guide and API
reference. The **provider-quota** plugin (bundled) is the canonical reference
implementation.

### `/hotkeys` — Show keyboard shortcuts

```
Usage: /hotkeys
Aliases: (none)

Display every keyboard shortcut grouped by category (navigation, editing,
application actions). Shortcuts come from the default keybinding set.
  /hotkeys     Show the shortcut table
```

### `/login` — OAuth login for providers

```
Usage: /login[:<provider>[:<token>]]
Aliases: (none)

Manage OAuth tokens for authentication-required providers.
  /login                    → List stored providers
  /login:<provider>         → Start OAuth login for a provider
  /login:<provider>:<token> → Store a token for a provider
```

### `/logout` — Clear provider tokens

```
Usage: /logout:<provider>
Aliases: (none)

Remove stored authentication tokens for the specified provider.
  /logout:openai    → Remove OpenAI tokens
```

### `/pair` — Pair programming mode

```
Usage: /pair:<task description>
Aliases: (none)

Enable pair programming mode with planner + coder agents.
The planner decomposes the task and the coder implements it.

Examples:
  /pair:Implement user authentication
  /pair:Refactor the database layer
```

### `/permission` — Manage tool permission rules

```
Usage: /permission [list|add|remove|clear]
Aliases: (none)

Manage access control rules for tool execution. Patterns support
`*` (single-segment) and `**` (multi-segment) wildcards.
  /permission                          → Show current rules
  /permission:list                      → Alias for show
  /permission:add:<pattern>:<decision>  → Add a rule (allow|deny|ask)
  /permission:remove:<id>               → Remove a rule by index
  /permission:clear                     → Remove all rules
```

### `/pipeline` — Multi-agent pipeline management

```
Usage: /pipeline [subcommand]
Aliases: (none)

Manage multi-agent pipeline runs. A pipeline executes stages sequentially
(planner → coder → reviewer) with each agent building on the previous output.

  /pipeline                   → List active pipelines
  /pipeline:list              → List all pipeline runs
  /pipeline:cancel            → Cancel the current pipeline
```

See [WORKFLOWS.md](WORKFLOWS.md) for workflow-based pipelines.

### `/prompt` — Show assembled system prompt

```
Usage: /prompt
Aliases: /sp

Display the assembled system prompt including the active profile,
memory injections, and skill content. Useful for debugging what
the agent currently sees.
Also accessible via Ctrl+P and the side panel prompt tab.
```

### `/pty` — Manage Pseudo-Terminal sessions

```
Usage: /pty[:subcommand[:args]]
Aliases: (none)

Manage pseudo-terminal sessions started by the agent via pty_exec tool.
  /pty                    → List active PTY sessions
  /pty:ps                 → List active PTY sessions
  /pty:kill:<id>          → Terminate a session (SIGTERM → SIGKILL)
  /pty:read:<id>          → Read last 100 lines of output
  /pty:read:<id>:<N>      → Read last N lines
  /pty:read:<id>:N:ansi   → Read last N lines with ANSI codes preserved
  /pty:monitor:<id>       → Open live monitor overlay
  /pty:write:<id>:<text>  → Send input to a session
```

### `/quit` — Exit Goa

```
Usage: /quit
Aliases: /q, /exit

Exit Goa. If an agent session is running, you will be prompted to confirm.
```

### `/reload` — Reload skills and context

```
Usage: /reload
Aliases: (none)

Reloads all skill directories and context files (AGENTS.md) from disk,
updates the skill registry, and rebuilds the system prompt. No restart needed.

What gets reloaded:
  - Skills: re-scans all SKILL.md directories
  - Context: re-scans for AGENTS.md/CLAUDE.md files (ancestor walk)
  - Shortcuts: re-registers /<command> shortcuts for skills with
    "command:" frontmatter
```

### `/review` — Interactive code review

```
Usage: /review[:commit]
Aliases: (none)

Start an interactive code review against a git commit or working tree changes.
Opens a pager where you can scroll, add comments, and submit to the agent.

  /review              → Review working tree vs HEAD (or HEAD^1..HEAD)
  /review:<commit>     → Review against a specific base commit
  /review:list         → List last 10 commits
  /review:status       → Show active review sessions
  /review:submit       → Send the review to the main agent
  /review:export       → Export review to review_<base>_<timestamp>.md

Pager keys: ↑/↓ (scroll), c (comment), e (edit comment), d (delete comment),
b (change base), s (submit), x (export), q/Esc (close).
```

### `/reviewer` — Code review with coder+reviewer cycle

```
Usage: /reviewer:<task description>
Aliases: /rev

Enable code review mode with coder + reviewer agent cycle.
The coder implements and the reviewer checks for correctness, security, and style.

Examples:
  /reviewer:Add input validation
  /reviewer:Fix the sorting algorithm
```

### `/setup` — Launch setup wizard

```
Usage: /setup
Aliases: /init, /wizard

Launch the interactive setup wizard to configure providers, profiles,
execution mode, and project initialization. Automatically shown on first run.
Use at any time to reconfigure.
```

### `/stats` — Show token usage breakdown

```
Usage: /stats[:turn-number]
Aliases: /tokens, /tok

Show detailed token usage breakdown per turn. With a turn number,
show a detailed tree for that turn.
  /stats        → Show per-turn overview
  /stats:3      → Show detailed breakdown for turn 3
```

### `/swarm` — Swarm mode

```
Usage: /swarm[:on|:off|:<task>]
Aliases: (none)

Enable or disable swarm mode for parallel multi-agent task processing.

  /swarm:on           → Enable swarm mode
  /swarm:off          → Disable swarm mode
  /swarm:<task>       → Run a one-off swarm task
```

### `/telemetry` — Anonymous telemetry

```
Usage: /telemetry [:on|:off]
Aliases: (none)

Control anonymous usage telemetry for improving Goa.
  /telemetry       → Show current status
  /telemetry:on    → Enable anonymous telemetry
  /telemetry:off   → Disable anonymous telemetry
```

### `/theme` — Manage themes

```
Usage: /theme [:set:<name>|:edit:<name>]
Aliases: (none)

List, switch, or edit color themes.

  /theme                  → List available themes
  /theme:set:<name>        → Activate a theme (dark, light, custom)
  /theme:edit:<name>       → Open theme JSON in $EDITOR
```

### `/thinking` — Set reasoning effort level

```
Usage: /thinking [level]
Aliases: (none)

Set the reasoning effort level for the current agent.

Levels:
  off     — No reasoning
  minimal — Very brief reasoning (~1k tokens)
  low     — Light reasoning (~2k tokens)
  medium  — Moderate reasoning (~8k tokens)
  high    — Deep reasoning (~16k tokens)
  xhigh   — Maximum reasoning (~32k tokens)

Examples:
  /thinking       → Show current level
  /thinking:high  → Set deep reasoning
```

### `/thinking-blocks` — Toggle thinking block visibility

```
Usage: /thinking-blocks[:on|:off]
Aliases: (none)

Toggle the visibility of main-agent thinking/reasoning blocks.

  /thinking-blocks       → Show current status
  /thinking-blocks:on    → Expand thinking blocks
  /thinking-blocks:off   → Collapse thinking blocks
```

### `/tree` — Session tree viewer

```
Usage: /tree
Aliases: (none)

Display the session tree showing branches and forks.
See also `/fork`, `/clone`.
```

### `/trust` — Manage trust decisions

```
Usage: /trust [list|allow|deny|prompt]
Aliases: (none)

Manage trust decisions for external domains and resources.

  /trust                        → List trust decisions
  /trust:list                    → List trust decisions
  /trust:allow:<domain>          → Trust a domain
  /trust:deny:<domain>           → Deny a domain
  /trust:prompt:<domain>         → Prompt for a domain
```

### `/usage` — Show cross-session usage statistics

```
Usage: /usage[:scope]
Aliases: (none)

Show cumulative token usage across all Goa sessions (stored in
`~/.goa/usage.db`). Each model turn is recorded with project, provider, and
model tags.

  /usage or /usage:all      Global totals + per-project/provider/model breakdowns
  /usage:project            Usage grouped by project
  /usage:provider           Usage grouped by provider
  /usage:model              Usage grouped by model
  /usage:here               Usage for the current project only

Examples:
  /usage
  /usage:model
  /usage:here
```

### `/ui` — UI control commands

```
Usage: /ui:action[:args]
Aliases: (none)

Fine-grained UI control actions.

  /ui:theme:set:<token>:<color>   → Change a theme token color
  /ui:pane:show:<id>              → Show a pane
  /ui:pane:hide:<id>              → Hide a pane
  /ui:flash:<message>             → Show a flash message

Examples:
  /ui:theme:set:accent:#ff6600
  /ui:flash:Task completed
```

### `/update` — Check for updates

```
Usage: /update
Aliases: (none)

Check for a newer Goa release.
```

### `/version` — Show version

```
Usage: /version
Aliases: /v, /ver

Show the Goa version number and build information.
```

### `/workflows` — Run multi-stage workflows

```
Usage: /workflows [:list|:show:<name>|:run:<name>|:cancel]
Aliases: (none)

Run multi-stage, multi-agent pipelines with pre-defined roles.
  /workflows:list                              → List available workflows
  /workflows:show:implement-feature            → Show workflow details
  /workflows:run:implement-feature "Add auth"  → Run a workflow with input
  /workflows:implement-feature                 → Shorthand (same as :run:)
  /workflows:cancel                            → Cancel running workflow
```

See [USER-GUIDE.md](USER-GUIDE.md#1-workflows--multi-stage-pipelines) for full documentation.

## Registering a New Command

To add a new command, create a file in `core/commands/`:

```go
package commands

import (
    "fmt"
    "github.com/yourorg/goa/core"
)

func init() {
    core.RegisterCommand(&MyCommand{})
}

type MyCommand struct{}

func (c *MyCommand) Name() string    { return "mycommand" }
func (c *MyCommand) Aliases() []string { return []string{"mc"} }
func (c *MyCommand) ShortHelp() string { return "Do a thing" }
func (c *MyCommand) LongHelp() string {
    return "Detailed documentation for the /mycommand command.\n\nUsage: /mycommand <arg>\n\nExamples:\n  /mycommand foo → does foo\n"
}
func (c *MyCommand) Run(ctx core.Context, args []string) error {
    fmt.Println("Running mycommand with args:", args)
    return nil
}
```

## Command Context

The `Context` passed to `Run()` provides access to all Goa subsystems:

```go
type Context struct {
    Config              *config.Config        // Current config
    AgentManager        *AgentManager         // Agent lifecycle
    SessionStore        *SessionStore         // Session persistence
    ExecutionController *ExecutionController  // Mode management
    ToolRegistry        *tools.ToolRegistry   // Tool access
    ModeRegistry        *core.ModeRegistry    // Mode definitions
    ProviderManager     *provider.ProviderManager
    MemoryStore         *memory.MemoryStore
    SkillRegistry       *skills.SkillRegistry
    DocEngine           *DocEngine
    Commands            *CommandRegistry
    GoalManager         *GoalManager          // Goal tracking
    CronManager         *CronManager          // Scheduled tasks
}
```
