# Plugin Hooks (notify/intercept), Confirm API & Quota Resets — Implementation Plan

> **Goal:** Plugins gain three capabilities, sequenced so the risky parts land last:
>
> 1. **Confirm/popup API** (`goa.ui.confirm`) — plugins can ask the user a blocking,
>   styled question instead of abusing two-step commands.
> 2. **Quota plugin resets** — `/quota` surfaces reset-credit *details* and can
>   *consume* a Codex rate-limit reset credit through the confirm API.
> 3. **Plugin hook system** — plugins register handlers at key agent-loop points
>   as `notify` (read-only, async, zero-latency) or `intercept` (synchronous,
>   may modify/deny). External plugins must have their exact hook list accepted
>   by the user at install time.
>
> Plus the small core enabler: `rate_limit_exceeded` emitted on the plugin event
> bus so the quota plugin can hint "you have N resets" when limits are hit.
>
> Branch: **`feature/plugins`**. Reference implementations studied: OpenAI Codex
> `rate-limit-reset-credits` (backend-client → app-server → TUI state machine)
> and goa's own `internal/hooks` (shell) + `internal/agentic/provider/hooks`
> (ordered pipeline) packages.

---

## 0. Guiding constraints

- **Hard rule: conversations are append-only.** Hooks intercept payloads
  *before* they enter history/persisted state. Nothing ever rewrites a message
  that was already appended. Every interception site below is placed strictly
  upstream of the append.
- **Zero overhead when unused.** A session with zero registered hooks pays one
  nil-check per call site. The agent hot path (stream deltas!) must not slow
  down measurably — benchmarks in M2 gate the merge.
- **Notify is async, intercept is sync.** Notify handlers run on scheduler
  goroutines against a payload *snapshot* — they can never stall the agent
  loop. Intercept handlers block the loop under a hard timeout; timeout ⇒
  pass-through unchanged (availability beats enforcement; these are
  user-installed plugins, not a security boundary).
- **Dependency direction.** `internal/agentic` must not import `plugins`.
  Define a small interface (`PluginHookSink`) in `agentic`; `internal/app`
  injects the adapter backed by the plugin registry (same pattern as
  `cfg.HookEngine` for shell hooks).
- **Every change ships a test** that would have caught its absence. Gates:
  `go vet ./...`, `go test -count=1 -race -cover ./...`, `gocognit -over 15`,
  `gocyclo -over 12`. Unit tests <100ms, packages <5s.
- **Complexity budget:** hook-chain folding logic ≤15 gocognit / 12 gocyclo;
  split fold/ordering/enforcement into separate files if needed.
- Docs live in `docs/PLUGINS.md` (user reference) — updated in M6, not per-step.

---

## 1. Target architecture

```
                    ┌──────────────────────────────────────────────────┐
                    │ internal/app (wiring, owns both sides)           │
                    │                                                  │
 Agent cfg          │  ┌───────────────┐      ┌─────────────────────┐  │
 PluginHookSink ◄───┼──┤ pluginHook    │◄─────┤ plugins.HookRegistry│  │
 (interface in      │  │ adapter       │      │ (ordered chains per │  │
  agentic pkg)      │  └───────────────┘      │  point, modes)      │  │
                    │                          └──────────▲──────────┘  │
                    │                                     │ register    │
                    │  ┌───────────────┐      ┌───────────┴──────────┐  │
 TUI confirm ◄──────┼──┤ ConfirmBridge ├──► goa.ui.confirm (JS)          │
 request/response   │  │ (chan-based)  │      goa.registerHook (JS)      │
                    │  └───────────────┘                                 │
                    └──────────────────────────────────────────────────┘
                              ▲                ▲
   agent loop call sites:     │                │
   runInternal (msg pre-send) │   EventBus("rate_limit_exceeded", ...)
   executeToolWithResult      │   (retry classify → forwarder → app)
   handleStreamTextDelta      │
   completeStreamTurn         │
   handleStreamError/Failure  │
```

**Key decisions**

1. **Payloads cross the VM boundary as plain JSON objects** (marshal Go struct →
   `map[string]any` → goja object; intercepted result unmarshalled back).
   No goja value aliasing, no shared mutable state, trivially versionable.
