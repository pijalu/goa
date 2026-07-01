<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Tool System

Goa's tool system provides the agent with interfaces to the filesystem, shell, network, and more. Tools implement the [`agentic.Tool`](../internal/agentic/tool.go) interface and are registered in a `ToolRegistry` that wraps the SDK's registry with documentation support.

## Tool Interface

```go
// internal/agentic/tool.go
type Tool interface {
    Schema() ToolSchema
    Execute(input string) (string, error)
}

type ToolSchema struct {
    Name        string
    Description string
    Schema      map[string]interface{}  // JSON Schema
}
```

Goa extends this with the `Documentable` interface for self-documenting tools:

```go
// tools/documentable.go
type Documentable interface {
    ShortDoc() string
    LongDoc() string
    Examples() []ToolExample
}

type ToolExample struct {
    Description string
    Input       string
    Output      string
}
```

## Tool Registry

```go
// tools/registry.go
type ToolRegistry struct {
    tools    map[string]agentic.Tool
    docTools map[string]Documentable
}

func NewToolRegistry() *ToolRegistry
func (r *ToolRegistry) Register(tool agentic.Tool)
func (r *ToolRegistry) Get(name string) (agentic.Tool, bool)
func (r *ToolRegistry) All() []agentic.Tool
func (r *ToolRegistry) Schemas() []agentic.ToolSchema
func (r *ToolRegistry) AllDocumented() []DocumentedTool
```

## Tool Reference

### `read` — Read file contents

Reads files from the project directory (or worktree, if isolated).
A leading `@` expands to the current working directory, and a
`fuzzy_match` fallback finds the closest filename when the exact path does
not exist.

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Path to file (required) |
| `start_line` | number | First line to read, 1-indexed (optional) |
| `end_line` | number | Last line to read, 1-indexed (optional) |
| `max_lines` | number | Max lines (optional, default 500) |
| `max_bytes` | number | Max bytes (optional, default 50000) |

**Example:**
```json
{
  "file_path": "src/main.go",
  "offset": 0,
  "limit": 50
}
```

### `write` — Write file contents

Creates or overwrites a file. A leading `@` expands to the current working
directory, and `fuzzy_match` can resolve a misspelled filename to an existing
file. In review mode, writes are queued for approval.

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Path to file (required) |
| `content` | string | File content (required) |
| `create_dirs` | boolean | Create parent directories (default: true) |

**Example:**
```json
{
  "file_path": "src/hello.go",
  "content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
}
```

### `edit` — Edit a file

Performs targeted search/replace edits on an existing file. Like `read` and
`write`, a leading `@` expands to the current working directory and
`fuzzy_match` resolves misspelled filenames. The search/replace content uses
3-tier fuzzy matching controlled by `tools.edit.allow_fuzz_on_edits`.

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Path to file (required) |
| `old_string` | string | Text to search for (required) |
| `new_string` | string | Replacement text (required) |

**Fuzzy matching (enabled by default via `tools.edit.allow_fuzz_on_edits`):**
| Tier | Strategy | Example |
|------|----------|---------|
| 1 | Exact match (after CRLF normalization) | `old_string` matches byte-for-byte |
| 2 | Trailing whitespace normalized | `"func foo() {  "` matches `"func foo() {"` |
| 3 | Full fuzzy + auto-reindent | Indentation differences are auto-corrected |

When fuzzy matching is disabled (`tools.edit.allow_fuzz_on_edits: false`), only
exact match (tier 1) is attempted.

**Example:**
```json
{
  "path": "src/main.go",
  "old_string": "fmt.Println(\"hello\")",
  "new_string": "fmt.Println(\"world\")"
}
```

### `search` — Search files

Full-text regex search across the project with concurrent file scanning.
Results are ordered by file extension priority (source code first), then by
match count per file (most matches first).

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `pattern` | string | Regex pattern (required) |
| `path` | string | Root directory (default: project root) |
| `glob` | string | File glob filter (e.g. `"*.go"`, `"**/*.test.js"`) |
| `recursive` | boolean | Search subdirectories (default: true) |
| `case_sensitive` | boolean | Case-sensitive search (default: false) |
| `max_results` | number | Max results to show (default: 30) |
| `context_lines` | number | Context lines per match (default: 1) |
| `exclude` | array | Additional directory exclude patterns |

**Glob patterns:** Supports `**` (match any number of directories),
`*` (match filename component), and standard filepath.Match patterns.

