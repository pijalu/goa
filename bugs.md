# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

## Provider 400: tool arguments must be valid JSON
Observed: `Error: 400 - Error from provider (Console Go): Upstream request failed:
[invalid_request_error] arguments must be valid JSON - /Users/muaddib/dev/goa/.goa/exports/goa-export-20260907-090335.zip`
Expected: tool call arguments containing a filesystem path (zip export path) are sent as
valid JSON (properly escaped/quoted); no provider 400. Investigate argument
serialization for the tool call carrying the export path (likely missing JSON
escaping or raw string interpolation) and add a regression test.

## Luna session stuck: provider stream went silent mid-response, watchdogs have unbounded escape holes, diagnostics blind at the moment of the stall
Observed: export `goa-export-20260922-064313.zip` (session `1790051421_y7jyq4e3`,
provider `openai-codex`, model `gpt-5.6-luna`, mode `coding-posture`). Request 4
was sent at 06:42:27.936 ("Re-streaming after tool call (round 3)") and streamed
content deltas until 06:42:32.286, then went totally silent: no deltas, no
thinking, no stats events until export at 06:43:14 (41s+). At export time the
HTTP stream was still open — `http.jsonl` contained only the 3 completed
transactions (entries finalize only on body EOF/close), the turn history showed
"Total: 0 turn(s)", and the TUI was frozen on a "thinking…" spinner. The user
reported "Stuck".

Root-cause analysis (from bundle + code) found the 2-minute watchdogs
(byte-idle `provider/idle_timeout.go`, event-stall `agent_streaming.go`) should
have recovered a *silent* stream, but there are structural gaps that can turn a
stall into an indefinite hang, and the diagnostics needed to confirm the case
are blind exactly when the stall happens:

- **F1a (HIGH)**: `emitEvent` (`internal/agentic/agent_events.go`) calls
  observers synchronously on the stream-consumer goroutine with no bound. A
  wedged observer blocks the consumer *outside* the stream receive, so the
  event-stall watchdog's `CloseWithError` cannot unblock it → permanent hang.
- **F1b (HIGH)**: the event-stall watchdog resets on **every** event, while
  unmapped event types are silent no-ops (`agent_streaming.go` handleStreamEvent
  returns for unknown types). A provider streaming periodic unmapped
  keep-alive/pacing events keeps both watchdogs alive while the user sees
  nothing → indefinite apparent hang with "healthy" logs.
- **F2 (MEDIUM)**: `HTTPLogEntry` is added only when the response body closes
  (`transport/http.go` `logOnCloseBody`). During a stuck stream the primary
  diagnostics (`logs/http.jsonl`, `diagnostics/trace.json`) show nothing about
  the open request — no status, no tail, no error — which is precisely the
  moment diagnosis needs them.
- **F3 (MEDIUM)**: `summarizeRequestBody` only parses `raw["messages"]`; codex
  `/responses` payloads use `input` → `messageCount=0`, `lastIsToolResult=false`,
  `requestSummary=nil` for every codex request in the trace, and the 2048-byte
  `requestBody` tail lands inside tool schemas. The README's headline check
  ("was a tool result sent back?") cannot be performed for `openai-codex`.
- **F4 (LOW)**: `execution.activity_timeout: 30s` is validated and merged but
  consumed nowhere — dead config; the user's configured expectation is ignored.
- **F5 (UX)**: after the last content delta there is no user-visible progress
  feedback during provider silence (the thinking-stall warn only covers
  thinking-only gaps); a dead spinner for up to 120s reads as "stuck" even when
  the watchdog would self-heal.

Expected: a provider silence (silent connection *or* invisible keep-alive
events) is detected, surfaced to the user, and recovered within the configured
window; no observer can wedge the stream consumer; an export taken during a
stall shows the in-flight request with enough detail to diagnose it; request
summaries work for codex payloads; every advertised config key is wired.

### Fix plan (test approach + validation)
1. **F3** — `transport`: parse `input` when `messages` is absent (chat-style
   `role` and codex `function_call`/`function_call_output` items); capture the
   message-region tail as `RequestBody` instead of the raw body tail.
   *Test*: table-driven `summarizeRequestBody` cases (messages / codex input /
   garbage) + body-capture assertion that recent tool results are inside the
   tail. Validate: `go test ./internal/agentic/provider/transport/…`.
2. **F2** — `transport`: register a *pending* entry at response-header time,
   finalize it on close (moves to the ring); export merges pending+completed
   sorted by time; `trace.json` marks the open request and flags
   "last request still in flight at export". *Test*: pending lifecycle
   (visible while open, moves on finish) + trace anomaly assertion.