2. **Chain semantics:** for a given point, `intercept` handlers run first in
   priority order (ascending, stable by registration), each receiving the
   previous handler's output; then `notify` handlers receive the *final*
   payload asynchronously. Observers therefore always audit what actually took
   effect.
3. **Deny protocol:** an intercept handler returns `{deny:true, reason}` —
   mapped per-point (tool pre ⇒ tool-result error the model sees; message
   pre-send ⇒ user-facing rejection, turn not started; reply pre ⇒ replaced by
   reason text? — no: reply cannot be denied, only modified; see §5.4).
4. **Manifest-declared hooks + install grant.** `plugin.yaml` gains a `hooks:`
   section. External (non-bundled) plugins registering hooks must have every
   declared `(point, mode)` pair accepted by the user; the grant is stored and
   re-prompted when the declaration changes. Runtime rejects undeclared
   registrations (plugin keeps running, hook dropped, warning surfaced).

---

## 2. Phase M1 — Agent-side seam (`internal/agentic`) — no behavior change

**Purpose:** create the injection points with a nil-safe interface so every
later phase is independently shippable.

### Micro steps

1. **New file `internal/agentic/plugin_hooks.go`:**
   - `type HookPoint string` with constants:
     `HookMessagePreSend`, `HookToolCallPre`, `HookToolCallPost`,
     `HookReplyPre`, `HookReplyDelta`, `HookLLMError`.
   - `type HookDecision int` (`HookPass`, `HookModified`, `HookDenied`).
   - ```go
     // PluginHookSink receives interception points from the agent loop.
     // Implementations MUST be safe for concurrent use and MUST treat
     // payload maps as owned by the caller after the call returns.
     // result may be nil when decision != HookModified.
     type PluginHookSink interface {
         Intercept(ctx context.Context, point HookPoint, payload map[string]any) (decision HookDecision, result map[string]any, denyReason string)
         Notify(point HookPoint, payload map[string]any) // async, never blocks
     }
     ```
   - `func (c *Config) SetPluginHookSink(s PluginHookSink)` or a `Config` field
     `PluginHookSink` (match however `cfg.HookEngine` is declared in
     `agent_config.go` — follow that precedent exactly).
2. **Call-site: message pre-send** — `agent_run.go` `runInternal`: after the
   queue pop assigns `currentInput`, before `userMsg` construction:
   - Build payload `{"point":…, "role":"user","text":currentInput,"images":[…],"metadata":{…},"session_id","turn":a.turnCounter}`.
   - If `Intercept` returns denied ⇒ emit a user-visible event (reuse
     `emitFlash`-style path used elsewhere in agentic; if none, `OutputEvent`
     with `EventContent` role System is acceptable) and `continue` the input
     loop WITHOUT appending anything.
   - If modified ⇒ replace `currentInput` (and metadata/images if changed) —
     the append below stays untouched, satisfying append-only.
3. **Call-site: tool call pre/post** — `agent_tools.go` `executeToolWithResult`:
   - Pre: BEFORE `fireBeforeToolHook` (shell hooks see the possibly-mutated
     input too — document why: shell vetoes validate the final input).
     Denied ⇒ return `ToolResult{Output: "tool \"x\" blocked by plugin hook:
     <reason>"}` with `err = fmt.Errorf(...)` matching existing veto style so
     the model sees reasons.
     Modified ⇒ local `input` variable reassigned; thread it into `runTool`
     (signature already takes `input` — no signature change needed, just use
     the mutated local).
   - Post: AFTER `runTool` returns, BEFORE returning to caller (the caller
     appends the result to history — pinpoint the append site near
     `agent_tools.go:116` and assert ordering in a test).
     Payload includes `{"output":result.Output,"error":errStr,"stop_turn":result.StopTurn…}`
     — flatten the `ToolResult` fields actually consumed downstream.
     Modified ⇒ rebuild `ToolResult` from returned map.
