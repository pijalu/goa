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

## 2. Tool execution scrolled out of view stops updating (elapsed frozen)

**Observed:** when a running tool's widget scrolls above the viewport top,
its live status line freezes at the value it had when it left the screen —
e.g. a long `go test` run keeps showing `elapsed 123.5s` forever instead of
ticking, and the final duration is never reflected on scrollback:

```
 ● $ go test -count=1 -race -cover ./... 2>&1 | grep -v "^ok" | head -30; echo "=== SUMMARY ==="; go test -count=1 -race -... (timeout 3
 elapsed 123.5s          ← never advances once the block is off-screen
```

The ticking text is recomputed per frame in
`tui/tool_execution.go`/`tool_execution_status.go` and pushed as a canvas
row update, but a terminal cannot rewrite rows already pushed into
scrollback: once the widget scrolls above the viewport, the diff repaint
(culled at the `scrollTop` watermark) never touches its rows again.
`scrollOffUnstable` (tui/compositor_frame.go) deliberately treats the
in-place tick as "benign" — one tick stale — to avoid a full-transcript
reset on every animation tick, and no one-time scrollback resync
(`scrollbackDirty`) is triggered when the tool finally completes, so the
stale `elapsed` value persists forever.

**Expected:** a live tool block that moves out of the visible window must
keep its status current — either it keeps receiving row updates while
running (updating scrollback in place) or the running widget is pinned
visible until completion; the final row must always end with the true final
duration, never a stale intermediate `elapsed`.

## 3. 429 rate-limit retry: fibonacci backoff, 5-attempt default, /config exposure

**Observed:** an `Error: 429` from the provider aborts the turn too early.
The retry machinery exists (429 is classified `RATE_LIMIT`, server
`Retry-After` is honored — internal/agentic/retry_classify.go,
agent_stream_retry.go), but the defaults and surface do not match expected
behavior:

- **Retry budget defaults to 2** (`execution.retries: 2`,
  config/configs/default.yaml:20) — a sustained rate limit exhausts the
  budget after two attempts and surfaces the error to the user.
- **Backoff cap defaults to 30s** (`maxStreamBackoff = 30 * time.Second`,
  internal/agentic/retry_classify.go:21) — providers asking for longer
  cooldowns cannot be waited out.
- **Backoff curve is exponential** (legacy schedule 1s, 2s, 4s, … with
  ≤250ms jitter) — not the requested fibonacci growth.
- **Neither knob is exposed in `/config`**: `execution.retries` and the
  per-provider `retry_policy` (config/config_types.go `RetryPolicyConfig`:
  mode, max_retries, backoff.initial/max/jitter, codes) are YAML-only;
  nothing in core/commands/config*.go offers them.

**Expected:**

1. On 429 (and other rate-limit class errors), retry with **fibonacci**
   delay growth (1s, 1s, 2s, 3s, 5s, 8s, …), **maxed at 5 minutes** by
   default. A server-supplied `Retry-After` should still win when present
   and within the cap (keep current dsh semantics).
2. **Total retries configurable, default 5** (up from 2).
3. **Max delay and max retry exposed in `/config`** (Execution or Provider
   section) with persistence through the config cascade, alongside the
   existing YAML keys — no new parallel setting.

## 4. Streaming render CPU exceeds the 10–20% budget (pre-existing; optimization)

**Measured** (controlled stub SSE stream, PTY 160×48, cputime-delta method;
harness: `e2e/perfdrive` + stub recipe in this entry):

| Scenario | branch | main | Δ |
|---|---|---|---|
| idle | 0.0% | — | — |
| sustained 20 tok/s (45s) | **23.2%** | 23.8% | parity |
| burst 100 tok/s (20s) | **74.6%** | 72.2% | parity |
| wire bytes (sustained) | 2.47MB | 2.66MB | −7.0% |
| SGR sequences on wire | 3,578 | 18,646 | −81% |
| frames emitted | 1,025 | 1,025 | 1:1 |

**Analysis:** CPU per delta is ~7–9ms and roughly constant regardless of
document length (cost is viewport/canvas-bound, not document-bound), and the
compositor emits one frame per delta (~2.5KB each) with no batching — so
CPU scales linearly with token rate. At the user-facing target (≤10–20%)
20 tok/s already fails; a fast local model at 100–200 tok/s pins 0.75–1.5+
cores. Present identically on main — **not** a regression of this branch
(the branch's SGR coalescing actually cuts wire bytes 7% and resets 91% at
frame parity).

**Expected:** delta→frame batching or throttling — render on a fixed ticker
(e.g. ≤30fps) with a dirty flag so N deltas arriving in one tick coalesce
into one frame (100 tok/s → ~3× CPU cut), keeping per-delta latency ≤1
frame interval. Bonus (trivial): after a turn ends the TUI still emits a
cursor-only empty frame (`?2026h` + CUP + `?2026l`) every ~2–3s — stop the
idle re-anchor timer once the cursor is parked.

**Harness recipe (repro):** stub OpenAI-compatible SSE server streaming
non-repeating filler (⚠ every word must be unique — a cycling 12-word
vocab is aborted in ~13s by the stream loop detector, which is CORRECT
behavior: "stream loop detected … after 4 warnings"), project config pinning
the stub provider, then `perfdrive --bin <goa> --dir <proj> --prompt …` and
poll `ps -o time= -p PID` deltas (ps `%cpu` decaying average reads 0.0%
during 23% real usage on macOS — do not use it).

# TODO

*(open items tracked under `# To fix` above; move each to docs/archive when
tested and closed.)*
