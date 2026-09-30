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

Closed in this round (moved to `docs/archive/`):
- "Stall watchdog discards already-complete streams" →
  `docs/archive/bugs-20260930-stall-held-open-complete-answer.md`
- "`EventStart` is unmapped — no watchdog re-arm + WARN on every request" →
  `docs/archive/bugs-20260930-eventstart-unmapped.md`
- "`tool_stream: true` is never sent to z.ai" →
  `docs/archive/bugs-20260930-zai-tool-stream.md`
- "No `max_tokens` cap on reasoning-capable providers" →
  `docs/archive/bugs-20260930-reasoning-max-tokens-cap.md`

---

## 2. Shipped `activity_timeout` default is too short for reasoning models (MEDIUM)

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

## 3. z.ai accepts context overflow silently (LOW, z.ai-specific)

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

## 4. FEATURE — z.ai Coding Plan quota reset API

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

## 5. Verify the Anthropic-surface hypothesis for z.ai (investigation)

**Observed.** ZCode talks to z.ai coding plans over
`https://api.z.ai/api/anthropic` (Anthropic Messages), while goa and pi use the
OpenAI-compat `.../api/coding/paas/v4`. Both known-bad streams came from the
OpenAI-compat surface (zai and opencode-go).

**Hypothesis (unverified).** The Anthropic surface terminates SSE cleanly and
would sidestep the held-open-stream stall (`docs/archive/bugs-20260930-stall-held-open-complete-answer.md`) for z.ai.

**Plan.** Live-probe both surfaces with an identical prompt and compare whether
`[DONE]` / a terminal event is always emitted. Record the result in the review
doc. Do **not** migrate the provider without that evidence.