4. **Call-site: reply delta** — `agent_stream_events.go` `handleStreamTextDelta`:
   - Payload `{"delta":text,"is_delta":true,"state":"content"}`.
   - Intercept MAY rewrite the delta. Guard rail: if ANY intercept handler is
     registered for `HookReplyDelta`, the agent sets a per-turn flag; the plan
     explicitly accepts the VM-call-per-delta cost here ONLY because the
     fast-path (no handler) is a nil/interface-nil check, and M2 adds a
     per-turn circuit breaker (§4 step 9).
   - Thinking deltas: **notify-only** (reasoning redaction is a legit notify
     use case; rewriting thinking risks breaking providers' reasoning-signature
     verification — document this restriction).
5. **Call-site: reply pre** — locate the single place the finished assistant
   `Message` is appended to `a.history` after a completed stream round
   (`completeStreamTurn` in `agent_stream_content.go` — first M1 task is to
   pin this down with a grep and record it in the test as the ordering anchor).
   Payload: full final text (+ tool-call summary counts). Intercept may rewrite
   text; denial is NOT supported here (an assistant turn cannot be unsaid —
   return `HookDenied` handling: log + treat as pass; document).
6. **Call-site: LLM error** — `handleStreamError` and `handleStreamFailure`
   (`agent_stream_retry.go`): payload `{"error":err.Error(),"attempt":n,
   "model":model.ID,"classified":"rate_limit|overloaded|auth|network|…",
   "will_retry":handled,"next_delay_ms":…}`. v1: **notify-only semantics even
   for intercept-mode registrations** (the sink wrapper downgrades; §4 step 8)
   — mutating retry policy is deferred (§8 open questions).
7. **Unit tests (`plugin_hooks_test.go`):** table-driven — for each point: nil
   sink = unchanged behavior (golden on emitted events); sink returning
   Pass/Modified/Denied produces the documented outcome; append-only invariant
   asserted by comparing history length/content before/after denied inputs.
   Reuse existing agent test fixtures (`agent_loop_test.go` patterns).
8. **Gate:** `go test -count=1 -race ./internal/agentic/` green; benchmarks
   untouched paths within noise (`-bench=Stream` if present, else skip).

---

## 3. Phase M2 — Plugin hook registry + JS API (`plugins/`)

### Micro steps

1. **New file `plugins/hooks_registry.go`:**
   - ```go
     type HookMode string // "notify" | "intercept"
     type HookSpec struct {
         PluginID string; Name string; Point string; Mode HookMode
         Priority int // ascending; ties broken by registration order
     }
     type HookEntry struct {
         Spec    HookSpec
         Handler func(payload map[string]any) map[string]any // runs UNDER vm lock
     }
     type HookRegistry struct { mu sync.RWMutex; chains map[string][]HookEntry }
     ```
   - Methods: `Register(spec, handler) error` (dup name per plugin ⇒ error),
     `UnregisterPlugin(pluginID)`, `Snapshot(point) []HookEntry` (copy-on-read),
     `HasInterceptors(point) bool`, `Count() int`.
   - Enforcement hook-up: `Register` consults an `allow func(HookSpec) error`
     validator injected by the manager (nil validator = allow; M6 installs the
     manifest/grant validator).
2. **Adapter `plugins/hook_sink.go`** implementing `agentic.PluginHookSink`:
   - `Intercept`: snapshot chain; for each entry: marshal current payload →
     `runOutsideVMLock`-free call — handler executes under `lockVM()` (goja
     single-goroutine rule, mirroring `buildToolWrapper`); wrapped in
     `context.WithTimeout(perHandlerTimeout)`. Because goja calls can't be
     preempted, the timeout is enforced by *skipping* further handlers once
     expired and recording a violation (see step 9), plus each handler's wall
     time checked before invocation (don't start a new handler with <5ms
     budget left).
   - Folding: start `decision=Pass`; handler result `undefined` ⇒ keep;
     object ⇒ shallow-merge into working map, decision=Modified;
     `{deny:true,reason}` ⇒ short-circuit, decision=Denied.
     Complexity guard: fold loop ≤15 gocognit — extract `applyOne()` helper.
   - Then notify chain: goroutine per notification is wasteful — reuse the
     Scheduler: `scheduler.SetTimeout(func(){ … }, 0)` gives the existing
     timer-goroutine + lockVM machinery for free (minInterval clamp does NOT
     apply to setTimeout(0)? verify `Scheduler.start` — if clamped, add a
     `SetImmediate(cb)` method with zero floor; prefer adding it, cleaner than
     fighting the clamp).
   - Panics/throws in handlers: recover → log warn with plugin/name, treat as
     Pass-through for that handler.
