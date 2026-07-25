# MCP Completion Plan — /mcp command + e2e verification

> **Goal:** Finish the MCP feature so Goa connects to and executes MCP servers
> like OpenCode, exposed via the idiomatic Goa `/mcp:<subcommand>` surface with
> completion, then verify end-to-end with LMStudio (gemma) + chrome-devtools-mcp
> (open a webpage, retrieve its content).

## State going in
- **Committed:** MCP config (`mcp:` YAML), go-sdk v1.6.1 transports (stdio +
  remote HTTP/SSE), sanitized/normalized tool registration, `<mcp_instructions>`
  injection; 4 unrelated bug fixes.
- **Uncommitted plumbing (compiles, vet-clean):** `core.Context.MCP`;
  `registerMCPServers` always returns a manager; `config` `SaveProjectFieldValue`/
  `DeleteProjectField` (map-capable YAML write/delete) + interface + fake saver;
  `internal/mcp/fromconfig.go` (`FromConfig` bridge).

## Work items

### A. Command surface (`/mcp:<sub>`) — follows Goa conventions
Router (`core/router.go`) splits on `:` → `/mcp:list` = command `mcp`, args `["list"]`.
New file `core/commands/mcp.go`, one `MCPCommand` type (mirrors `GoalCommand`):
- `Name() "mcp"`, `ShortHelp`, `LongHelp` (via `help.LongHelp`).
- `Run(ctx, args)`: first arg = subcommand (default `list`); dispatch map.
- `CompleteArgs(ctx, prefix)`: subcommands for arg[0]; server names (from
  `ctx.Config.MCP`) for server-taking subcommands — this is the completion hook.
- Subcommands:
  - `/mcp:list` — table: name, type, status icon (✓ connected / ○ disabled /
    ✗ failed), tool count, target (command or url). Reads `ctx.Config.MCP` +
    `ctx.MCP.Status()`.
  - `/mcp:add <name> (--url <u> [--header K=V]... | -- <cmd...> [--env K=V]...)`
    — validate url-vs-command exclusivity; build `config.MCPServerConfig`;
    persist via `ConfigSaver.SaveProjectFieldValue(["mcp", name], cfg)`; update
    in-memory `ctx.Config.MCP`; **connect now** (OpenCode behavior).
  - `/mcp:remove <name>` — `DeleteProjectField(["mcp", name])`, drop from
    `ctx.Config.MCP`, `ctx.MCP.Disconnect(name)`.
  - `/mcp:enable <name>` / `/mcp:disable <name>` — flip `enabled` in project
    config (`SaveProjectFieldValue(["mcp", name, "enabled"], bool)`) + live
    connect/disconnect.
  - `/mcp:reconnect <name>` — `Disconnect` then `Connect(FromConfig(...))`.
  - `/mcp:debug <name>` — one-shot connect, print status + tool list.
- **Tool refresh:** after any connect/disconnect, push the live set to the agent:
  `ctx.AgentManager.SetTools(ctx.ToolRegistry.All())` so tools appear/disappear
  without a restart.
- Register `&MCPCommand{}` in `core/commands/register.go` `RegisterAll`.

### B. Housekeeping
- Point `internal/app/bootstrap.go` at `mcp.FromConfig` (remove the now-duplicated
  local `mcpServerConfig`/`resolveMCPCwd`/`mcpTimeout`).
- Tests: `core/commands/mcp_test.go` — arg parsing (url vs command exclusivity,
  K=V parsing), list rendering from a fake manager, enable/disable toggling
  manager state, add/remove round-trip against `t.TempDir()` project config.
- Gate: `go vet ./...`, `go test -count=1 -race ./...` (affected pkgs),
  `gocognit -over 15`, `gocyclo -over 12`.
- Commit.

### C. Phase 1d lifecycle (makes it robust)
- `notifications/tools/list_changed` → re-list + swap registered group.
- `roots/list` → answer with `file://<projectDir>`.
- Process-tree cleanup on stdio `Close()` (kill child + descendants, unix).
- Server log notifications → `agentic.Logger`.

### D. E2E verification (the acceptance test)
1. Prereq: LMStudio serving gemma on `http://localhost:1234`; `npx` + Chrome.
2. Fresh temp project; Goa provider → LMStudio; `mcp.chrome-devtools` =
   `{type: local, command: ["npx","-y","chrome-devtools-mcp@latest"], timeout: 60s}`
   (add via `/mcp:add` to exercise the command, or config for the headless run).
3. Run headless: `goa --prompt "Open https://example.com and return its text
   content"` and assert an `mcp__chrome-devtools__*` tool fires and page text
   (e.g. "Example Domain") appears in the output.
4. Iterate on any failure (spawn, handshake, tool call, content extraction)
   until it passes.

## Out of scope (separate follow-ups)
- Phase 3 OAuth (SDK `auth` pkg) — not needed for local stdio chrome-devtools-mcp.
- Phase 4 resources/prompts as first-class tools.