**Fuzzy fallback:** When a pattern contains `|` and returns no results,
the tool automatically splits on `|` and searches each term separately.

**Priority ordering:** Built-in `search_priority.json` maps file extensions
to priority tiers. Source code (.go, .py, .rs) sorts first, then config
files (.json, .yaml), then data/doc files (.md, .csv, .html), then media.
Users can override via `~/.goa/search_priority.json`.

**Example:**
```json
{
  "pattern": "func.*Handler",
  "glob": "*.go",
  "max_results": 10
}
```

### `bash` — Execute shell commands

Runs shell commands with security controls (blocked/allowed command
filtering, env masking, output truncation, and an optional project-directory
jail).

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `command` | string | Shell command (required) |
| `timeout` | number | Timeout in seconds (default: 60, max: 300) |
| `workdir` | string | Working directory (default: project root) |
| `env` | object | Extra environment variables (optional) |

**Security:**
- `blocked_commands` — Never execute these (configurable)
- `allowed_commands` — Whitelist (empty = allow all except blocked)
- `env_mask_patterns` — Mask sensitive values in output
- `jail` — In SOLO mode, reject commands that escape the project directory

**Output:** Long output is truncated to the tail and a truncation notice is
included. The full output is saved to a temp file when truncated.

### Output Compression

Tool output compression reduces verbose command output into a compact form
before returning it to the agent. This saves tokens and is especially
beneficial for local models with tighter context windows.

| Command | Compression | Example |
|---------|-------------|---------|
| `ls -la` | Strips permissions/owner/group/size — filenames only | `file.go` vs `-rw-r--r--  user group  1024 Jan 1 file.go` |
| `git status` | One line per changed file | `M  file.go` vs full porcelain |
| `git diff` | Condensed per-file diff with only changed lines | `--- file.go` + changed lines |
| `git log` | Deduplicated, author email stripped | `feat: add foo` vs full commit metadata |
| `grep` / `rg` | Grouped by file, long lines truncated at 200 chars | `file.go:  func foo()` |
| `cat` / `head` / `tail` | Line-numbered output | `    1  package main` |
| `go test` | PASS lines stripped, stack traces compressed, pass/fail summary | `2 passed, 0 failed` |

Compression is **disabled by default** for cloud providers. For **local
providers** (LM Studio, Ollama, and any provider with a localhost endpoint)
it is enabled by default since local models benefit most from token savings.

You can override the default in three ways:

**1. Per-model** (highest precedence) — add `compress_output` to a model definition:
```yaml
models:
  - id: my-local-model
    model: qwen/qwen3.5-9b
    provider: lmstudio
    compress_output: true     # enable for this model
```

**2. Global config** — set `tools.bash.compress_output`:
```yaml
tools:
  bash:
    compress_output: false    # disable globally
```

**3. Provider auto-detect** (default) — local providers (lm-studio, ollama,
endpoints with localhost/127.0.0.1) get compression enabled automatically;
remote providers get it disabled.

**Example:**
```json
{
  "command": "go build ./...",
  "timeout": 60
}
```

### `ssh_bash` — Execute commands on remote hosts

Runs shell commands on SSH hosts using the system `ssh` binary.

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `host` | string | Host ID from config (required) |
| `command` | string | Command to run (required) |
| `timeout` | number | Timeout in seconds (optional) |

**Example:**
```json
{
  "host": "server1",
  "command": "systemctl status app",
  "timeout": 30
}
```

### `bg_exec` — Background process execution

Starts a background process with pipe I/O (stdin/stdout/stderr).

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `command` | string | Command to run (required) |
| `args` | array | Command arguments (optional) |
| `stdin` | string | Stdin content (optional) |
| `timeout` | number | Timeout in seconds (optional) |

**Example:**
```json
{
  "command": "npm",
  "args": ["test"],
  "timeout": 120
}
```

### `memento` — Read/write thinking artifacts

Persists agent thoughts as markdown files in `.goa/memory/`. The agent can
also read the full content of memory files via `read` when memory summaries
are injected into the system prompt.

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `action` | string | `read` | `write` | `append` | `list` | `delete` (required) |
| `name` | string | Memory name (required for read/write/append/delete) |
| `content` | string | Content (required for write/append) |

**Example:**
```json
{
  "action": "write",
  "name": "architecture-notes",
  "content": "## Database Schema\n\nThe users table needs a unique constraint on email."
}
```

### `goa_command` — Execute Goa commands from LLM