3. **JS API in `bridge_extended.go` (new `setupHooks(goaObj)`):**
   - ```js
     goa.registerHook({
       name: "redact", point: "tool-call:pre", mode: "intercept",
       priority: 10,
       handler: function(p) {
         if (/AKIA[0-9A-Z]{16}/.test(p.input)) {
           return { input: p.input.replace(/AKIA[0-9A-Z]{16}/g, "[REDACTED]") };
         }
         // return undefined = pass-through
         // return {deny:true, reason:"no aws calls"} = deny
       }
     });
     ```
   - Validate args: unknown point/mode/bad handler ⇒ return descriptive error
     string (same convention as `registerCommand`). Unknown-but-well-formed
     point strings are rejected against the constant list (typo protection).
4. **Point-id ↔ agentic constant map** lives in the adapter
   (`"message:pre-send"` ⇔ `agentic.HookMessagePreSend`, etc.) — single map,
   tested exhaustively so a rename breaks a test, not production.
5. **Wiring in `internal/app/plugins.go`:** `pluginRegisterHook(rt)` handler →
   registry; extend `pluginContext()` with the registry; construct the sink
   adapter once per runtime; after each plugin load/unload, recompute the
   sink handed to agents: agents get the *adapter*, which reads the live
   registry — so a single long-lived adapter suffices (agents hold it via
   cfg; registry swaps are visible immediately). Locate where Agents are
   constructed (`AgentManager` factory in `core/`) and inject; if Agent cfg is
   built before plugins load, ensure the adapter exists from boot (empty
   registry = no-op).
6. **Perf instrumentation:** counter + rolling max of handler wall times per
   point exported via `goa.logger().debug` and a debug command later; M2 gate:
   benchmark with 1k synthetic deltas, 0 hooks vs 1 notify vs 1 intercept —
   notify path must be indistinguishable from baseline (async!), intercept
   documented at ~VM-call cost.
7. **Tests:** `hooks_registry_test.go` (ordering, dup names, unregister,
   concurrent Snapshot/Register under `-race`), `hook_sink_test.go` (fold
   semantics incl deny short-circuit, timeout skip, panic recovery, JSON
   round-trip fidelity for nested maps), `hooks_bridge_test.go` extending
   `quota_harness_test.go`'s env pattern: load a fixture JS plugin registering
   hooks, drive payloads, assert mutations/denies land. Table-driven.
8. **Downgrade rule (llm-error):** sink wraps `Intercept(HookLLMError)` — if
   any intercept entries exist for that point, they run BUT their return value
   is discarded except a `note` field which is appended to the error text shown
   to the model (mirrors `additionalContext` from shell hooks). Documented as
   v1 contract; §8 lists full retry-control as future.
9. **Reply-delta circuit breaker:** adapter counts intercept invocations per
   turn per point; >500 (configurable const) ⇒ remaining deltas bypass hooks
   for that turn + one warn log. Protects pathological JS from doubling turn
   latency.
10. **Gate:** full package suite + `-race`; `make lint` equivalents
    (`golang-check` skill) clean on touched files.

---

## 4. Phase M3 — Confirm/popup API (`goa.ui.confirm`)

**Design:** channel-based request/response. JS calls `goa.ui.confirm(spec)`
from any context (command, timer, hook handler); the bridge releases the VM
lock while waiting (`runOutsideVMLock` — precedent: `setupOAuth` tokenFn), the
TUI renders a modal selection card, the answer flows back over a reply channel
with timeout. Deadlock analysis: TUI never calls INTO the VM while holding the
confirm reply channel — rendering reads cached defs only (same invariant as
segments today).

### Micro steps

1. **`plugins/ui_bridge.go`:** add
   ```go
   type ConfirmOption struct { ID, Label, Style string } // style: ok|danger|default
   type ConfirmRequest struct {
       PluginID, Title, Body string
       Options []ConfirmOption  // ≥1; default cancel appended by TUI if AllowCancel
       DefaultID string        // initial cursor
       AllowCancel bool
       Timeout time.Duration   // 0 = no timeout (cap 5m)
   }
   type ConfirmResponse struct { ID string; Cancelled bool; Err string }
   func (b *UIBridge) RequestConfirm(req ConfirmRequest) <-chan ConfirmResponse
   ```
   Buffered channel (size 1); `pending map[int64]chan ConfirmResponse` under
   the existing mutex; IDs monotonic.
