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
- "Shipped `activity_timeout` default too short for reasoning models" →
  `docs/archive/bugs-20260930-activity-timeout-reasoning-window.md`
- "z.ai accepts context overflow silently" →
  `docs/archive/bugs-20260930-zai-silent-context-overflow.md`
- "z.ai Coding Plan quota reset API" →
  `docs/archive/bugs-20260930-zai-coding-plan-reset.md`

---

## 4. Verify the Anthropic-surface hypothesis for z.ai (investigation)

**Observed.** ZCode talks to z.ai coding plans over
`https://api.z.ai/api/anthropic` (Anthropic Messages), while goa and pi use the
OpenAI-compat `.../api/coding/paas/v4`. Both known-bad streams came from the
OpenAI-compat surface (zai and opencode-go).

**Hypothesis (unverified).** The Anthropic surface terminates SSE cleanly and
would sidestep the held-open-stream stall (`docs/archive/bugs-20260930-stall-held-open-complete-answer.md`) for z.ai.

**Plan.** Live-probe both surfaces with an identical prompt and compare whether
`[DONE]` / a terminal event is always emitted. Record the result in the review
doc. Do **not** migrate the provider without that evidence.