3. **F4** — `provider`: `BuildStreamOptions` uses `execution.activity_timeout`
   as `opts.IdleTimeout` when the provider has no explicit `idle_timeout`
   (bounds both byte-idle and event-stall guards). *Test*: manager test with
   config `activity_timeout: 30s`, provider idle empty/overridden.
4. **F5** — `agentic`: per-stream quiet-warning timer at stallTimeout/2 emits
   one `EventProgress` ("provider quiet for Xs, will auto-retry at Ys").
   *Test*: silent-stream harness observes exactly one progress event before the
   stall error.
5. **F1a** — `agentic`: per-observer bounded delivery queue + pump goroutine;
   if the queue stays full for the delivery timeout, warn and detach the
   observer instead of blocking the stream. *Test*: wedged observer — emit
   returns within the (test-shrunk) timeout, later events skip, healthy
   observers unaffected and in-order; panic in observer doesn't kill the pump.
6. **F1b** — `agentic`: only *mapped* events reset the event-stall watchdog;
   unmapped event types are logged (once per type per stream) and do not reset
   it. *Test*: provider streaming only unmapped events trips the stall and ends
   the turn.
7. **Gate**: run `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
   `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` separately; no
   new warnings vs baseline (staticcheck `probe.go:171` S1008 and gocyclo
   `agent_probe_retry_test.go`/`editfile.go` are pre-existing). Commit each fix.

### Status: FIXED — all items executed and validated
| Item | Commit | Regression tests | Validation |
|---|---|---|---|
| F3 | `d504523` | `TestSummarizeRequestBodyCodexInput`, `TestRequestAnalysisCapturesConversationRegion` | transport pkg green |
| F2 | `da3d254` | `TestHTTPLogPendingLifecycle`, `TestHTTPLogSnapshotAllMergesChronologically`, `TestHTTPLogPendingDuringStalledStream`, `TestBuildLLMTrace_PendingLastRequest`, `TestBuildLLMTrace_NoPendingAnomalyWhenFinalized` | transport+export green |
| F4 | `b6c1872` | `TestBuildStreamOptions_ActivityTimeoutIsConsumed` (4 cases) | provider+config green |
| F5 | `28c120d` | `TestSilentStream_EmitsQuietProviderWarning`, `TestPacedStream_NoQuietProviderWarning` | agentic green |
| F1a | `ec2e287` | `TestEmitEvent_WedgedObserverDetached`, `TestEmitEvent_SynchronousForHealthyObserver`, `TestEmitEvent_PreservesOrder`, `TestEmitEvent_PanickingObserverIsolated`, `TestRemoveObserver_SynchronousWithinOnEvent` | agentic/core/tui green |
| F1b | `b6a7597` | `TestUnmappedEventFlood_TripsStallWatchdog`, `TestNoteStreamEventProgress` | agentic green |

Gate (run separately, post-change): `go vet ./...` clean · `staticcheck ./...`
= only the pre-existing `probe.go:171` S1008 · `gocognit -over 15 .` clean ·
`gocyclo -over 12 .` = only the two pre-existing entries ·
`go test -count=1 -race -cover ./...` → 87 packages ok, 0 FAIL (exit 0),
`internal/agentic` 87.9%. Issue entry ready to archive per guideline 4.
## Embedded skills are active by default (telegram sticky-injected into every session; dream reported ON)
Observed: the embedded default-off policy keeps two exceptions
(`skills/loader_loading.go:268-301`): `DefaultOnEmbeddedSkill = "telegram"` stays
**ON** by default, and hidden/internal embedded skills (dream) are excluded from
the default-off set. Consequences:

- `skills/telegram/SKILL.md` is `inline: true`, `category: knowledge`,
  `sticky: true`, so the ON-by-default telegram skill is persisted into EVERY
  agent's history by the sticky-skill provider (`internal/app/subsystems.go`
  `stickySkillProvider`) and is advertised in `<available_skills>` — a fresh
  install silently forces telegraphic thinking/communication on the user.
- The hidden dream skill is loaded and reported ON in
  `/config → Skills → Embedded` (`skillEnabled`/`EmbeddedDefaultDisabled`), even
  though memory consolidation is a user-invoked feature.

Expected: **every** embedded skill is OFF by default — including telegram and
dream — and the user opts in explicitly (`skills.embedded_enabled`, or
`/config → Skills → Embedded`). Dream stays off by default; when enabling it
cannot take effect without a restart, the user is told so instead of the toggle
silently doing nothing. File-based (home/project/plugin) skills keep their
current semantics.

### Fix plan (test approach + validation)
1. **skills**: make the default-off set cover every embedded skill. Remove the
   `DefaultOnEmbeddedSkill` telegram exception and the hidden-skill exclusion
   from `DefaultEmbeddedOffNames`; the set becomes "all embedded skills",
   derived from the embedded FS (no hardcoded names).
   *Test*: `TestDefaultEmbeddedOffNames_CoversEveryEmbeddedSkill` — every name
   from `EmbeddedSkillNames(EmbeddedSkillsFS)` is in the default-off set,
   including `telegram` and `dream`;
   `TestShippedEmbeddedSkills_AllOffByDefault` — a registry wired like
   `newSkillRegistry` (embedded FS + default-off set) has an EMPTY `List()` and
   an empty `StickyBodies()` for a default config; `TestEmbeddedSkill_OptIn…`
   — `embedded_enabled: [telegram]` loads exactly telegram (sticky body present)
   and `embedded_enabled: [dream]` loads dream, while other embedded skills stay
   off.
2. **skills/commands**: `IsEmbeddedDefaultOff` stays true for every embedded
   skill, so a disable is "drop the opt-in" (`setSkillEnabled`); keep the
   `Disabled` honouring so configs that pinned `skills.disabled: [telegram]`
   under the old default-ON policy stay off and can still be re-enabled.
   *Test*: extend `core/commands/config_skills_test.go` — telegram shows off,
   toggling ON writes `skills.embedded_enabled: [telegram]` (never a stale
   `disabled` entry), toggling OFF removes it.
3. **app**: `ReloadHandler.ReloadSkills` must refresh `Skills.EmbeddedEnabled`
   from the reloaded (on-disk) config like it already does for
   `Enabled/Disabled/Sticky/StickyOff`, so the running session and a freshly
   started one compute identical skill sets; after the reload the toggle checks
   whether the requested state is actually live and, when it is not (no reload
   handler / skill could not load), informs the user that a **restart is
   required** instead of flashing success.
   *Test*: `TestReloadSkills_PicksUpEmbeddedEnabled`, and a toggle test
   asserting the restart notice when no `ReloadHandler` is wired.
4. **dream**: both dream entry points (`DreamCommand.Run`,
   `internal/app/dream.go runDream`) report an actionable error when the skill is
   off by default — name the setting (`skills.embedded_enabled: [dream]` or
   `/config → Skills → Embedded`) and state that a restart may be required —
   instead of the bare "dream skill not found".
   *Test*: `TestDreamCommand_SkillDisabledReportsHowToEnable` (no registry entry
   → message names the opt-in and the restart caveat) plus a positive test that
   the command runs when the skill is opted in.
5. **docs/config**: update the misleading comments that still say
   "all embedded skills except telegram" (`skills/loader.go`,
   `skills/loader_loading.go`, `internal/app/subsystems_skills.go`,
   `core/commands/config_skills.go`, `config/config_features.go`) and set
   `telegram.enabled: false` in the embedded default config so the shipped
   config no longer advertises the style injection as on.
6. **Gate**: `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
   `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (separately); then
   archive this entry per guideline 4 and commit.

## The goal tool is disabled by default
Observed: `tools.enabled.goal` is absent from the embedded default config
(`config/configs/default.yaml`, `tools.enabled` block), so
`ToolEnabledConfig.Goal` is false and `registerGoalTools`
(`internal/app/subsystems.go:328-331`) builds the goal tool with
`createFlagOn: cfg.Tools.Enabled.Goal || opts.Goal` = false → autonomous goal
`create` is rejected unless the user edits config. `ConfigurableTools()`
(`tools/registry.go:139`) reports `{goal, Default: false}` and the
`/config:set tools.enabled.goal` completion text says "enable goal tools
(default false)" (`core/commands/config_completion.go:140`).

Expected: the goal tool is enabled by default — the model can create goals with
no config edit — with `/config`, `/tools:goal:off` and the config cascade able
to turn it back off.

### Fix plan (test approach + validation)
1. **config**: add `goal: true` to the embedded default
   `tools.enabled` block, keeping the opt-IN flag mechanics untouched.
   *Test*: `TestDefaultConfig_GoalToolEnabledByDefault` — cascade load of the
   shipped defaults yields `cfg.Tools.Enabled.Goal == true` (and a project with
   `goal: false` still overrides it).
2. **registry/docs**: flip `ConfigurableTools()` goal entry to `Default: true`
   and the `/config:set tools.enabled.goal` help/completion text, so
   `/config → Tools`, `/docs` and `/tools:goal` agree with the shipped default.
   *Test*: `TestConfigurableTools_GoalDefaultMatchesEmbeddedConfig` — assert the
   `Default` field equals the value the embedded default config loads.
3. **make the gate live, not captured**: `newGoalTool` currently captures the
   flag by value (`internal/app/subsystems_goal.go`), so flipping
   `tools.enabled.goal` in-session leaves the registered tool with a stale
   `false` and `create` keeps failing (see the next entry). Change
   `newGoalTool`/`registerGoalTools` to take `createFlagOn func() bool`, wired to
   the live config (`func() bool { return cfg.Tools.Enabled.Goal || opts.Goal }`)
   and to `makeGoalToolRuntime`.
   *Test*: `TestGoalTool_CreateGateFollowsLiveConfig` — build the tool with a
   live flag callback, flip the underlying config from false to true, assert
   `create` succeeds without re-registration.
4. **Gate**: same separate checks as above; archive + commit.

## Tools are disabled "out of the blue" (unsynchronized registry + lost deferred loads)
Observed, two independent defects:

- **C1 — `tools.ToolRegistry` has no synchronization.** `tools/registry.go`
  keeps a plain `map[string]agentic.Tool` behind `Register` / `Unregister` /
  `RegisterGroup` / `UnregisterGroup` (writers: the TUI goroutine through
  `/config` and `/tools`, MCP connect/disconnect, plugin load) while `All()` /
  `Get()` / `AllDocumented()` iterate the same map from other goroutines. The
  agent path reads the live registry on every request: `ToolSearchTool.Schema()`
  and `ExecuteWithResult` call `deferredTools()` → `t.reg.All()`
  (`tools/tool_search.go:181-202`), and `All()` itself calls `Schema()` on every
  registered tool. Concurrent write + iterate/lookup is a data race; a lost
  `Register` (or a group unregister racing a re-register) shows up as a tool that
  was available disappearing.
- **C2 — a tool-set update discards the deferred loaded-tail.**
  `Agent.SetTools` rebuilds the agent-side registry
  (`internal/agentic/agent_config.go:110-111` → `NewToolRegistry(tools)`), which
  throws away the append-only loaded-tail (`loadedOrder` / `loadedSchemas`,
  `internal/agentic/tool_registry.go:224-249`). Any runtime tool-set push
  (`/config` or `/tools` toggle → `refreshToolRegistry`, MCP register/unregister
  → `core/commands/mcp.go:443`, plugin load) silently reverts tools the model had
  loaded with `tool_search` to "deferred, not loaded", so the next call is
  answered by the deferred-status redirect instead of executing the tool.

Expected: registry reads and writes are safe from any goroutine, and a tool-set
update preserves the loaded tail (append-only, provider-cache stable) so tools
the model loaded stay callable and no tool silently disappears.

### Fix plan (test approach + validation)
1. **C1** — make `tools.ToolRegistry` concurrency-safe: an `RWMutex` guarding
   the tool/doc maps and the group list, with **snapshot-then-release** reads
   (`All`, `AllDocumented`, `Match`, `Get`, `Register`, `Unregister`,
   `RegisterGroup`, `UnregisterGroup`). `All()` must copy name→tool under the
   read lock and call `Schema()` only after releasing it — `ToolSearchTool` is
   itself registered, and its `Schema()` re-enters `All()` (a non-reentrant lock
   must never be held across `Schema()`).
   *Test*: `TestToolRegistry_ConcurrentRegisterAndAll` — N goroutines
   registering/unregistering disposable tools while others call `All()`/`Get()`;
   must be clean under `-race` (RED before the fix: race report). Plus
   `TestToolRegistry_AllIsSnapshot` (a `Register` during iteration is not visible
   to the in-flight `All`, the next call sees it) and
   `TestToolRegistry_AllWhileSchemaReenters` (a registered tool whose `Schema()`
   calls `All()` does not deadlock).
2. **C2** — preserve the deferred loaded-tail across `Agent.SetTools`: expose
   `LoadedDeferred() []string` on `ToolLookup`/`ToolRegistry` (append order) and
   re-apply it to the freshly built registry in `Agent.SetTools` (unknown /
   no-longer-deferred names are skipped by `LoadDeferred`, so a tool that was
   genuinely removed is not resurrected).
   *Test*: `TestAgentSetTools_PreservesDeferredLoadedTail` — registry with
   deferral active, `LoadDeferred(["x"])`, then `SetTools(same set)` →
   `DeferredStatus("x")` reports loaded (callable) and `Schemas()` still ends
   with x's schema; a removed tool is not restored.
3. **Gate**: `go test -count=1 -race ./tools/... ./internal/agentic/...` plus the
   separate vet/staticcheck/gocognit/gocyclo/race-cover runs; archive + commit.

## Enabling a tool during a session does not enable it
Observed: `/config → Tools → <name>` (`core/commands/config_tools.go:73-101`)
flips `tools.enabled.<name>` and saves it, but `applyToolToggle` only acts in the
**disable** direction (`if !enabled { return }` → `Unregister`). Turning a tool
ON therefore registers nothing: `/config` reports it enabled while the registry
never contains it, so the model's call fails ("unknown tool") even though the
next session has it. The slash path `/tools:<name>:on`
(`core/commands/docs.go toggleTool` → `enableRuntimeTool`, docs.go:414-427) does
consult `ctx.ToolFactory`, so the two surfaces disagree. Two further holes in the
same area:

- the registered goal tool captured `createFlagOn` at construction
  (`internal/app/subsystems_goal.go`), so even after `/tools:goal:on` flips the
  config, a tool built while the flag was false keeps `create` blocked until the
  process restarts;
- `refreshToolRegistry` pushes `ToolRegistry.All()`
  (`core/commands/docs.go:436-440`), not the mode-filtered set the session was
  started with (`filterToolsForCurrentMode`, `internal/app/prompt.go:515`), so a
toggle can advertise tools the active mode disallows — and any push it performs
  re-partitions the tool set.

Expected: one shared enable/disable primitive used by `/config → Tools`,
`/tools:<name>:on|off` and `/docs …:on`; enabling constructs and registers the
tool, pushes the same tools the session would start with, and when a tool cannot
be built at runtime the user is explicitly told a restart is required (no silent
no-op); the command path and the `/config` menu always agree.

### Fix plan (test approach + validation)
1. **core/commands**: move `enableRuntimeTool` / `disableRuntimeTool` /
   `refreshToolRegistry` into a single helper set shared by both surfaces and
   call them from `toolToggleHandler`, deleting `applyToolToggle`'s
   disable-only logic. Report the outcome (registered vs "restart required").
   *Test*: `TestConfigMenu_EnableToolRegistersIt` — menu context with a stub
   `ToolFactory` returning a disposable tool: toggle OFF→ON registers the tool in
   the registry, pushes it to the agent (`AgentManager.SetTools` spy) and the
   message contains no restart claim; toggle ON→OFF unregisters and calls
   `ToolTeardown`; `TestConfigMenu_EnableToolWithoutFactorySaysRestart` — nil
   factory → the user is told a restart is required.
2. **mode-filtered push**: add a live tool-set provider to `core.Context`
   (e.g. `LiveTools func() []agentic.Tool`) wired by `internal/app` to
   `filterToolsForCurrentMode(subs, subs.toolRegistry.All())`;
   `refreshToolRegistry` uses it when set (fallback: `ToolRegistry.All()`).
   *Test*: `TestRefreshToolRegistry_UsesModeFilteredProvider` (provider called,
   its slice pushed verbatim) + `internal/app` test asserting the wired provider
   equals the session-start filter (`filterToolsForCurrentMode`).
3. **goal liveness**: covered by the goal entry's item 3 (live `func() bool`
   callback) — verified here end-to-end: `/config` goal ON → model `create`
   succeeds in the same session.
   *Test*: `TestGoalToolEnabledLive_ConfigMenuPath`.
4. **coverage for the remaining configurable tools**: extend `makeToolFactory` so
   the tools that are re-enablable are actually constructible (verify, run_code,
   webfetch, ask_user_question + its clarify hook); keep `smartsearch` on the
   documented "needs restart" path and assert that message.
   *Test*: table test over every `ConfigurableTools()` name → factory either
   builds a tool with a matching schema name or the caller reports "restart
   required".
5. **Gate**: separate vet/staticcheck/gocognit/gocyclo/race-cover runs; archive +
   commit.

## Stall warning / auto-retry timing: 15s–30s must become 30s–45s, and both values must be visible + editable from /config
Observed: with a silent provider the user sees exactly
`provider quiet for 15s — still waiting; will auto-retry after 30s of silence`.
Single producer: `internal/agentic/agent_streaming.go:422-427` (`emitQuietWarning`),
arming `quietAfter := stallTimeout / 2` (line 367) while `stallTimeout` comes from
`effectiveEventStallTimeout(opts)` = `opts.IdleTimeout` (lines 226-232), which
`provider/manager_streamopts.go:43-48` fills from
`execution.activity_timeout` — shipped default `"30s"`
(`config/configs/default.yaml`). So the notice fires at 15s and the stall
watchdog (auto-retry) at 30s, and a user who finds 15s too eager has no way to
change it:

- the 15s is a hard-coded `stallTimeout/2` — no config key feeds it;
- `execution.activity_timeout` has no `/config:set` setter
  (`core/commands/config_cli_setters.go`) and no `/config` menu entry, so it is
  neither visible nor editable from `/config` — and a mid-session change would
  not reach the running agent either (`BuildStreamOptions` is only sampled at
  session start, `internal/app/prompt.go:512`).

Expected: default 30s warning / 45s auto-retry — i.e.
`provider quiet for 30s — still waiting; will auto-retry after 45s of silence` —
with BOTH values visible and editable via `/config → Retry settings` and
`/config:set`, persisted through the config cascade, and applied to the running
session without a restart.

### Fix plan (test approach + validation)
1. **config**: add `execution.activity_warn_after` (duration string, default
   `"30s"`, pairs with `activity_timeout`) to `ExecutionConfig`, the embedded
   defaults (`activity_timeout: "45s"`, `activity_warn_after: "30s"` with
   comments), `mergeExecution` and `Validate` (unparseable value rejected; when
   both are set the warning must be shorter than the retry window).
   *Test*: `TestDefaultConfig_StallTimingDefaults` (cascade load → 45s/30s),
   `TestValidate_ActivityWarnAfter` (bad duration rejected, warn ≥ timeout
   rejected), `TestMergeExecution_ActivityWarnAfter` (home/project override).
2. **provider**: `schema.StreamOptions` gains `ActivityWarnAfter` and
   `BuildStreamOptions` parses `execution.activity_warn_after` next to the
   existing `IdleTimeout` wiring.
   *Test*: extend `provider/manager_activity_timeout_test.go` —
   `activity_warn_after: "30s"` → `opts.ActivityWarnAfter == 30s`, invalid value
   ignored, empty leaves 0 (agent derives the default).
3. **agentic**: `effectiveStallWarnAfter(opts)` returns `opts.ActivityWarnAfter`
   when it is > 0 and strictly below the stall window, else two thirds of the
   stall window (the shipped 30s of 45s, and still proportional for a 2-minute
   fallback window); `consumeStream` arms the quiet timer with it and the
   message keeps printing both real values.
   *Test*: `TestStallWarnAfter_DefaultsToTwoThirdsOfWindow` (45s → 30s),
   `TestStallWarnAfter_ExplicitOverride` (10s),
   `TestStallWarnAfter_IgnoredAtOrBeyondStallWindow` (fallback),
   `TestQuietWarningMessage_ByDefaultReports30sAnd45s` (asserts the exact
   user-visible string with the shipped defaults) and the existing
   `agent_quiet_warning_test.go` cases updated to the new threshold (the F5
   regression still passes: a silent stream warns before the watchdog acts, a
   paced stream stays quiet).
4. **core/commands**: `/config:set execution.activity_timeout` and
   `execution.activity_warn_after` setters (`setStringWithValidate` → duration
   check), `/config` completion entries, and — so the change is not another
   "toggle that does nothing" — a `syncRuntimeConfig` case pushing
   `ctx.AgentManager.SetStreamOptions(ctx.ProviderManager.BuildStreamOptions())`
   for both keys (shared with the existing provider idle/retry-cap entries).
   Menu: two new `/config → Retry settings` entries (labels show the current
   values) that prompt for a duration.
   *Test*: `TestConfigSet_ActivityWarnAfterAppliesAndPersists` (setter stores the
   value, saver called, live agent's StreamOptions updated — spy on
   `SetStreamOptions`), `TestConfigSet_ActivityTimeoutPushesLiveOptions`,
   `TestRetrySettingsMenu_ShowsStallTiming` (both entries present with the
   configured values), completion test for both keys.
5. **docs**: document both keys where the stall/watchdog behaviour is described
   (embedded doc for execution/limits) so the 30s/45s default is discoverable.
6. **Gate**: `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
   `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (separately);
   archive + commit.