2. **TUI consumer:** new small component `tui/confirm_card.go` modeled on
   `clarify_card.go` (closest existing analog): renders title/body/options,
   arrow/enter/esc navigation, danger options styled with theme "critical"
   color via `ansi` package. App-side glue in `internal/app/tui.go`: drain
   `ui.ConfirmRequests()` (new accessor exposing the pending queue) on the
   event loop; on user action, resolve the channel. Esc ⇒ Cancelled.
   Modal takes input focus like existing selection popups; queued confirms
   serialize FIFO (one visible at a time).
3. **Headless/no-TUI policy:** if the app runs without TUI (tests, `-e` script
   mode if applicable — verify how headless is detected in `internal/app`),
   `RequestConfirm` resolves immediately `{Cancelled:true, Err:"no-ui"}` —
   fail-closed, callers must handle cancellation.
4. **JS binding** in `setupUI`: `goa.ui.confirm({title, body, options:[…],
   defaultId, allowCancel, timeoutMs})` → returns `{id}` | `{cancelled:true}`
   | `{error}`. Blocking wait implemented with a select on the response chan +
   `time.After(timeout)` INSIDE `runOutsideVMLock` so other plugins/timers
   proceed meanwhile. Re-entering the VM after wake-up uses the standard
   `lockVM` path.
