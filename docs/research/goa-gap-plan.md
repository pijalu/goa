# Implementation Plan: Closing Goa's Feature Gaps vs Zero

> **Status:** Planning | **Target:** Goa v0.1 codebase
> **Prerequisite reading:** [goa-gap.md](goa-gap.md) — gap analysis document

---

## Table of Contents

1. [Lifecycle Hooks System](#1-lifecycle-hooks-system)
2. [MCP HTTP Client Transport](#2-mcp-http-client-transport)
3. [MCP Server CLI (`goa serve --mcp`)](#3-mcp-server-cli)
4. [Plugin Bundling (JSON manifest)](#4-plugin-bundling)
5. [Plugin-Declared Skills](#5-plugin-declared-skills)
6. [User-Configurable Hooks JSON](#6-user-configurable-hooks-json)
7. [Audit Log for Lifecycle Events](#7-audit-log)
8. [Specialist Management CLI](#8-specialist-management-cli)

---

## 1. Lifecycle Hooks System

**Priority:** P0 | **Effort:** Medium | **Package:** `internal/hooks/`

### Overview

Create a shell-command hook system that fires on lifecycle events. Users configure hooks in a JSON file. Each hook runs a shell command with the event payload delivered as JSON on stdin. Exit code 0 continues; non-zero blocks the tool or surfaces an error.

### Data Model

```go
// internal/hooks/hooks.go

package hooks

import "encoding/json"

// EventType identifies the lifecycle event that triggers a hook.
type EventType string

const (
    EventBeforeTool      EventType = "beforeTool"
    EventAfterTool       EventType = "afterTool"
    EventSessionStart    EventType = "sessionStart"
    EventSessionEnd      EventType = "sessionEnd"
    EventSpecialistStart EventType = "specialistStart"
    EventSpecialistStop  EventType = "specialistStop"
)

// HookConfig describes a single hook from the JSON config file.
type HookConfig struct {
    ID      string    `json:"id"`
    Event   EventType `json:"event"`
    Matcher string    `json:"matcher,omitempty"` // tool name glob or specialist name
    Command string    `json:"command"`
    Args    []string  `json:"args,omitempty"`
    Enabled bool      `json:"enabled"`
}

// HooksConfig is the top-level hooks configuration.
type HooksConfig struct {
    Enabled bool        `json:"enabled"`
    Hooks   []HookConfig `json:"hooks"`
}

// HookPayload is the JSON delivered to the hook command on stdin.
type HookPayload struct {
    Event       EventType        `json:"event"`
    Matcher     string           `json:"matcher,omitempty"`
    ToolCallID  string           `json:"toolCallId,omitempty"`
    ToolName    string           `json:"toolName,omitempty"`
    ToolInput   string           `json:"toolInput,omitempty"`
    ToolOutput  string           `json:"toolOutput,omitempty"`
    Status      string           `json:"status,omitempty"` // "success", "error", "blocked"
    Specialist  string           `json:"specialist,omitempty"`
    SessionID   string           `json:"sessionId,omitempty"`
    ProjectDir  string           `json:"projectDir,omitempty"`
    Timestamp   string           `json:"timestamp,omitempty"`
}
```

### Core Engine

```go
// internal/hooks/engine.go

package hooks

// Engine manages hook configuration and dispatch.
type Engine struct {
    config     HooksConfig
    configDirs []string // [project, home] directories to search for hooks.json
    auditLog   *AuditLog  // nil if audit logging is disabled (see §7)
}

// NewEngine creates a hook engine, loading config from the given directories.
// Directories are searched in order: project dir first, home dir second.
// Results are merged (project overrides home on ID collision).
func NewEngine(configDirs []string) (*Engine, error)

// Dispatch fires hooks matching the given event and optional matcher.
// It returns an error if any hook exits non-zero (for beforeTool events, this
// blocks the tool). For afterTool events, the error is surfaced to the agent.
func (e *Engine) Dispatch(ctx context.Context, event EventType, payload HookPayload) error

// Reload re-reads hooks.json from config directories.
func (e *Engine) Reload() error
```

### Integration Points

| Integration | File | What to change |
|---|---|---|
| Config loading | `config/config.go` | Add `HooksDir` to config path, or add explicit hooks config section to config.yaml |
| Config paths | New file | Add `hooks.json` file paths: `.goa/hooks.json` and `~/.goa/hooks.json` |
| Tool execution | `internal/agentic/loop.go` or `internal/agentic/tool.go` | Call `hooks.Engine.Dispatch(EventBeforeTool, ...)` before tool execute, check return before running |
| Tool execution | Same file | Call `hooks.Engine.Dispatch(EventAfterTool, ...)` after tool execute |
| Session start | `core/agentmanager.go` or `internal/app/subsystems.go` | Call `hooks.Engine.Dispatch(EventSessionStart, ...)` when session begins |
| Session end | Same file | Call `hooks.Engine.Dispatch(EventSessionEnd, ...)` when session ends |
| Specialist spawn | `internal/agentic/agent.go` or `core/agentmanager.go` | Call `hooks.Engine.Dispatch(EventSpecialistStart/Stop, ...)` when sub-agents start/stop |
| Subsystem init | `internal/app/subsystems.go` | Create `hooks.Engine` and wire into agent manager |

### Implementation Steps

1. **Create `internal/hooks/` package** with types, config loader, dispatch engine
2. **Implement Engine.Dispatch()** that:
   - Filters hooks by event type and matcher (if set)
   - Skips disabled hooks
   - Runs each hook's command via `os/exec` with stdin payload
   - Captures stdout/stderr, exit code
   - For `beforeTool`: returns error on non-zero exit (blocking the tool)
   - For `afterTool`/others: logs error but does not block (non-zero exit surfaces as warning)
   - Records execution in audit log (if configured)
3. **Add hook config loading** to `config/` package or as standalone loader
4. **Wire into tool execution** — modify `Tool.Execute()` path to call `Dispatch` before/after
5. **Wire into session lifecycle** — call `Dispatch` at session start/end in `core.AgentManager`
6. **Wire into sub-agent lifecycle** — call `Dispatch` at specialist start/stop

### Testing

| Test | Scope |
|---|---|
| Unit: Hook dispatch filtering | `internal/hooks/engine_test.go` |
| Unit: Command execution with stdin payload | `internal/hooks/engine_test.go` |
| Unit: Exit code handling (0 vs non-zero) | `internal/hooks/engine_test.go` |
| Unit: Config loading from multiple dirs | `internal/hooks/hooks_test.go` |
| Integration: Tool blocked by beforeTool hook | `internal/app/hooks_integration_test.go` |
| Integration: Session hooks fire correctly | `internal/app/hooks_integration_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `internal/hooks/hooks.go` | Create | Types, constants, payload |
| `internal/hooks/engine.go` | Create | Core dispatch engine |
| `internal/hooks/engine_test.go` | Create | Unit tests |
| `config/config.go` | Modify | Add hooks config paths |
| `internal/app/subsystems.go` | Modify | Wire hooks engine |
| `core/agentmanager_lifecycle.go` | Modify | Add hooks dispatch calls |
| `internal/agentic/tool.go` | Modify | Add before/after tool hooks |

---

## 2. MCP HTTP Client Transport

**Priority:** P0 | **Effort:** Low-Medium | **Package:** `internal/mcp/client/`

### Overview

Add HTTP/HTTPS transport to the MCP client, enabling connection to remote MCP servers. Extend `ServerConfig` with a `type` field, `url`, and `headers`.

### Data Model Changes

```go
// internal/mcp/config.go — extend ServerConfig

type ServerConfig struct {
    Name    string            `json:"name"`
    Type    string            `json:"type"` // "stdio" (default) or "http"
    Command string            `json:"command,omitempty"`  // used for stdio
    Args    []string          `json:"args,omitempty"`     // used for stdio
    URL     string            `json:"url,omitempty"`      // used for http
    Headers map[string]string `json:"headers,omitempty"`  // used for http
}
```

### Transport Implementation

```go
// internal/mcp/client/http.go

package client

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
)

// HTTPClient connects to an MCP server over HTTP/HTTPS using JSON-RPC 2.0
// over HTTP POST requests. Each RPC call is a single HTTP request/response
// pair (no long-lived connection, no reader goroutine, no multiplexing).
type HTTPClient struct {
    baseURL string
    headers map[string]string
    client  *http.Client
}

// NewHTTPClient creates an HTTP MCP client.
func NewHTTPClient(baseURL string, headers map[string]string) *HTTPClient

func (c *HTTPClient) Initialize(ctx context.Context) error {
    // Send POST to baseURL with initialize JSON-RPC request
    // Verify response protocol version
}

func (c *HTTPClient) ListTools(ctx context.Context) ([]ToolInfo, error)
func (c *HTTPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error)
func (c *HTTPClient) Close() error
```

### Client Factory Changes

```go
// internal/mcp/manager.go — update defaultFactory

func defaultFactory(cfg ServerConfig) (client.Client, error) {
    switch cfg.Type {
    case "http", "https":
        c := client.NewHTTPClient(cfg.URL, cfg.Headers)
        if err := c.Initialize(context.Background()); err != nil {
            return nil, err
        }
        return c, nil
    default: // "stdio" or empty
        c := client.NewStdioClient(cfg.Command, cfg.Args)
        if err := c.Initialize(context.Background()); err != nil {
            return nil, err
        }
        return c, nil
    }
}
```

### Implementation Steps

1. **Create `internal/mcp/client/http.go`** with `HTTPClient` implementing `Client` interface
2. **Extend `ServerConfig`** in `internal/mcp/config.go` — add `Type`, `URL`, `Headers` fields
3. **Update `defaultFactory`** in `internal/mcp/manager.go` to handle HTTP transport
4. **Update `ConfigPaths`** or config loading to support the new fields
5. **Migrate existing config format** — `ServerConfig` used to be just `{name, command, args}`, now needs the new fields. The `Type` field defaults to `"stdio"` for backwards compatibility.

### Testing

| Test | Scope |
|---|---|
| Unit: HTTP client initialize handshake | `internal/mcp/client/http_test.go` |
| Unit: HTTP client ListTools/CallTool | `internal/mcp/client/http_test.go` |
| Unit: HTTP client with custom headers | `internal/mcp/client/http_test.go` |
| Unit: ServerConfig type routing | `internal/mcp/manager_test.go` |
| Integration: Connect to remote MCP server | `internal/mcp/mcp_integration_test.go` |
| Integration: Config backwards compatibility | `internal/mcp/config_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `internal/mcp/client/http.go` | Create | HTTP MCP client transport |
| `internal/mcp/client/http_test.go` | Create | Unit tests |
| `internal/mcp/config.go` | Modify | Extend ServerConfig |
| `internal/mcp/manager.go` | Modify | Update factory dispatch |
| `internal/mcp/manager_test.go` | Modify | Test HTTP routing |

---

## 3. MCP Server CLI

**Priority:** P2 | **Effort:** Low | **Package:** `cmd/goa/` + `internal/app/`

### Overview

Expose Goa's tools as an MCP server over stdio, accessible via `goa serve --mcp`. Uses the existing `MCPToolPublisher` from `internal/agentic/mcp/publisher.go`.

### CLI Integration

```go
// internal/app/mcp_serve.go (new file)

package app

// RunMCPServe starts an MCP stdio server that exposes Goa's tool registry.
// It reads JSON-RPC 2.0 requests from stdin and writes responses to stdout.
func RunMCPServe(toolRegistry *tools.ToolRegistry) error {
    publisher := mcp.NewMCPToolPublisher(toolRegistry)
    scanner := bufio.NewScanner(os.Stdin)
    for scanner.Scan() {
        line := scanner.Bytes()
        if len(line) == 0 {
            continue
        }
        var req mcp.JSONRPCRequest
        if err := json.Unmarshal(line, &req); err != nil {
            continue
        }
        resp := publisher.HandleRequest(req)
        respData, _ := json.Marshal(resp)
        fmt.Println(string(respData))
    }
    return scanner.Err()
}
```

### Flag Registration

```go
// internal/app/bootstrap.go — add to ParseCLIFlags or RuntimeOptions

type RuntimeOptions struct {
    // ... existing fields ...
    ServeMCP bool // new: run as MCP server over stdio
}
```

Add a `--mcp` flag:
```go
flag.BoolVar(&ro.ServeMCP, "mcp", false, "Run as MCP stdio server exposing tools")
```

### Main Dispatch

```go
// internal/app/app.go — in runApp(), before the main switch:

if runtimeOpts.ServeMCP {
    if err := RunMCPServe(subs.toolRegistry); err != nil {
        fmt.Fprintf(os.Stderr, "MCP server error: %v
", err)
    }
    return false
}
```

### Implementation Steps

1. **Add `ServeMCP` bool** to `RuntimeOptions`
2. **Add `--mcp` flag** in `ParseCLIFlags()`
3. **Add early-exit branch** in `runApp()` before TUI initialization
4. **Create `internal/app/mcp_serve.go`** with `RunMCPServe()`
5. **Test** with another agent connecting via stdio MCP

### Risks

- Goa starts slower than Zero when used as a server; consider lazy initialization
- Tool registry must be fully populated before `goa serve --mcp` runs — verify all tools (including plugin-registered) are available

### Testing

| Test | Scope |
|---|---|
| Unit: MCP server echoes tools/list | `internal/app/mcp_serve_test.go` |
| Unit: MCP server executes tools/call | `internal/app/mcp_serve_test.go` |
| E2E: `goa serve --mcp` outputs valid JSON-RPC | `cmd/goa/e2e_mcp_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `internal/app/mcp_serve.go` | Create | MCP server loop |
| `internal/app/mcp_serve_test.go` | Create | Unit tests |
| `internal/app/bootstrap.go` | Modify | Add `ServeMCP` flag |
| `internal/app/app.go` | Modify | Dispatch to serve mode |

---

## 4. Plugin Bundling (JSON manifest)

**Priority:** P1 | **Effort:** Medium | **Package:** `plugins/` + new `plugins/manifest/`

### Overview

Support a JSON-based plugin manifest format (`plugin.json`) that declares shell-executable tools, shell hooks, and skill files — without requiring JavaScript. This complements the existing JS plugin system with a lighter-weight alternative.

### Data Model

```go
// plugins/manifest.go

package plugins

// ManifestPlugin is a compiled-in type (parsed from plugin.json).
type ManifestPlugin struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Description string            `json:"description"`
    Version     string            `json:"version"`
    Tools       []ManifestTool    `json:"tools,omitempty"`
    Hooks       []ManifestHook    `json:"hooks,omitempty"`
    Skills      []ManifestSkill   `json:"skills,omitempty"`
    Dir         string            `json:"-"` // resolved plugin directory
}

// ManifestTool describes a shell-executable tool.
type ManifestTool struct {
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty"`
    Command     string   `json:"command"` // relative path from plugin dir
    Args        []string `json:"args,omitempty"`
}

// ManifestHook describes a shell hook (same schema as hooks.json).
type ManifestHook struct {
    ID      string `json:"id"`
    Event   string `json:"event"`
    Matcher string `json:"matcher,omitempty"`
    Command string `json:"command"` // relative path from plugin dir
}

// ManifestSkill describes a skill file bundled in the plugin.
type ManifestSkill struct {
    Path string `json:"path"` // relative path to SKILL.md from plugin dir
}
```

### Shell Tool Adapter

```go
// plugins/shell_tool.go

// ShellTool adapts a shell command to the agentic.Tool interface.
type ShellTool struct {
    name    string
    desc    string
    command string
    args    []string
    workDir string // plugin directory
}

func (t *ShellTool) Schema() agentic.ToolSchema { ... }
func (t *ShellTool) Execute(input string) (string, error) {
    // Parse input as JSON args, pass to shell command as environment or stdin
    cmd := exec.Command(t.command, t.args...)
    cmd.Dir = t.workDir
    cmd.Stdin = strings.NewReader(input)
    out, err := cmd.Output()
    return string(out), err
}
func (t *ShellTool) IsRetryable(err error) bool { return false }
```

### Plugin Loader Extension

```go
// plugins/loader.go — extend the plugin loading pipeline

// LoadManifestPlugins discovers and loads all manifest plugins from dirs.
func LoadManifestPlugins(dirs []string) ([]*ManifestPlugin, error)

// loadManifestPlugin reads a single plugin.json from a directory.
func loadManifestPlugin(dir string) (*ManifestPlugin, error)
```

### Implementation Steps

1. **Define types** in `plugins/manifest.go` — `ManifestPlugin`, `ManifestTool`, etc.
2. **Create `ShellTool` adapter** in `plugins/shell_tool.go`
3. **Implement manifest loader** that walks plugin dirs, reads `plugin.json`, validates fields
4. **Integrate with tool registration** — when a manifest plugin declares tools, register them via `ToolRegistry`
5. **Integrate with hooks engine** — when a manifest plugin declares hooks, feed them into the hooks engine (see §6)
6. **Integrate with skill loader** — when a manifest plugin declares skills, feed them into `SkillRegistry` (see §5)
7. **Update `initPlugins()`** in `internal/app/subsystems.go` or similar to also load manifest plugins

### Design Decisions

- Manifest plugins live in the same directories as JS plugins: `~/.goa/plugins/<id>/` and `.goa/plugins/<id>/`
- If both `plugin.json` and `plugin.yaml` exist in a directory, `plugin.json` takes precedence (new behavior), `plugin.yaml` falls back to JS loading
- Shell tool commands are resolved relative to the plugin directory
- Hooks declared in manifest plugins feed into the same hook engine as user-configured hooks (see §6)

### Testing

| Test | Scope |
|---|---|
| Unit: Manifest plugin parsing | `plugins/manifest_test.go` |
| Unit: Shell tool execution | `plugins/shell_tool_test.go` |
| Unit: Manifest discovery in dirs | `plugins/loader_test.go` (extend) |
| Unit: Tool registry integration | `plugins/manifest_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `plugins/manifest.go` | Create | Manifest types & parser |
| `plugins/manifest_test.go` | Create | Tests |
| `plugins/shell_tool.go` | Create | Shell command tool adapter |
| `plugins/shell_tool_test.go` | Create | Tests |
| `plugins/loader.go` | Modify | Add manifest loading path |
| `internal/app/subsystems.go` | Modify | Wire manifest plugins |

---

## 5. Plugin-Declared Skills

**Priority:** P2 | **Effort:** Low-Medium | **Package:** `plugins/` + `skills/`

### Overview

Allow plugins (both JS and manifest plugins) to declare skills that are merged into the `SkillRegistry`. This lets plugins bundle instructional content alongside their tools.

### Integration Points

```go
// plugins/plugin.go — extend PluginContext

type PluginContext struct {
    // ... existing fields ...
    RegisterSkill func(name, path string) error // new: JS-callable goa.registerSkill
}
```

```go
// skills/loader.go — add registration API

// Register adds a skill directly to the registry (bypasses filesystem loading).
// Used by plugins to contribute skills.
func (r *SkillRegistry) Register(skill *Skill) error
```

### JS Plugin API

```javascript
// In plugin.js:
goa.registerSkill("review-checklist", "./skills/review-checklist/SKILL.md");
```

The `RegisterSkill` handler in `PluginContext` reads the SKILL.md from the plugin directory and calls `SkillRegistry.Register()`.

### Manifest Plugin Integration

The manifest plugin's `skills` array (§4) is loaded by the manifest plugin loader and registered into the `SkillRegistry` automatically.

### Implementation Steps

1. **Add `RegisterSkill` to `PluginContext`** in `plugins/plugin.go`
2. **Implement JS bridge wrapper** `wrapRegisterSkill()` in `plugins/plugin.go`
3. **Add `Register(skill *Skill)` method** to `skills.SkillRegistry`
4. **Wire the handler** in the plugin initialization code (`internal/app/subsystems.go`) — the handler reads the SKILL.md file from the plugin directory, parses it via `skills.ParseSkill()`, and registers it
5. **For manifest plugins**, call `SkillRegistry.Register()` directly from the manifest loader

### Edge Cases

- Skill name collisions: plugin-declared skills use plugin prefix or plugin ID to scope names
- De-duplication: if two plugins declare the same skill name, the last one loaded wins (consistent with existing skill loading behavior)
- Unloading: when a plugin is removed, its skills should be removed from the registry (add `Unregister(name string)` to `SkillRegistry`)

### Testing

| Test | Scope |
|---|---|
| Unit: JS goa.registerSkill works | `plugins/plugin_test.go` |
| Unit: SkillRegistry.Register makes skill discoverable | `skills/loader_test.go` |
| Unit: Skill removal on plugin unload | `skills/loader_test.go` |
| Integration: Plugin skill appears in agent context | `internal/app/skill_plugin_integration_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `plugins/plugin.go` | Modify | Add RegisterSkill, JS bridge |
| `skills/loader.go` | Modify | Add Register/Unregister methods |
| `plugins/plugin_test.go` | Modify | Test JS skill registration |
| `skills/loader_test.go` | Modify | Test Registry.Register |

---

## 6. User-Configurable Hooks JSON

**Priority:** P2 | **Effort:** Low | **Package:** `internal/hooks/` + `config/`

### Overview

Provide a standalone `hooks.json` configuration file format that users can edit directly. This is the configuration layer for the hooks engine (§1). The hooks engine is the core; this feature is the file-format user interface.

### Configuration Paths

```go
// internal/hooks/config.go

package hooks

// ConfigPaths returns candidate hooks.json paths in priority order.
func ConfigPaths(projectDir, configDir string) []string {
    return []string{
        filepath.Join(projectDir, ".goa", "hooks.json"),  // project-level
        filepath.Join(configDir, "hooks.json"),            // user-level
    }
}

// LoadConfig loads and merges hooks.json files from all paths.
// Project-level hooks override user-level hooks on ID collision.
// The enabled flag is OR'd: if either config enables hooks, they run.
func LoadConfig(paths []string) (*HooksConfig, error)
```

### Config Loading Details

```go
// Merge semantics:
// 1. Load user-level hooks.json (lowest priority)
// 2. Load project-level hooks.json
// 3. For each hook in project config:
//    - If ID matches an existing hook in user config, replace it
//    - Otherwise append
// 4. enabled = user.enabled || project.enabled
```

### Integration with Hooks Engine

The hooks engine constructor takes the merged `HooksConfig` as input. Config paths are provided at engine creation time.

### Implementation Steps

1. **Create `internal/hooks/config.go`** with `ConfigPaths()` and `LoadConfig()`
2. **Call `ConfigPaths()` and `LoadConfig()`** when creating the `Engine` in `NewEngine()`
3. **Add `Reload()` method** to `Engine` that re-reads `hooks.json`
4. **Wire the reload** into the existing `/reload` command's `ReloadHandler`

### Testing

| Test | Scope |
|---|---|
| Unit: LoadConfig from single path | `internal/hooks/config_test.go` |
| Unit: LoadConfig merge user + project | `internal/hooks/config_test.go` |
| Unit: Project hook overrides user hook | `internal/hooks/config_test.go` |
| Unit: Missing hooks.json is not an error | `internal/hooks/config_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `internal/hooks/config.go` | Create | Config paths, loader, merge logic |
| `internal/hooks/config_test.go` | Create | Tests |
| `internal/hooks/engine.go` | Modify | Accept config from LoadConfig |

---

## 7. Audit Log for Lifecycle Events

**Priority:** P1 | **Effort:** Medium | **Package:** `internal/hooks/audit.go`

### Overview

Add a structured audit log that records every hook execution: which hook ran, what event triggered it, what command was executed, the exit code, the tool name/input/output, and a timestamp. The log is append-only, stored as newline-delimited JSON (JSONL) in `~/.goa/audit.jsonl`.

### Data Model

```go
// internal/hooks/audit.go

package hooks

import "time"

// AuditEntry records a single hook execution.
type AuditEntry struct {
    Timestamp   time.Time `json:"timestamp"`
    HookID      string    `json:"hookId"`
    Event       EventType `json:"event"`
    Matcher     string    `json:"matcher,omitempty"`
    Command     string    `json:"command"`
    ExitCode    int       `json:"exitCode"`
    ToolName    string    `json:"toolName,omitempty"`
    ToolInput   string    `json:"toolInput,omitempty"`
    ToolOutput  string    `json:"toolOutput,omitempty"`
    Duration    string    `json:"duration,omitempty"` // human-readable
    Error       string    `json:"error,omitempty"`    // non-empty if hook failed
    SessionID   string    `json:"sessionId,omitempty"`
}

// AuditLog writes structured entries to a JSONL file.
type AuditLog struct {
    path   string
    writer *JSONLWriter
    mu     sync.Mutex
}

// NewAuditLog creates or appends to the given JSONL file.
func NewAuditLog(path string) (*AuditLog, error)

// Record appends an audit entry.
func (l *AuditLog) Record(entry AuditEntry) error

// Reader returns an iterator over existing entries.
func (l *AuditLog) Reader() (*AuditReader, error)
```

### Integration Points

| Where | What |
|---|---|
| `hooks.Engine.Dispatch()` | After each hook runs, call `auditLog.Record(entry)` |
| Engine constructor | Accept optional `*AuditLog` parameter |
| `/audit` command (future) | Read and display audit entries |
| Session end hook | Flush audit log, record session summary |

### Agent Visibility

The audit log should be reachable from the agent's view of past actions (as Zero describes). This means either:

1. The agent can read `audit.jsonl` via the `read` tool (simplest, always available)
2. A dedicated `/audit` command displays recent entries in the TUI
3. After a blocked tool call, the blocking hook's error message is included in the `ToolError` response to the agent

**Recommendation:** Option 1 + 3. The `audit.jsonl` path is in `~/.goa/`, which is readable by the `read` tool. The agent can inspect it when needed. Option 3 ensures immediate feedback on blocked calls.

### Implementation Steps

1. **Create `internal/hooks/audit.go`** with `AuditEntry`, `AuditLog`, `AuditReader`
2. **Add `Record()` call** in `Engine.Dispatch()` after each hook execution
3. **Create audit log path** at engine creation (`~/.goa/audit.jsonl` by default)
4. **When a hook blocks a tool**, include the hook's exit code and stderr in the `ToolError` returned to the agent

### Log Rotation

Since JSONL grows unbounded:

- Rotate at 10 MB: truncate to last 1000 entries
- Respect `$GOA_AUDIT_MAX_MB` (default: 10)
- Implement in `AuditLog.Record()`: check file size before writing

### Testing

| Test | Scope |
|---|---|
| Unit: AuditLog.Record appends JSONL | `internal/hooks/audit_test.go` |
| Unit: AuditLog.Reader reads entries | `internal/hooks/audit_test.go` |
| Unit: Log rotation at size limit | `internal/hooks/audit_test.go` |
| Unit: Blocked tool includes hook error | `internal/hooks/engine_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `internal/hooks/audit.go` | Create | Audit types, log, reader |
| `internal/hooks/audit_test.go` | Create | Tests |
| `internal/hooks/engine.go` | Modify | Wire audit log |

---

## 8. Specialist Management CLI

**Priority:** P1 | **Effort:** Medium | **Package:** `internal/app/` + new `core/specialist/`

### Overview

Add lightweight "specialist" sub-agents — markdown files with YAML frontmatter and system prompts, managed via CLI. Unlike Goa's existing custom modes (which require directory structures and guard rules), specialists are single-file definitions discoverable from scoped directories.

### Specialist Format

```markdown
---
description: Reviews API changes for breaking-change risk and missing tests.
tools: read-only,plan
---

You review API changes. For every changed hunk in `internal/api/` or any file
that ends in `_api.go`:

1. Confirm the public signature is backward-compatible, or note the breaking
   change explicitly with the migration path.
2. Confirm a corresponding test exists in `internal/api/*_test.go` and that
   the new behaviour is exercised.
3. Flag any new exported symbol without a doc comment.

Reply with one JSON object per finding: `{"file", "line", "severity", "message", "fix"}`.
```

### Data Model

```go
// core/specialist/specialist.go

package specialist

// Meta contains the parsed YAML frontmatter.
type Meta struct {
    Description string   `yaml:"description"`
    Tools       []string `yaml:"tools"` // allowed tool scopes
}

// Specialist represents a loaded specialist definition.
type Specialist struct {
    Name    string
    Meta    Meta
    Prompt  string // body after frontmatter
    Source  string // "builtin", "user", "project"
    Dir     string // directory the file was loaded from
}

// Registry manages specialist discovery and loading.
type Registry struct {
    specialists map[string]*Specialist
    dirs        []string // discovery directories
}

// Discovery directories (in priority order):
//   1. compiled-in specialists (embedded FS)
//   2. ~/.goa/specialists/ (user)
//   3. .goa/specialists/ (project)
```

### CLI Commands

```go
// internal/commands/specialist.go

// SpecialistCLI implements the `goa specialist` subcommand set.
type SpecialistCLI struct {
    Registry *specialist.Registry
}

func (c *SpecialistCLI) List() ([]Specialist, error)
func (c *SpecialistCLI) Show(name string) (*Specialist, error)
func (c *SpecialistCLI) Create(name string, meta specialist.Meta, prompt string, project bool) error
func (c *SpecialistCLI) Edit(name string, project bool) error
func (c *SpecialistCLI) Delete(name string, project bool) error
func (c *SpecialistCLI) Path() string // prints resolved specialists directory
```

### CLI Flag Integration

Add to `ParseCLIFlags()` a new `--specialist-*` family, or better, create a top-level `goa specialist` subcommand. Since Goa uses `flag` package currently, the cleanest approach is:

```go
// In ParseCLIFlags(), add:

if os.Args[0] == "specialist" || (len(os.Args) > 1 && os.Args[1] == "specialist") {
    runSpecialistCLI(os.Args[2:])
    return
}
```

Alternatively, as Goa matures, migrate to a proper subcommand-based CLI (cobra/kingpin). For now, a simple `flag`-based dispatch in `runApp()` works:

```go
// internal/app/bootstrap.go

type RuntimeOptions struct {
    // ... existing ...
    SpecialistMode bool   // running as "specialist" subcommand
    SpecialistOp   string // list|show|create|edit|delete|path
    SpecialistName string
    SpecialistProject bool
    SpecialistDescription string
    SpecialistTools string // comma-separated
    SpecialistPrompt string
}
```

### Integration with Agent

When the agent selects a specialist via `/specialist <name>`, the agent switch to that specialist's system prompt and tool scope. This is similar to `/mode` but lighter:

- `/specialist list` — shows available specialists
- `/specialist <name>` — activates the specialist's prompt as a system prompt overlay
- `/specialist stop` — returns to the base mode

### Implementation Steps

1. **Create `core/specialist/specialist.go`** with types and registry
2. **Implement `Registry.LoadAll()`** scanning embedded/user/project directories
3. **Create `core/specialist/parser.go`** for markdown frontmatter parsing (reuse pattern from `skills/loader.go`)
4. **Create CLI handler** in `internal/app/specialist_cli.go`
5. **Add flags to `RuntimeOptions`** in `internal/app/bootstrap.go`
6. **Wire `/specialist` slash command** in `core/commands/` — uses the specialist registry to activate a specialist
7. **Integrate with system prompt** — when a specialist is active, inject its prompt as a `<specialist>` block in the system prompt, similar to how skills are injected

### Integration with Existing Systems

- **System prompt injection**: The specialist's prompt is injected into the system prompt wrapped in `<specialist name="...">...</specialist>` tags, at the end of the prompt (after project context, before available tools)
- **Tool scope filtering**: The specialist's `tools` field limits which tools the agent sees. This can be implemented by filtering `ToolRegistry.All()` at build time when a specialist is active
- **Sub-agent routing**: When `/specialist <name>` is active, the agent should use the specialist's prompt as the sub-agent system prompt

### Testing

| Test | Scope |
|---|---|
| Unit: Specialist frontmatter parsing | `core/specialist/parser_test.go` |
| Unit: Registry discovery ordering | `core/specialist/specialist_test.go` |
| Unit: CLI create/edit/delete | `internal/app/specialist_cli_test.go` |
| Integration: Specialist prompt injection | `internal/app/specialist_integration_test.go` |

### Files to Create/Modify

| File | Action | Purpose |
|---|---|---|
| `core/specialist/specialist.go` | Create | Types, Registry |
| `core/specialist/parser.go` | Create | Markdown parser |
| `core/specialist/specialist_test.go` | Create | Tests |
| `internal/app/specialist_cli.go` | Create | CLI commands |
| `internal/app/specialist_cli_test.go` | Create | Tests |
| `internal/app/bootstrap.go` | Modify | Add specialist flags |
| `core/commands/specialist.go` | Create | /specialist slash command |
| `internal/app/prompt.go` | Modify | Inject specialist prompt |

---

## Dependency Graph

```
┌─────────────────────────────────────────────────────┐
│  1. Lifecycle Hooks System (P0)                      │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: nothing (standalone package)        │ │
│  │  needed by: §6 (hooks.json config), §7 (audit)  │ │
│  └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│  2. MCP HTTP Client Transport (P0)                   │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: nothing (extends existing client)   │ │
│  └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│  3. MCP Server CLI (P2)                              │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: internal/agentic/mcp already exists│ │
│  └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│  4. Plugin Bundling (P1)                             │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: §5 (skills), §6 (hooks)            │ │
│  │  (both are sub-features, can be built in order)  │ │
│  └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│  5. Plugin-Declared Skills (P2)                      │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: nothing (extends SkillRegistry)     │ │
│  └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│  6. User-Configurable Hooks JSON (P2)                │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: §1 (hooks engine must exist)        │ │
│  └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│  7. Audit Log (P1)                                   │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: §1 (hooks engine calls audit)       │ │
│  └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│  8. Specialist Management CLI (P1)                   │
│  ┌─────────────────────────────────────────────────┐ │
│  │  depends on: nothing (standalone package)         │ │
│  └─────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘
```

---

## Implementation Order

### Phase 1 (P0 — Immediate, can be parallelized)

```
Week 1-2:
  ├── §2 MCP HTTP Client Transport (Low effort, standalone)
  └── §1 Lifecycle Hooks Engine (Medium effort, foundational)
```

### Phase 2 (P1+P2 — Short-term, depends on Phase 1)

```
Week 3-4:
  ├── §6 User-Configurable Hooks JSON (Low effort, depends on §1)
  ├── §7 Audit Log (Medium effort, depends on §1)
  └── §8 Specialist Management CLI (Medium effort, standalone)

Week 5-6:
  ├── §5 Plugin-Declared Skills (Low effort, standalone)
  └── §3 MCP Server CLI (Low effort, standalone)
```

### Phase 3 (P1 — Medium-term)

```
Week 7-8:
  └── §4 Plugin Bundling (Medium effort, depends on §5 + §6)
```

---

## Risk Assessment

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Hooks system blocks critical tools | High | Low | Hooks are opt-in; default config has no hooks. Test with `--hooks-disable` fallback flag. |
| MCP HTTP client breaks on non-standard servers | Medium | Medium | Follow MCP spec strictly; add integration tests with a reference MCP HTTP server. |
| Plugin manifest format conflicts with existing JS plugins | Medium | Low | Clear precedence: `plugin.json` → `plugin.yaml` → `plugin.js` for same directory. Document the priority. |
| Audit log grows unbounded | Medium | Low | Implement rotation at 10 MB. Document `$GOA_AUDIT_MAX_MB` env var. |
| Specialist CLI adds flag complexity | Low | Low | Keep specialist commands as a separate binary entry point or subcommand, not mixed with main flags. |

---

## Success Criteria

| Feature | Success Metric |
|---------|---------------|
| §1 Hooks | A `beforeTool` hook can block `rm -rf` in bash tool. Integration test passes. |
| §2 MCP HTTP | Goa connects to an HTTP MCP server, lists tools, calls a tool. |
| §3 MCP serve | Another agent connects to `goa serve --mcp` and calls a Goa tool via MCP. |
| §4 Bundle | A `plugin.json` with a shell tool and a skill file is loaded without JavaScript. |
| §5 Skill from plugin | A JS plugin calling `goa.registerSkill()` makes the skill appear in skill list. |
| §6 Hooks.json | Editing `.goa/hooks.json` changes hook behavior without restarting. |
| §7 Audit | After a hook blocks a tool, `~/.goa/audit.jsonl` contains the record. |
| §8 Specialist | `goa specialist create my-spec --description "..."` creates a working specialist file. |

---

*Plan prepared by comprehensive codebase analysis. Date: 2026-07-04.*
