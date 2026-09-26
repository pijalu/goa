<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: Tools are disabled "out of the blue" (unsynchronized registry + lost deferred loads)

Closed 2026-09-26. Moved from `bugs.md`. Fixed by commit `b00d4b8`
("tools: synchronize ToolRegistry + keep the deferred loaded-tail across
SetTools").

## Symptom

A tool that was available disappeared from one turn to the next ("tools are
disabled out of the blue"), and tools the model had loaded with `tool_search`
started being answered by the deferred-status redirect instead of executing.

## RED evidence

All four racing tests fail at the pre-fix commit (`cc5f35a` = `b00d4b8^`); test
files were copied into a scratch worktree of that commit.

`TestToolRegistry_ConcurrentRegisterAndAll` — `go test -race ./tools/`:

```
WARNING: DATA RACE
Read at 0x00c000b52c30 by goroutine 26:
  tools.sortedKeys()           tools/registry.go:167
  tools.(*ToolRegistry).All()  tools/registry.go:90
Previous write at 0x00c000b52c30 by goroutine 22:
  tools.(*ToolRegistry).Register()  tools/registry.go:70
```

`TestToolRegistry_AllIsSnapshot` + `TestToolRegistry_GroupOpsConcurrent` —
`go test -race -run 'ToolRegistry_AllIsSnapshot|ToolRegistry_GroupOpsConcurrent'`:

```
--- FAIL: TestToolRegistry_AllIsSnapshot
--- FAIL: TestToolRegistry_GroupOpsConcurrent
WARNING: DATA RACE   (registry.go:167/168 All vs registry.go:70 Register, :84 Unregister)
WARNING: DATA RACE   (registry.go:34 RegisterGroup append vs registry.go:36/93 Match/All reads)
WARNING: DATA RACE   (registry.go:58 UnregisterGroup in-place filter vs registry.go:36/93)
FAIL  github.com/pijalu/goa/tools
```

`TestAgentSetTools_PreservesDeferredLoadedTail` (C2 test plus a `LoadedDeferred`
shim over HEAD fields, since the interface method does not exist pre-fix):

```
--- FAIL: TestAgentSetTools_PreservesDeferredLoadedTail
    DeferredStatus("d0") unloaded = true after SetTools; the loaded-tail was discarded
    DeferredStatus("d5") unloaded = true after SetTools; the loaded-tail was discarded
    LoadedDeferred() = [], want [d0 d5] (load order preserved)
    Schemas() = 3 entries after SetTools, want 5
```

## Root cause — C1, `tools.ToolRegistry` had no synchronization

`tools/registry.go` kept a plain `map[string]agentic.Tool` behind `Register`/
`Unregister`/`RegisterGroup`/`UnregisterGroup` (writers: the TUI goroutine via
`/config` and `/tools`, MCP connect/disconnect, plugin load) while `All()`/
`Get()`/`AllDocumented()` iterate/lookup the same map from other goroutines.
The agent path reads the live registry on every request
(`ToolSearchTool.Schema()` and `ExecuteWithResult` → `deferredTools()` →
`t.reg.All()`), and `All()` walks every registered tool. Concurrent
write + iterate/lookup is a data race; a lost `Register` (or a group
unregister racing a re-register) surfaces as a tool silently disappearing.

## Root cause — C2, a tool-set update discarded the deferred loaded-tail

`Agent.SetTools` rebuilt the agent-side registry
(`internal/agentic/agent_config.go` → `NewToolRegistry(tools)`), throwing away
the append-only loaded-tail (`loadedOrder` / `loadedSchemas`,
`internal/agentic/tool_registry.go`). Any runtime tool-set push
(`/config` or `/tools` toggle → `refreshToolRegistry`, MCP register/unregister,
plugin load) reverted tools the model had loaded to "deferred, not loaded", so
the next call was answered by the deferred-status redirect.

## Call-site audit (criterion 8)

The registry lock is an unexported field of `tools.ToolRegistry`, so no caller
can hold it, and it is a **leaf lock**: while it is held the code performs no
call into tool code or any other package (`Register` resolves `Schema()` before
locking; `AllDocumented` calls `ShortDoc`/`LongDoc`/`Examples` after releasing;
`Match` runs `g.Match` over a copied slice after releasing). It therefore cannot
form a lock cycle with a caller's mutex (`plugins.completionsMu`,
`acp_driver.mu`, subsystems/TUI locks) — no inversion, no new deadlock risk.

Call sites inspected (all consume the returned value immediately; none relied on
the unsynchronized behavior — `All()` already returned a freshly allocated slice
before the fix, so snapshot semantics are unchanged):

| Call site | Use |
| --- | --- |
| `tools/tool_search.go:186` `deferredTools()` → `All()` | per-`Schema()` listing; exercised by `TestToolRegistry_AllWhileSchemaReenters` |
| `internal/app/prompt.go:141,515` | prompt tool set / deferred-tools section (filtered snapshot) |
| `internal/app/probe.go:24`, `internal/app/acp_driver.go:54` | schema probe, ACP tool push |
| `internal/app/plugins.go:359,493` | plugin host register / call-tool lookup |
| `core/commands/docs.go:113,208,421,429,438,484,594` | `/docs`, `/tools:…:on\|off`, registry refresh |
| `core/commands/config_tools.go:99`, `core/commands/mcp.go:443` | `/config → Tools`, MCP connect/disconnect → `AgentManager.SetTools(reg.All())` |
| `core/commands/meta.go:82`, `session.go:92`, `config_sandbox.go:110-178`, `core/docengine.go:125,158` | status counts, session list, sandbox toggles, doc lookup |

The only observable behavior change for callers is `AllDocumented()` now being
name-sorted instead of map order (its only consumer outside the type is
`tools/registry_test.go`, verified by grep). The `agentic.ToolRegistry`
"immutable after construction (except the append-only loaded-tail)" contract is
unchanged — `Agent.SetTools` still builds a fresh one and now replays the tail.

## Fix

1. **C1** — `RWMutex` guard on the tool map, doc map, and group list, with
   snapshot-then-release reads in `Get`/`All`/`AllDocumented`/`Match` and
   synchronized writes in `Register`/`Unregister`/`RegisterGroup`/
   `UnregisterGroup`. The lock is never held across a call into a registered
   tool: `ToolSearchTool.Schema()` re-enters the registry (its description is
   derived from the live `All()`), so `Register` resolves the name before
   locking and `AllDocumented` calls the doc accessors after releasing.
   `AllDocumented` output is now name-sorted (map order was random, so its
   output was not byte-stable).

2. **C2** — new `ToolLookup.LoadedDeferred() []string` (load order, copy,
   nil-safe) on the registry abstraction; `Agent.SetTools` snapshots the tail
   from the outgoing registry and replays it onto the fresh one via
   `LoadDeferred`, which skips unknown / no-longer-deferred / already-loaded
   names — a genuinely removed tool is not resurrected, and the request payload
   stays byte-identical (provider prefix cache stable).

## Tests

| Test | Package | Asserts |
| --- | --- | --- |
| `TestToolRegistry_ConcurrentRegisterAndAll` | `tools` | 4 writers × 4 readers; clean under `-race`, no lost registration |
| `TestToolRegistry_AllIsSnapshot` | `tools` | in-flight `All()` never sees later Register/Unregister; a racing writer never tears it nor loses a tool |
| `TestToolRegistry_GroupOpsConcurrent` | `tools` | `RegisterGroup`/`UnregisterGroup` racing `Match`/`All` readers; group tool matchable between register and removal; base group survives |
| `TestToolRegistry_AllDocumentedSnapshotAndOrder` | `tools` | stable order + snapshot semantics |
| `TestToolRegistry_AllWhileSchemaReenters` | `tools` | no deadlock when `Schema()`/`ShortDoc()` re-enter the registry |
| `TestAgentSetTools_PreservesDeferredLoadedTail` | `internal/agentic` | tail survives `SetTools`: status, order, byte-identical schemas, callable without redirect |
| `TestAgentSetTools_DoesNotResurrectRemovedTool` | `internal/agentic` | removed name is not replayed back in |
| `TestAgentSetTools_NilRegistryTailSnapshotIsSafe` | `internal/agentic` | nil / typed-nil registry does not panic |

## Gate (run separately, post-change)

`go vet ./...` clean · `staticcheck ./...` clean · `gocognit -over 15 .` clean ·
`gocyclo -over 12 .` clean · `go test -count=1 -race -cover ./...` → all
packages ok, 0 FAIL (exit 0); `tools` 84.7%, `internal/agentic` 88.0%; the new
functions (`LoadedDeferred`, `loadedDeferredOf`) and every synchronized
`ToolRegistry` method at 100% statement coverage under their tests.