5. **Security note (doc'd, enforced in M6):** confirm is *display + choice*;
   option IDs are opaque strings back to the plugin. No capability grant, but
   M6 makes it a declared permission `"ui-confirm"` so external plugins must
   list it.
6. **Tests:** `confirm_bridge_test.go` — request/response happy path, cancel,
   timeout, FIFO serialization, no-TUI immediate cancel, VM-lock released
   while waiting (asserted via a concurrent timer firing during a pending
   confirm). TUI: `confirm_card_test.go` render golden + key handling, plus
   one `compositor`-level regression like `tui/compositor_quota_stream_repro_test.go`
   proving confirm-during-stream doesn't corrupt frames.

---

## 5. Phase M4 — Quota plugin: reset details + consume

Follows the Codex contract exactly (`redeem_request_id` idempotency, outcomes
reset|nothing_to_reset|no_credit|already_redeemed, count-vs-details
degradation).

### Micro steps

1. **`lib/http-quota.js`:** add `postJSON(url, headers, bodyObj, onBody)`
   beside `getJSON`, reusing the identical error vocabulary
   (`auth_required` on 401/403, `http_<status>`, `bad_response`).
2. **`fetchers/codex.js`:**
   - Extract `WHAM_BASE = "https://chatgpt.com/backend-api"`; derive USAGE_URL
     and the two new URLs (`/wham/rate-limit-reset-credits`,
     `/wham/rate-limit-reset-credits/consume`).
   - Shared `codexHeaders(token)` builder (Authorization, Accept, UA,
     ChatGPT-Account-Id) — today inlined in `fetch()`.
   - `resetCredits()`: GET details endpoint → tolerant map
     (`reset_type/status` unknown strings ⇒ "unknown"; RFC3339 ⇒ ms epoch;
     sort available-first by earliest expiry). Errors ⇒ return
     `{error}` and let the caller degrade to count-only (the count already
     arrives via usage `lines`).
   - `consumeReset(redeemRequestId, creditId?)`: POST `{redeem_request_id,
     credit_id}` → map `code` ⇒ outcome string; transport errors ⇒ `{error}`.
   - Export both for the harness tests (pattern: exported `mapUsage` today).
3. **Idempotency key:** module-scope `_pendingResetKey` — UUID-v4 from
   `Math.random` bytes (fine for redeem ids). Retained from first attempt until
   a terminal outcome (reset/already_redeemed/no_credit/nothing_to_reset);
   "try again" reuses it ⇒ server dedupes double-redeems (Codex parity).
   Cleared on terminal outcome.
4. **`plugin.js` commands:**
   - `/quota:resets` — force-refresh codex, then fetch details; render a
     markdown table (id-short, title, expiry, status) + count; errors ⇒
     count-only line (already exists).
   - `/quota:reset[:<credit-id>]` — lists what will happen, then calls
     `goa.ui.confirm` (danger styling on "Yes, use reset", default = Cancel —
     Codex-parity defaults). On confirm: acknowledge immediately
     ("Resetting your usage…"), do the POST inside `goa.setTimeout(…, 0)`
     (never block the command path — bugs.md precedent), then:
     - reset/already_redeemed ⇒ `delete _cache["codex"]` + `refreshDue("codex",
       true)` + success message with remaining count from refreshed entry +
       `goa.ui.refreshSegment("quota")`.
     - no_credit ⇒ cache count 0, message.
     - nothing_to_reset ⇒ informational.
     - transport error ⇒ offer "Try again (same request)" as a second confirm
       reusing the retained idempotency key.
   - Update `longHelp` text; bump plugin version 1.1.0 → 1.2.0.
5. **`plugin.yaml`:** `permissions: [provider-keys, oauth-token, account-write,
   ui-confirm]` (declarative; M6 enforces for external installs, bundled ships
   trusted).
6. **Tests** (extend `quota_harness_test.go` env — real plugin source in VM):
   - URL/payload contract test pinning paths + body shape (mirrors Codex's
     `rate_limit_resets_tests.rs`).
   - Outcome matrix table-driven: each backend code ⇒ expected message + cache
     effect; stale-picker/request-id cases not needed (no request-id scheme —
     our single-flight `_resetInFlight` boolean replaces Codex's u64 ids;
     document the simplification).
   - Idempotency retention: simulated transport error then retry asserts same
     `redeem_request_id` on the wire (captured http bridge).
   - Details degradation: details fetch error ⇒ count-only render still works.

---

## 6. Phase M5 — `rate_limit_exceeded` event → plugin hint

1. **`internal/agentic/observer.go`:** add `EventRateLimit EventType =
   "rate_limit"` + fields on `OutputEvent`: `RateLimit *RateLimitInfo`
   `{Model string; Attempt int; RetryAfterMS int64; Classified string}` (only
   populated for this type — follow `CompactionInfo` precedent).
2. **Emit site:** `handleStreamFailure`/`handleStreamError` after
   classification succeeds and a retry is scheduled (and once more with
   `will_retry:false` when giving up). Keep emission off the hot success path.
3. **Forwarding:** `core/agent_event_forwarder.go` passes events through
   already (verify switch — add passthrough if filtered); `internal/app`
   subscriber maps it to `rt.EmitEvent("rate_limit_exceeded", {provider,
   model, retry_after_ms, will_retry})` on the plugin bus (wildcard observers
   receive it).
4. **Quota plugin:** `goa.registerObserver(function(name, payload){ if
   (name==="rate_limit_exceeded" && payload.model is codex-ish) { force-refresh
   codex entry; if resets>0 && !hintShownRecently ⇒ goa.output("You have N
   rate-limit resets available. Run /quota:resets.") } })`. Debounce: module
   timestamp, ≥10 min between hints.
5. **Tests:** agentic unit (emission timing/count), app wiring test
   (`plugins_integration_test.go` pattern), plugin harness test (observer
   receives event, debounce honored).

---

## 7. Phase M6 — Install-time hook acceptance (external plugins)

1. **Manifest schema (`plugin.go` `PluginDef` + docs):**
   ```yaml
   permissions: [provider-keys, ui-confirm, account-write]
   hooks:
     - { point: tool-call:pre,  mode: intercept, description: "Redact AWS keys" }
     - { point: llm-error,      mode: notify,    description: "Log failures" }
   ```
   Parse + validate at discovery; malformed ⇒ plugin refused load (config
   error surface, consistent with existing manifest failures).
2. **Grant store:** `<managerRoot>/grants.json`:
   `{pluginID: {version, manifestHash, approvedHooks:[{point,mode}],
   approvedAt}}`. Key change trigger = hash of (sorted hook list) OR version
   bump ⇒ stale grant ⇒ re-prompt. File perms 0600 (match storage_bridge).
3. **Approval UX:** on enabling an external plugin whose manifest declares
   hooks (or ui-confirm/account-write), show ONE confirm-style review card:
   plugin name/version, then per-hook rows `[intercept] tool-call:pre — Redact
   AWS keys`, per-row toggle (default OFF for intercept, ON for notify —
   conservative defaults), accept-selected / reject-all buttons. Reuses M3's
   card component with checkboxes (small extension: `MultiSelect bool`).
4. **Enforcement:** the registry's `allow` validator (M2 step 1) checks
   `(pluginID, point, mode)` against the stored grant at `registerHook` time
   AND at every plugin reload; unapproved ⇒ registration rejected, warning via
   `goa.output` + logger, rest of plugin proceeds. Undeclared-in-manifest
   registrations are rejected outright regardless of grants.
   **Bundled plugins** (provider-quota) ship pre-approved (skip prompt).
5. **Headless:** no TUI ⇒ external hooks disabled entirely (grant flow can't
   run) unless `GOA_PLUGIN_HOOKS_APPROVED=1` env escape hatch (documented,
   loud log).
6. **Tests:** manifest parse table; grant staleness (hash/version change ⇒
   re-prompt); enforcement positive/negative via harness-loaded fixture
   plugin with manifest; headless fail-closed; bundled exemption.

---

## 8. Explicitly out of scope (recorded for later)

- Full intercept control of LLM retry policy (override verdict/delay) — v1
  keeps llm-error intercept limited to annotation (`note`).
- Thinking-delta interception (rewrite) — reasoning signatures break; notify
  only.
- Shell-hook (`internal/hooks`) interplay beyond ordering (plugin intercept
  runs before shell veto sees the input) — unifying both systems is a larger
  redesign.
- Cross-session persistence of plugin-modified payloads beyond normal history.

## 9. Open questions (decide before the phase starts)

1. `reply:pre` modification vs provider-side caches: rewritten assistant text
   diverges from what some tools (compaction provenance?) recorded — M1 step 5
   includes an audit of downstream consumers before finalizing placement.
2. Should `message:pre-send` also gate queued/driven inputs (goals, skills)?
   Default YES (single choke point in `runInternal` covers all) — confirm no
   path appends user messages without `runInternal` (orchestrator sub-agents
   use their own Agents ⇒ covered by their own sinks).
3. Confirm modal vs streaming contention — **DECIDED (M3)**: selector-style
   capturing overlay, FIFO-serialized one-at-a-time; NOT clarify-style and NOT
   queue-until-idle. Grounding (read from today's behavior): ClarifyCard is
   display-only because its answer is FREE TEXT typed on the main editor
   ("Input discipline", docs/TUI.md) — a confirm has no typing, so that model
   does not apply. Discrete choice already owns a focus-capturing precedent
   (`ShowSelector`, `CaptureInput` overlay + FocusStack exact restore); the
   compositor renders capturing-overlay frames mid-stream safely
   (`tui/compositor_confirm_stream_repro_test.go`). Queue-until-idle would
   delay plugin prompts behind arbitrarily long streams for no safety gain.
   Implementation notes/deviations recorded in the M3 commit and code:
   timers/hotkeys/segments/hooks defer while a confirm frame is live (item-E
   invariant: never two goja frames on one runtime — delayed-not-lost), plugin
   commands run async so the command loop stays free to serve the modal, and a
   block guard fails `goa.ui.confirm` closed if invoked on the UI thread.

## 10. Milestone order & ship criteria

| Phase | Ships | Gate |
|---|---|---|
| M1 | agentic seam, nil-safe, zero-behavior-change | agentic suite + race green |
| M2 | registry + JS API + adapter | perf benchmark + harness tests |
| M3 | confirm API | TUI regression + concurrency tests |
| M4 | quota details/reset (uses M3) | harness outcome matrix |
| M5 | rate-limit event → hint | end-to-end wiring test |
| M6 | install acceptance + enforcement | grant lifecycle tests |

Each phase is one PR-sized commit series on `feature/plugins`; M1–M2 unblock
nothing user-visible alone (safe landings), M3–M4 deliver the user-approved
feature, M5–M6 complete parity with the studied Codex design.
