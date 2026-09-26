<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: Enabling a tool during a session does not enable it

Closed 2026-09-26. Moved from `bugs.md`. Fixed in this commit ("config: one
shared tool enable/disable primitive that really constructs and registers").

## Symptom

`/config → Tools → <name>` flipped `tools.enabled.<name>` and saved it, but the
tool was never constructed: `/config` reported it enabled while the registry did
not contain it, so the model's call failed ("unknown tool") even though the next
session had it. The slash path `/tools:<name>:on` did consult `ctx.ToolFactory`,
so the two surfaces disagreed. Two further holes sat in the same area:

- the registered goal tool captured `createFlagOn` at construction, so a tool
  built while the flag was false kept `create` blocked until restart (closed by
  the goal-tool goal, verified here end-to-end);
- `refreshToolRegistry` pushed `ToolRegistry.All()` instead of the mode-filtered
  set the session started with (`filterToolsForCurrentMode`), so a toggle could
  advertise tools the active mode disallows and re-partition the tool set.

## RED evidence

`go test -count=1 -timeout 120s -run 'TestConfigMenu_Enable|TestConfigMenu_ToggleMatches|TestRefreshToolRegistry_|TestMakeToolFactory_BuildsEvery' ./core/commands/... ./internal/app/...`
at the pre-fix code:

```
--- FAIL: TestConfigMenu_EnableToolRegistersIt (0.00s)
    tool_lifecycle_runtime_test.go:216: enabling bg_exec did not construct it through ToolFactory
--- FAIL: TestConfigMenu_EnableToolWithoutFactorySaysRestart (0.00s)
    tool_lifecycle_runtime_test.go:264: enabling a tool that cannot be built at runtime must tell the user a restart is required; messages = []
--- FAIL: TestConfigMenu_ToggleMatchesToolsCommand (0.00s)
    tool_lifecycle_runtime_test.go:300: both paths must construct the tool: menu=0 cmd=1
--- FAIL: TestRefreshToolRegistry_UsesLiveProvider (0.00s)
    tool_lifecycle_runtime_test.go:334: refreshToolRegistry ignored the live tool-set provider
--- FAIL: TestRefreshToolRegistry_FallsBackToRegistry (0.00s)
    tool_lifecycle_runtime_test.go:354: fallback pushed [read send_message write], want the registry [read write]
FAIL	github.com/pijalu/goa/core/commands	0.501s
--- FAIL: TestMakeToolFactory_BuildsEveryConfigurableTool (0.00s)
    tool_factory_coverage_test.go:58: factory cannot build "goa" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "verify" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "ask_user_question" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "run_code" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "delegate_to" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "lsp" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "terminals" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "request_review" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:58: factory cannot build "webfetch" and it is not documented as restart-only: enabling it at runtime would silently do nothing
    tool_factory_coverage_test.go:75: ask_user_question must be constructible at runtime
FAIL	github.com/pijalu/goa/internal/app	0.511s
```

The RED run used the initial minimal fixture, so the dependency-gated names
(`goa`, `terminals`, `lsp`, `delegate_to`, `request_review`) failed for fixture
reasons as well; the fixture was then completed (PTY manager, command router,
sub-agent pool, session store, clarify hook, LSP enabled) so the delivered test
isolates the real gaps: `verify`, `run_code`, `webfetch`, `ask_user_question`
were never constructible and `smartsearch` is the only documented restart-only
tool.

Coverage for the four tools, live provider and push/teardown paths was RED the
same way. `TestGoalToolEnabledLive_ConfigMenuPath` was **strengthened** from an
emulation (flag flip + manual `SetTools`) to the real `/config` command path; no
separate pre-fix run is claimed for it — the failure it now catches is exactly
test 1's (`held == nil`: the menu enabled nothing).
`TestRuntimeToggle_KeepsDeferredTail` is a regression guard over the loaded-tail
work landed in `b00d4b8`; it passed before this change too.

## Root cause

`core/commands/config_tools.go` had its own half-implementation:
`toolToggleHandler` read the current state, called `applyToolToggle(name,
enabled)` — which early-returned when the tool was being **enabled**
(`if !enabled { return }`, i.e. only the disable direction acted) — then flipped
and saved the config. Enabling therefore registered nothing, while the slash
path (`docs.go toggleTool` → `enableRuntimeTool`) did build through
`ctx.ToolFactory`. Two surfaces, two implementations, no shared contract for
"the tool could not be built at runtime" (silent no-op), and
`refreshToolRegistry` pushed the raw registry rather than the session-shaped,
mode-filtered set.

## Fix

1. **One primitive** — new `core/commands/tool_lifecycle.go`:
   `ApplyToolToggle(ctx, name, enabled)` flips the flag, persists it, and applies
   the change live, returning an explicit `ToolToggleOutcome`
   (`Unchanged` / `Applied` / `RestartRequired`) plus the single wording helpers
   (`ToolToggleMessage`, `ToolRestartMessage`, `ToolToggleErrorText`).
   `enableRuntimeTool` / `disableRuntimeTool` / `refreshToolRegistry` live there
   too and are the only implementations. `config_tools.go toolToggleHandler` and
   `docs.go toggleTool` both call the primitive; `applyToolToggle`'s
   disable-only logic is deleted. Enabling is idempotent and reconciling: if the
   flag already says on but the registry lacks the tool, it is constructed now,
   and an already-registered tool is never replaced by a second instance.
2. **No silent no-op** — when no live instance can exist (no factory, factory
   cannot build, no registry) the outcome is `RestartRequired` and the user is
   told: the menu sends a durable system message plus a flash, the command
   surface writes `Tool <name> could not be instantiated at runtime. Restart Goa
   to apply the change.` A failed config write reports
   `Failed to save config: …` and leaves the runtime untouched.
3. **Session-shaped push** — `core.Context` gains
   `LiveTools func() []agentic.Tool` (fallback via `Context.LiveToolSet()`);
   `internal/app` wires it with `liveToolSetProvider(subs)` =
   `filterToolsForCurrentMode(subs, subs.toolRegistry.All())` — the same call
   `startSession` makes, so `prompt.go` stays the single source of that filter
   and a toggle can no longer advertise a mode-disallowed tool.
4. **Factory coverage** — `makeToolFactory` is now a builder table
   (`runtimeToolBuilders`), with `verify`, `run_code`, `webfetch` and
   `ask_user_question` (carrying the same Clarify hook the startup registration
   receives, retained as `subsystems.clarifyFn`) added;
   `registerVerifyTool`/`registerRunCodeTool`/`registerWebFetchTool` and the new
   `makeVerifyTool`/`makeRunCodeTool`/`makeWebFetchTool` share one constructor
   each so startup and runtime can never build different tools. `smartsearch`
   keeps the documented restart-only path.
5. **Typed-nil hardening** — the /config menu surfaced a latent hazard: a nil
   `*skills.SkillRegistry` (or `*tools.ToolRegistry`) stored in an interface
   field is non-nil, defeats the `if reg == nil` guards (skillSummariesForSource
   type-asserts to the concrete type and panics). Wiring now goes through
   `toolRegistryFor` / `skillRegistryFor`, which return a real nil interface.
6. **Deferred-tail observability** — `Agent`/`AgentManager` gained
   `DeferredStatus`, `LoadedDeferred` and `LoadDeferredTools` (read + the
   loader's append-only load), so a runtime toggle's effect on the deferred
   loaded tail is assertable outside `internal/agentic`.

## Tests

| Test | Package | Asserts |
| --- | --- | --- |
| `TestConfigMenu_EnableToolRegistersIt` | `core/commands` | menu OFF→ON constructs via the factory, registers, pushes to the running agent, no restart claim; ON→OFF unregisters + tears down + unpushes |
| `TestConfigMenu_EnableToolWithoutFactorySaysRestart` | `core/commands` | nil factory → explicit restart message, nothing registered |
| `TestConfigMenu_ToggleMatchesToolsCommand` | `core/commands` | `/config` and `/tools:verify:on` end with identical registry + pushed agent set |
| `TestConfigMenu_ToolToggleSaveFailureReportsAndSkipsRuntime` | `core/commands` | failed save → user told, runtime untouched, lowercase error (ST1005) + capitalized render |
| `TestRefreshToolRegistry_UsesLiveProvider` / `_FallsBackToRegistry` | `core/commands` | provider called, its slice pushed verbatim; registry fallback when unwired |
| `TestRuntimeToggle_KeepsDeferredTail` | `core/commands` | deferral active (loader + threshold), tail loaded, toggle → `DeferredStatus` unchanged, `LoadedDeferred` preserved |
| `TestLiveToolSetProvider_MatchesSessionStartFilter` | `internal/app` | wired provider == `filterToolsForCurrentMode`, mode-disallowed tool withheld |
| `TestGoalToolEnabledLive_ConfigMenuPath` | `internal/app` | real `ConfigCommand` → Tools → goal: registers, pushes, agent holds the registry instance, same-session `create` succeeds |
| `TestMakeToolFactory_BuildsEveryConfigurableTool` | `internal/app` | table over `ConfigurableTools()`: built with matching schema name (Clarify hook for ask_user_question) or the allowlisted restart-only `smartsearch` |
| `TestApplyToolToggle_EveryConfigurableToolRegistersOrSaysRestart` | `internal/app` | same table through the caller: registered + pushed, or restart wording — never a silent success |
| `TestConfigMenu_SmartSearchEnableReportsRestart` | `internal/app` | real menu for smartsearch: flag saved, nothing registered, restart message surfaced |

## Gate (run separately, post-change)

`go vet ./...` clean (exit 0) · `staticcheck ./...` clean (exit 0) ·
`gocognit -over 15 .` clean (exit 0) · `gocyclo -over 12 .` clean (exit 0) ·
`go test -count=1 -race -cover -timeout 900s ./...` → 87 packages ok, 0 FAIL
(exit 0): `tools` 84.7%, `internal/agentic` 87.8%, `config` 80.0%, `core` 78.0%,
`core/commands` 64.7%, `internal/app` 62.1%. The two low packages are
pre-existing and unchanged by this work — measured at the parent commit
(`HEAD~1`, scratch worktree): `core/commands` 64.5%, `internal/app` 62.4% (both
interactive/TUI-heavy packages). The new code itself is covered:
`tool_lifecycle.go` 81–100% per function (100% for persist/disable/refresh),
`makeWebFetchTool` 92.6%, `makeRunCodeTool`/`makeVerifyTool` 100%.
