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

### `/cron` — Manage scheduled tasks

```
Usage: /cron <subcommand> [args]
Subcommands: create, list, delete, pause, resume
```

Run agent tasks on a schedule. Schedules use standard 5-field cron syntax
or shortcuts (@daily, @weekly, @every <duration>).

```
/cron create "0 9 * * 1" "Review code quality"   → Weekly Monday 9AM
/cron create "@daily" "Check for dependency updates" → Every midnight
/cron create "@every 30m" "Run health checks"      → Every 30 minutes
/cron list                                            → List all jobs
/cron delete cron-1                                   → Delete job
/cron pause cron-1                                    → Pause without deleting
/cron resume cron-1                                   → Re-enable
```

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
  /tools:search:pattern=TODO       → Execute a tool directly
  /tools:search:glob=*.go,pattern=test  → With multiple params

Tool execution runs the tool immediately and displays output in the
conversation (not sent to the model). Parameters use comma-separated
key=value syntax. Supported types:
  strings:    pattern=test
  booleans:   case_sensitive=true, recursive=false
  integers:   max_results=50, context_lines=3
```

### `/plugins` — Plugin management

```
Usage: /plugins [list|reload]
Aliases: (none)

Manage JS plugins.
  /plugins        → List all loaded plugins
  /plugins reload → Reload all plugins from disk
```

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
