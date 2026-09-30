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

Review evidence: `docs/research/zai-connection-review-20260930.md`
(two diagnostic bundles, `zai`/`glm-5.3-flash` and `opencode-go`/`space-bunny-free`).

---

## 1. Stall watchdog discards already-complete streams (CRITICAL)

**Observed.** A provider streams HTTP 200 and the full answer, then holds the SSE
connection open without ever sending `[DONE]` or a `finish_reason` chunk. Goa
waits `execution.activity_timeout` (60s in `~/.goa/config/user.yaml`), declares
a stall, throws the completed answer away, and replays the whole turn.

Measured on export B: last content byte `23:09:57.881` → stall kill
`23:10:57.882` = 60.001s, then `retry scheduled attempt=1/15`. Request 19
shows `status 200`, `finishReason null`, no `[DONE]`, no `finish_reason`.
Export A reproduces the same signature (requests 19/20/21, 66s/78s/in-flight).

**Expected.** A stream that has delivered a complete response and is merely
holding the socket open is a soft end-of-stream, not a stall. The turn must be
finalized from what was received. Productive silence (model still thinking or
still streaming) must keep waiting.

**Fix plan.**
1. Reproduce with an `httptest` SSE server that writes content chunks then holds
   the connection open (no `[DONE]`). Assert current behaviour: 60s stall →
   `undoLastAssistantMessage` → retry.
2. Distinguish post-answer silence from mid-answer silence. Track whether the
   accumulator reached a terminal condition (finish_reason seen, or content
   flushed with no tool call pending) while the socket stayed open.
3. On terminal-without-terminator, finalize the turn from buffered content and
   emit a warning that names the provider's missing `[DONE]` — do not retry.
4. Keep the existing stall behaviour for genuinely productive silence.
5. Bound it: only treat as complete when a finish_reason was seen, or when
   content ended and no tool call is pending. Never finalize an empty turn.

**Validation.** The new test completes without a retry and with the content
intact. Existing stall tests (`internal/agentic/idle_timeout_test.go`,
`agent_streaming` watchdog tests) still pass.

---

## 2. `EventStart` is unmapped — no watchdog re-arm + WARN on every request (MEDIUM)

**Observed.** `streamAccum.ensureStarted()` pushes `schema.EventStart` as the
first event of every OpenAI-completions stream, but `provider.EventStart` is
absent from `streamEventHandlers`
(`internal/agentic/agent_stream_events.go:15-23`). It is therefore classified
unmapped: it does not re-arm the stall watchdog, and it logs
`provider sent unmapped event type "start"` on **every request** (4× in export
A, ~40× in export B, on two unrelated providers). The wording blames the
provider for a goa-internal lifecycle event.

**Expected.** `EventStart` is a mapped, silent lifecycle event. It re-arms the
watchdog like any other real event, and never warns.

**Fix plan.**
1. Add `provider.EventStart` to `streamEventHandlers` with a no-op handler that
   only calls `markGenStart()`.
2. Add a regression test: a stream emitting `EventStart` first must re-arm the
   watchdog (assert no stall kill when a subsequent delta arrives past the old
   deadline) and must produce zero WARN lines.
3. Add a test asserting the set of unmapped-event warnings stays empty for a
   normal `EventStart` + deltas + `EventDone` sequence.

**Validation.** `unmapped event type` disappears from agent logs for normal
turns on both providers.

---

## 3. `tool_stream: true` is never sent to z.ai (MEDIUM, z.ai-specific)

**Observed.** `ZaiToolStream` is declared in
`internal/agentic/provider/compat.go:25` and
`protocol/openai_completions.go:77`, hardcoded `boolPtr(false)` in
`compat_detect.go:118`, and then never read — nothing writes `tool_stream` into
the request body. The live body captured in export A
(`logs/cache_miss_requests.json` → `requests[0].body`) has keys
`messages, model, prompt_cache_key, prompt_cache_retention, stream,
stream_options, thinking, tools` — no `tool_stream`.

`pi` sends it (`packages/ai/src/api/openai-completions.ts:851-853`) and its
generated z.ai catalog sets `compat.zaiToolStream: true` for GLM-4.6 → GLM-5.3.

**Expected.** Requests to z.ai carrying tools include top-level
`tool_stream: true`, matching pi. Providers without the capability are
unaffected.

**Fix plan.**
1. Read `compat.ZaiToolStream` in `buildOpenAIParams` and set
   `body["tool_stream"] = true` when tools are present and the flag is set.
2. Set the flag from the endpoint fingerprint (`isZai`), consistent with how
   `maxTokensField`/`ThinkingFormat` are derived — not left hardcoded false.
3. Tests: z.ai + tools → `tool_stream: true` present; z.ai without tools →
   absent; non-z.ai provider → absent (mirrors pi's
   `openai-completions-tool-choice.test.ts` cases).

**Validation.** Assert against a built z.ai request payload.

---

## 4. No `max_tokens` cap on reasoning-capable providers (MEDIUM, z.ai-specific)

