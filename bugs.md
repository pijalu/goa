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

## Silent startup death: fatal config errors never reach the console (goa exits with no TUI, no message)
Observed: with a home config that fails load validation (see stall-pair entry
below), `goa` at HEAD `474d70c` exits in <100ms with rc=1, no TUI and NO visible
message (reproduced under a pty: 4 bytes of output). The real error — "Config
error: loading home config: ... execution.activity_warn_after (45s) must be
shorter than execution.activity_timeout (45s)" — only survives in
`<workspace>/.goa/crash.log`. Root cause: `setupCrashLog` re-points stderr into
an async tee (`teeStderr` in `internal/app/crash_log_unix.go`: pipe + drain
goroutine); the bootstrap fatal path prints the message to that tee and then
calls `os.Exit(1)`, killing the goroutine before it flushes to the real
terminal. `handleShutdown` already works around this for panics by writing
directly to the crash file (`writeCrashLog`), but ordinary fatal errors do not.
Expected: ANY startup fatal error is visible on the console (and in the crash
log) before the process exits. Fix: give the stderr tee a synchronous delivery
guarantee — add a `flushStderrTee()` (drain + wait) invoked by a single
`fatalExitf` helper used by every `os.Exit` path in `internal/app`
(bootstrap.go, app.go, mcp_cli.go), or make the tee write synchronously to the
original stderr. Test: unit test the tee (write → flush → bytes on the original
writer, no goroutine dependency) + a fatal-path test asserting the message hits
both crash file and original stderr; validate with a pty run of the built
binary against a bad `GOA_HOME` config asserting rc!=0 AND the message on the
pty.

## Contradictory stall-timing pair in one layer refuses to start — must autocorrect
Observed: commit `da8120b` added `checkActivityPairLayer` (config/loader_yaml.go):
when ONE cascade layer sets both `execution.activity_timeout` and
`execution.activity_warn_after` with warn >= timeout, config load returns an
error and goa refuses to start. A real ~/.goa/config.yaml (timeout 45s + warn
"45s", see next entry for how it got there) made every launch die silently.
The runtime itself already defines graceful semantics for at-or-above values
(`effectiveStallWarnAfter` falls back to 2/3 of the window; field doc says so),
so a hard startup refusal is disproportionate and breaks existing installs on
upgrade.
Expected: an explicit contradictory pair within one layer is AUTO-CORRECTED at
load — the layer's warn value is dropped (falls back to the cascade default,
e.g. the shipped 30s inside 45s) with a visible warning naming the file and the
correction — and startup proceeds. Fix: change the layer check from error →
sanitize+warn (mutate the layer config, log to console + crash log). Keep
invalid duration shapes as errors (Config.Validate). Test: table-driven loader
tests (valid pair / warn==timeout / warn>timeout / cross-layer combination):
load succeeds, warn corrected, warning surfaced; validate goa starts under a
pty with the user's real pair.

## /config writes configs it never validated (wrote the contradictory 45s/45s pair)
Observed: setting `execution.activity_timeout` (or warn) via /config persists
through `applyConfigSet` → `SaveHomeField` with NO pair validation — only the
single value's duration shape is checked. Setting stall timeout to 45s while
`activity_warn_after: "45s"` was already in the same file produced a home
config that later loads reject: goa wrote a config goa cannot start with.
Expected: goa must never write an unvalidated config. Two guards: (a) the
/config set path validates the stall pair on the merged in-memory config and
auto-corrects the warn value (same rule as load-time sanitization), flashing
what it corrected; (b) writer-level invariant in `saveField` (config/
loader_fields.go): keep the previous bytes, write, reload + validate the
written file, restore the old bytes and return an error if validation fails.
Test: setter test — set timeout equal to an existing warn → warn auto-corrected
and persisted pair valid; writer test — force an invalid pair → saveField
returns error and file content is byte-identical to before.

## Edit tool renderer drops file names (batch edits show "edit ...", result header hidden)
Observed: export goa-export-20260926-194900.zip. (a) The model calls edit in
batch form `{"edits":[{path, old_string, new_string, operation}]}` — top level
has NO `path` — so `EditFileRenderer.RenderCall` shows `✓ edit ...`.
(b) `RenderResult` renders only the first unified-diff hunk body:
`extractDiff`/`parseHunkHeader` discard everything before the `@@` header,
including the tool result's first line `[edit: creaves/TODO.md] 1 edits
applied — match: exact match (resolved: ...)` — so the file name appears
nowhere in the TUI for either the call or the result.
Expected: the call title shows the target path(s) (first path + "(+N more)" for
batch), and the result rendering keeps the `[edit: <path>] N edits applied —
match: ...` header line (muted) above the diff. Fix both in
tools/edit_renderer.go; table-driven renderer tests for batch call titles and
header-preserving result render; validate in the filmstrip/interactive shell
per guideline 5.

## Stall-timing values are displayed/entered with a glued unit ("60s" instead of "60")
Observed: the /config retry-settings labels and the stall-timing input prompts
prefill Go-duration strings ("45s", "2m0s", "30s"); the user reads/edits these
as plain seconds and expects "60", not "60s". Entering a bare number ("60") is
rejected by `validateDurationValue` (time.ParseDuration requires a unit), so
the input is formatted in a unit the user did not choose.
Expected: stall-timing UI speaks plain seconds — labels and prompt prefills
render whole-second values as bare numbers ("45", "120"), and the setters
accept a bare number as seconds ("60" → 60s) alongside explicit durations;
persisted values stay canonical ("45s"). Fix: format helper + unitless-parse in
the stall setters (config_menu_retry.go, config_cli_setters.go); table-driven
tests for label formatting and setter parsing ("60"→60s, "60s"→60s, "2m"→2m,
garbage→error).

## Clarification cards with options give no way to type a custom answer
Observed: `App.clarify` routes option-carrying questions exclusively to the
selector overlay (`clarifyWithOptionList`): arrow keys + Enter pick a listed
option, Esc cancels. The user cannot express an answer that is not one of the
proposed options (the free-text main-input path exists only for option-less
cards).
Expected: a clarification/user question ALWAYS allows the user to express
another option — the selector offers an explicit "type your own answer"
affix that opens the main input line for free text (Esc still cancels).
Fix: extend `clarifyWithOptionList` with a custom-reply entry routed through
`requestMainInput`; renderer/TUI test driving the selector → custom path and
asserting the typed answer reaches the waiting tool caller.

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
