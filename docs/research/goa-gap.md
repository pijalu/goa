# Goa ⇄ Zero Feature Gap Analysis

> **Objective:** Identify features present in the [Zero](https://github.com/zero/zero) open-source CLI coding agent that are missing or underdeveloped in Goa, and assess their potential value to Goa.

---

## Legend

| Icon | Meaning |
|------|---------|
| ✅ | Already implemented in Goa |
| ⚠️ | Partially implemented — significant gaps remain |
| ❌ | Missing — not present in Goa |
| 🔮 | Roadmap item or planned |
| 🏆 | **High-value candidate for Goa** |

---

## 1. 🏆 Lifecycle Hooks System — **HIGH VALUE**

### Zero's Approach

Zero has a user-configurable **hooks system** defined in `hooks.json` (user and project levels). Hooks fire shell commands on lifecycle events:

| Event | When it fires | Matcher allowed? |
|-------|---------------|-----------------|
| `beforeTool` | A tool is about to run | Yes (tool name) |
| `afterTool` | A tool just returned | Yes (tool name) |
| `sessionStart` | A session begins | No |
| `sessionEnd` | A session ends | No |
| `specialistStart` | A sub-agent is spawned | Yes (specialist name) |
| `specialistStop` | A sub-agent ends | Yes (specialist name) |

Hooks receive the event payload as **JSON on stdin**. Exit code 0 continues, non-zero blocks the tool or surfaces an error. Hook execution is recorded in an audit log.

```json
{
  "enabled": true,
  "hooks": [
    {
      "id": "block-rm-rf",
      "event": "beforeTool",
      "matcher": "bash",
      "command": "/usr/local/bin/zero-hook-block-rmrf.sh",
      "enabled": true
    }
  ]
}
```

### Goa's Current State

⚠️ **Partial — JS plugin lifecycle hooks exist, no external shell hooks**:
- `plugins/lifecycle.go` defines `HookType` (start, shutdown, tool_call, tool_done, mode_enter)
- JS plugins can register observers via `goa.registerObserver()`
- `core/agentmanager_lifecycle.go` dispatches lifecycle events
- But there is **no user-configurable JSON hook system** that fires shell commands
- No equivalent of `beforeTool`/`afterTool`/`sessionStart`/`sessionEnd` shell hooks
- No `hooks.json` configuration file
- No audit log for hook execution

### Value to Goa 🏆

| Benefit | Description |
|---------|-------------|
| **Safety guards** | Block dangerous commands (rm -rf, git push --force) before they execute |
| **Enterprise compliance** | Enforce policies (no network commands, no env var leaking) via external scripts |
| **Workflow integration** | Notify CI/CD systems when sessions start/end |
| **Audit trail** | Log every tool execution for security review |
| **Plugin parity** | Completes the picture: JS plugins have lifecycle hooks, but shell-based plugins should too |

**Estimated effort:** Medium (new package `internal/hooks/`, config loader, dispatch engine)

---

## 2. 🏆 MCP HTTP Client Transport — **HIGH VALUE**

### Zero's Approach

Zero supports both **stdio** and **HTTP** MCP servers:

```json
{
  "mcp": {
    "servers": {
      "docs": {
        "type": "stdio",
        "command": "docs-mcp",
        "args": ["--port", "7777"]
      },
      "github": {
        "type": "http",
        "url": "https://api.example.com/mcp",
        "headers": { "Authorization": "Bearer YOUR_TOKEN_HERE" }
      }
    }
  }
}
```

### Goa's Current State

⚠️ **Partial — MCP client exists but stdio-only**:
- `internal/mcp/client/` has stdio transport (`StdioClient`)
- `internal/mcp/manager.go` manages server connections
- No HTTP/HTTPS transport for MCP servers
- No `type` field in `ServerConfig` to distinguish transport types
- No header support for authenticated MCP requests

### Value to Goa 🏆

| Benefit | Description |
|---------|-------------|
| **Remote MCP servers** | Connect to MCP servers running on different machines |
| **Cloud MCP services** | Use managed MCP services over HTTP |
| **Authentication** | Support bearer tokens and API keys for MCP servers |
| **Parity with Zero** | Both agents should support the same MCP transport options |

**Estimated effort:** Low-Medium (new client transport + config changes)

---

## 3. ❌ MCP Server CLI (`goa serve --mcp`)

### Zero's Approach

Zero can expose its tools as an MCP server via:
```bash
zero serve --mcp
```
This lets other agents call Zero's tools over the MCP protocol.

### Goa's Current State

⚠️ **Partial — MCP server exists internally but no CLI**:
- `internal/agentic/mcp/server.go` has a full `MCPServer` (JSON-RPC 2.0)
- `internal/agentic/mcp/publisher.go` has `MCPToolPublisher` that wraps ToolRegistry
- But there is **no `goa serve --mcp` CLI command** to expose this functionality
- No stdio-based server mode that other agents can connect to

### Value

| Benefit | Description |
|---------|-------------|
| **Interoperability** | Other agents (including Zero) can use Goa's tools |
| **CI/CD integration** | Pipe Goa's tools into automated pipelines |
| **Multi-tool orchestration** | Use Goa as a tool server in a larger system |

**Estimated effort:** Low (wrap existing MCPToolPublisher in a `cmd/goa/` subcommand)

---

## 4. ❌ Plugin Bundling (JSON manifest with hooks + skills + tools)

### Zero's Approach

Zero's `plugin.json` can bundle **tools, hooks, and skills** in a single manifest:

```json
{
  "id": "github-pr-review",
  "name": "GitHub PR Review",
  "version": "1.0.0",
  "tools": [
    { "name": "list_prs", "command": "./tools/list_prs.sh" }
  ],
  "hooks": [
    { "name": "pre-merge-check", "event": "beforeTool", "command": "./hooks/pre-merge.sh" }
  ],
  "skills": [
    { "path": "./skills/review-checklist/SKILL.md" }
  ]
}
```

### Goa's Current State

⚠️ **Partial — JS plugins with goa.* API but no unified manifest**:
- Goa has a full JS plugin system (Goja runtime) with tool/command/observer registration
- Plugins use `plugin.yaml` manifests with `entry: plugin.js`
- But plugins **cannot**:
  - Bundle shell-executable tools (only JS functions via `goa.registerTool`)
  - Declare shell hooks (only JS lifecycle observers)
  - Bundle skill files that merge into the skill registry
  - Work without JavaScript (pure JSON/manifest plugins)

### Value

| Benefit | Description |
|---------|-------------|
| **Simple plugins** | Add tools/hooks/skills without writing any code |
| **Portability** | Shell-based plugin tools work across environments |
| **Team sharing** | Share skill files and hooks alongside tools in one directory |

**Estimated effort:** Medium (new plugin format + loader changes)

---

## 5. ❌ Plugin-Declared Skills

### Zero's Approach

Zero's plugins can declare skills in `plugin.json` that are merged into the skill loader's discovery set, making them visible to the `skill` tool.

### Goa's Current State

❌ **Missing**:
- Goa's JS plugins can register tools via `goa.registerTool()` but cannot register skills in the skill registry
- Skills and plugins are completely separate systems
- No way for a plugin to contribute a SKILL.md that appears in `<available_skills>`

### Value

| Benefit | Description |
|---------|-------------|
| **Plugin + skill synergy** | A plugin can install both a custom tool and a skill that teaches the agent how to use it |
| **Simplified distribution** | One plugin directory = one installation step for all capabilities |

**Estimated effort:** Low-Medium (merge plugin-declared skills into SkillRegistry)

---

## 6. ❌ User-Configurable Hooks JSON

### Zero's Approach

Zero uses human-editable `hooks.json` files at user (`~/.config/zero/hooks.json`) and project (`.zero/hooks.json`) levels. Users configure shell command hooks without writing any code.

### Goa's Current State

❌ **Missing**:
- No `hooks.json` configuration file format
- No project-level or user-level hooks configuration
- JS plugin hooks require writing JavaScript — not accessible to non-developers

### Value

| Benefit | Description |
|---------|-------------|
| **Accessibility** | Any user can add hooks by editing a JSON file |
| **No rebuild** | Hooks take effect on next session start |
| **Team policy** | Commit `.goa/hooks.json` to enforce team-wide safety rules |

**Estimated effort:** Low (config loader + dispatch)

---

## 7. ❌ Audit Log for Lifecycle Events

### Zero's Approach

Zero records hook execution in an audit log that is reachable from the agent's view of past actions. This provides accountability for every tool call and hook decision.

### Goa's Current State

❌ **Missing**:
- Goa has session logging (JSONL events) but no structured audit trail
- No per-tool execution record outside the conversation viewport
- No way to review "who/what blocked this tool call and why"

### Value

| Benefit | Description |
|---------|-------------|
| **Security** | Track all tool executions for compliance |
| **Debugging** | Understand why a tool was blocked or failed |
| **Transparency** | Users can review agent decisions |

**Estimated effort:** Medium (new audit data structure + storage + TUI integration)

---

## 8. ❌ Specialist Management CLI

### Zero's Approach

Zero has dedicated CLI subcommands for managing lightweight sub-agent specialists:

```bash
zero specialist list
zero specialist show api-reviewer
zero specialist create api-reviewer --project --description "Reviews API changes"
zero specialist edit api-reviewer --project
zero specialist delete api-reviewer --project
zero specialist path
```

Specialists are markdown files with YAML frontmatter and system prompts, managed at three scopes (built-in, user, project).

### Goa's Current State

⚠️ **Partial — custom modes/profiles exist but no lightweight "specialist" sub-agent concept**:
- Goa has custom modes (`prompts/mode/<name>/definition.md`) with guard rules
- Goa has skills that can act as sub-agents
- But there's no dedicated **specialist** concept with:
  - A scoped directory (`.goa/specialists/`, `~/.goa/specialists/`)
  - A standard markdown format with frontmatter (description, tools, prompt)
  - CLI subcommands for management
  - Lightweight scope without requiring a full mode definition

### Value

| Benefit | Description |
|---------|-------------|
| **Simplicity** | Create a sub-agent specialist by writing one markdown file |
| **Discoverability** | List/show specialists via CLI |
| **Project sharing** | Commit specialists to the repo |
| **Scoping** | Three levels (built-in, user, project) with override semantics |

**Estimated effort:** Medium (new CLI subcommands + specialist registry + loader)

---

## Summary of Gaps by Value and Effort

| # | Feature | Value | Effort | Priority |
|---|---------|-------|--------|----------|
| 1 | 🏆 Lifecycle Hooks System | High | Medium | **P0** |
| 2 | 🏆 MCP HTTP Client Transport | High | Low-Medium | **P0** |
| 7 | Audit Log for Lifecycle Events | Medium | Medium | P1 |
| 4 | Plugin Bundling (JSON manifest) | Medium | Medium | P1 |
| 8 | Specialist Management CLI | Medium | Medium | P1 |
| 3 | MCP Server CLI (`serve --mcp`) | Medium | Low | P2 |
| 5 | Plugin-Declared Skills | Low-Medium | Low-Medium | P2 |
| 6 | User-Configurable Hooks JSON | Medium | Low | P2 |

---

## What Goa Already Does Better

For balance, Goa already surpasses Zero in several areas:

| Area | Goa Advantage |
|------|---------------|
| **Config cascade** | 6 levels vs Zero's 3 (embedded → home → project → local → env → flags) |
| **Skills system** | More sophisticated (inline/sub-agent, knowledge/action, sub-skills, cross-agent `.agents/skills/`) |
| **Plugin system** | Full JS runtime (Goja) vs Zero's JSON-only manifest |
| **Multi-agent orchestration** | Hub/fanout/pipeline topologies, goal binding, event-sourced run log |
| **Workflows** | Multi-stage, multi-agent workflows with AgentBus |
| **TUI** | Rich ANSI engine with differential rendering, filmstrip testing |
| **Context compression** | Micro compaction, tool elision, summarization strategies |
| **Concurrent tools** | Resource-aware parallel execution with conflict resolution |
| **Memory/Dream** | Persistent memory with consolidation (dream mode) |
| **Session management** | Full JSONL save/restore/import/export |
| **Profiles/Modes** | Guard rules (expr-lang), autonomy levels, custom mode definitions |
| **Goal tracking** | Long-running goals with budget and lifecycle |
| **Cron scheduling** | Scheduled agent tasks |
| **Loop detection** | 5 heuristics with graduated response |
| **Git worktree isolation** | Sandboxed filesystem via git worktree |
| **AGENTS.md support** | Ancestor walk, home directory, budget-aware prompt injection |

---

## Recommended Roadmap

### Phase 1 (Immediate — High Value, Low-Medium Effort)
1. **MCP HTTP client transport** — Add HTTP/HTTPS transport to MCP client
2. **Lifecycle Hooks System** — Shell command hooks with hooks.json config (core dispatch + safety guards)

### Phase 2 (Short-term — Medium Value, Low-Medium Effort)
3. **MCP server CLI** — `goa serve --mcp` subcommand
4. **Plugin-declared skills** — Merge plugin skills into SkillRegistry
5. **User-Configurable Hooks JSON** — Config file format and project/user layered loading

### Phase 3 (Medium-term — Medium Value)
6. **Audit Log** — Structured audit trail for lifecycle events
7. **Specialist management CLI** — Lightweight sub-agent specialists via CLI
8. **Plugin bundling** — JSON manifest format for shell tools + hooks + skills

---

*Analysis prepared by agentic analysis of Goa v0.1 codebase and Zero project documentation. Date: 2026-07-04.*