**Observed.** No `max_tokens` / `max_completion_tokens` in the captured z.ai body;
the user model profile sets `thinking_level: xhigh`, `max_tokens: 0`. Export A
request 17 spent 4 268 reasoning tokens before its first tool call and ran 80.7s.

`pi` documents the hazard (`packages/ai/src/types.ts:834-840`): reasoning and the
answer share `max_tokens`, so without a budget a reasoning-heavy turn can consume
the whole response and emit no answer.

**Expected.** Reasoning-capable providers receive an explicit output cap so the
answer cannot be starved by reasoning.

**Fix plan.**
1. Send `max_completion_tokens` (or `max_tokens`, per the existing
   `MaxTokensField` compat) for reasoning-capable providers when unset.
2. Derive the default from the model's known output ceiling; make it
   configurable and skip when the user set an explicit value.
3. Tests: z.ai reasoning model with no configured cap → cap present; explicit
   user cap wins; non-reasoning providers unchanged.

**Validation.** Payload assertion tests.

---

## 5. Shipped `activity_timeout` default is too short for reasoning models (MEDIUM)

**Observed.** `config/user.yaml` pins `activity_timeout: 60s`, but the shipped
default is `2m` (`provider.DefaultStreamIdleTimeout`). 60s is short for a
reasoning model — export A request 17 legitimately ran 80.7s. The same key also
drives *two* racing guards (byte-level `idleTimeoutReader` and the event-level
watchdog), both wired from `execution.activity_timeout` at
`provider/manager_streamopts.go:38-46`.

**Expected.** The default tolerates long reasoning turns, and the two guards do
not race on one budget.

**Fix plan.**
1. Raise the shipped default stall window to comfortably exceed a long reasoning
   turn; keep it configurable.
2. Document the reasoning-model trade-off in `docs/CONFIGURATION.md`.
3. Ensure the byte-level reader and the event watchdog cannot both fire on the
   same silence window (single owner, or event watchdog strictly inside the byte
   budget).
4. Test: assert the byte guard does not pre-empt the event watchdog for the same
   silence interval.

**Validation.** Unit tests on timeout resolution + watchdog ordering.

---

## 6. z.ai accepts context overflow silently (LOW, z.ai-specific)

**Observed.** `opencode` records the quirk
(`packages/opencode/src/provider/error.ts:31`): *"z.ai: can accept overflow
silently (needs token-count/context-window checks)"*. Goa's z.ai profile has
`context_window: 0`, so there is no proactive guard, and `OnContextError`
compression only triggers on an actual context-length error that z.ai may never
raise.

**Expected.** Overflow is detected by token count before the request, not only by
provider error.

**Fix plan.**
1. Resolve the real context window for z.ai models (registry / probe) so the
   existing threshold check has a bound.
2. Test: a z.ai request projected over the window triggers compression instead of
   being sent.

**Validation.** Unit tests on the projection + threshold path.

---

## 7. FEATURE — z.ai Coding Plan quota reset API

**Observed.** goa fetches only quota consumption
(`GET {origin}/api/monitor/usage/quota/limit`). ZCode additionally implements
the Coding Plan **reset** surface: `/api/v1/coding-plan/reset/{status,opportunity,use,history/read}`.

Contract details to preserve:
- envelope `{code,msg,data}`; **`code 3301` = throttled** and its `next_try_at`
  must pass through verbatim or the client re-polls into the limit;
- bare HTTP 429 maps to the same cooldown;
- `idempotency_key` mandatory, ≤64 chars, on both mutating calls;
- dual auth: `zcodejwttoken` (Bearer) **plus** family OAuth token
  (`oauth:zai:access_token`) sent raw, **no** cross-family fallback.

**Expected.** `/quota` surfaces available 5-hour / week resets, and the client
respects the server's retry boundary.

**Fix plan.**
1. Add `plugins/bundled/provider-quota/fetchers/zai-coding-plan.js` registered in
   `plugin.js`, reusing `lib/http-quota.js`.
2. Fetch reset status alongside the monitor quota; expose via `/quota :resets`
   and the status-bar segment.
3. Honour `next_try_at` on `3301` and on HTTP 429; persist the cooldown.
4. Tests: mapping of `available_*_resets` / `latest_*_history`; throttle path
   preserves `next_try_at`; idempotency key validation; no cross-family
   credential fallback.

**Validation.** Follow the existing `plugins/quota_*_test.go` harness pattern;
render the segment and assert the reset rows appear.

---

## 8. Verify the Anthropic-surface hypothesis for z.ai (investigation)

**Observed.** ZCode talks to z.ai coding plans over
`https://api.z.ai/api/anthropic` (Anthropic Messages), while goa and pi use the
OpenAI-compat `.../api/coding/paas/v4`. Both known-bad streams came from the
OpenAI-compat surface (zai and opencode-go).

**Hypothesis (unverified).** The Anthropic surface terminates SSE cleanly and
would sidestep defect 1 for z.ai.

**Plan.** Live-probe both surfaces with an identical prompt and compare whether
`[DONE]` / a terminal event is always emitted. Record the result in the review
doc. Do **not** migrate the provider without that evidence.