Allows the LLM to invoke Goa commands programmatically.

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `command` | string | Command string (e.g., `mode confirm`) (required) |

**Example:**
```json
{
  "command": "mode confirm"
}
```

### `ask_user_question` — Ask clarifying questions

Lets the LLM ask the user one or more clarifying questions when requirements
are ambiguous. Each question is shown as a card in the conversation
(title / summary / question / numbered options) and answered through the
**main input line**; the card never captures input. Registered by default;
disable with `tools.enabled.clarify_disabled: true`.

**Parameters:**
| Field | Type | Description |
|-------|------|-------------|
| `questions` | array | One or more questions; each asked separately (required) |
| &nbsp;&nbsp;`question` | string | The question (required) |
| &nbsp;&nbsp;`title` | string | Short label for the card / input title |
| &nbsp;&nbsp;`summary` | string | Optional context |
| &nbsp;&nbsp;`options` | string[] | Up to 6 choices (type number or text) |
| &nbsp;&nbsp;`required` | bool | If true, cancellation is an error |
| &nbsp;&nbsp;`allow_free_text` | bool | If false with options, restrict to listed options |

**Example:**
```json
{
  "questions": [
    {
      "title": "Target branch",
      "summary": "Two release branches are active",
      "question": "Which branch should I target?",
      "options": ["main", "release-2.x"]
    }
  ]
}
```

## Documentable Tool Update

To make a tool self-documenting, implement the `Documentable` interface:

```go
type MyTool struct{}

func (t *MyTool) Schema() agentic.ToolSchema { ... }
func (t *MyTool) Execute(input string) (string, error) { ... }

// Documentable interface
func (t *MyTool) ShortDoc() string { return "Does something useful" }
func (t *MyTool) LongDoc() string  { return "Detailed explanation..." }
func (t *MyTool) Examples() []ToolExample {
    return []ToolExample{
        {Description: "Basic usage", Input: `{"key": "value"}`, Output: "result"},
    }
}
```

## Tool Registration

Tools are registered in `main.go`:

```go
func registerTools(reg *tools.ToolRegistry, wm *internal.WorktreeManager, projectDir string, cfg *config.Config) {
    gitStager := tools.NewGitStager(projectDir)
    reg.Register(&tools.ReadFileTool{WorktreeMgr: wm})
    reg.Register(&tools.WriteFileTool{WorktreeMgr: wm, ProjectDir: projectDir, GitStager: gitStager})
    reg.Register(&tools.EditFileTool{WorktreeMgr: wm, ProjectDir: projectDir, GitStager: gitStager})
    reg.Register(&tools.SearchTool{WorktreeMgr: wm, Threads: cfg.Tools.Search.Threads, ...})
    reg.Register(&tools.BashTool{WorktreeMgr: wm, Blocked: cfg.Tools.Bash.BlockedCommands, ...})
    reg.Register(&tools.SSHBashTool{Hosts: sshHosts(cfg)})
    reg.Register(tools.NewBGExecTool())
    reg.Register(&tools.MementoTool{ProjectDir: projectDir, GlobalDir: cfg.ConfigDir})
}
```

## Concurrent Tool Execution

Goa supports concurrent execution of non-conflicting tool calls. When the LLM
issues multiple tool calls in a single turn, tools with independent resource
accesses run in parallel, while conflicting tools are serialized.

### Resource Access Declaration

Each tool implements the `Accessor` interface to declare what resources it
accesses:

```go
type Accessor interface {
    Access(input string) toolaccess.Access
}

type Access struct {
    ReadPaths  []string  // file paths this tool reads
    WritePaths []string  // file paths this tool writes to
    Category   string    // "shell", "network", "memory" for broad conflict
}
```

### Conflict Rules

| Scenario | Executes |
|----------|----------|
| Two reads on different files | ✅ Parallel |
| Two reads on the same file | ✅ Parallel (reads are safe) |
| Read + write on different files | ✅ Parallel |
| Read + write on the same file | ❌ Serialized |
| Two writes on the same file | ❌ Serialized |
| Two shell commands | ❌ Serialized (same category) |
| Shell + file read | ✅ Parallel (different categories) |

### Scheduling

The `ToolScheduler` dispatches non-conflicting tools in goroutines and queues
conflicting ones until their dependencies complete. Results are returned in
provider order (the order the LLM issued them), regardless of completion order.

This is transparent to the LLM — it sees the same tool result sequence as
sequential execution, but with lower latency for multi-tool turns.
```
