# Archived bug — completion blocked by the quota plugin's startup prime

Moved out of `bugs.md` on 2026-10-03 (guideline 4: closed items live here).
Fixed in main `22865e64`, merged into `feature/webui` as `49b23076`.


**Observed.** At startup, typing `/` does not extend for a couple of seconds: the
autocomplete popup stays absent and the keystroke feels ignored. It resolves on
its own once the bundled `provider-quota` plugin finishes its load-time prime.

**Expected.** Input completion is available from the first keystroke, exactly as
it is with `--no-plugins`. A plugin's background work must never delay the
input path.

**Cause.** Verified by reading the frame discipline and the two call sites:

- `tui/editor_autocomp.go:27` — `scheduleAutoComp` calls `updateAutoComp()`
  **inline** (its own doc comment, claiming completion is deferred "to the next
  render cycle", is stale), and `updateAutoComp` calls `e.completer.Complete(prefix)`
  at line 53. Input bytes arrive as
  `terminal.Start(func(data){ Apply(func(){ handleKey(data) }) })`
  (`tui/tui.go:401`), so this completion runs **on the commandLoop** — the sole
  owner of input and render state. A slow `Complete()` freezes the whole
  keyboard, not just the popup.
- `plugins/plugin.go:341` `buildCompletionWrapper` — the JS completer acquires
  the runtime frame with the **blocking** `b.enterFrame()`
  (`plugins/vm_frame.go:37`), which spins on `time.Sleep(frameRetry)` until the
  frame is free.
- `plugins/bundled/provider-quota/plugin.js` primes its cache during load: the
  load frame (`plugins/plugin_modules.go:49`) starts a `setTimeout(0)` that
  runs provider quota HTTP fetches. A blocking bridge call keeps its frame for
  the whole hop, so the quota runtime's frame is held across network I/O for as
  long as the fetches take.

So `/quota:` argument completion calls `enterFrame()` on a runtime whose frame
is parked on HTTP, and blocks the commandLoop — input and rendering — for the
full duration of the prime. The stall length is the prime's length, which is why
it is seconds and provider-dependent.

**Precedent / contrast.** The segment-render path already got this right:
`plugins/bridge_extended_surface.go:74` `buildSegmentRender` uses
`tryEnterFrame()` and reports `ok=false` so the caller keeps the last good text.
`vm_frame.go`'s own doc says `tryEnter` is "for the UI-facing paths — segment
render and hotkeys — where waiting would stall the render loop or the
keystroke." Command completion is exactly such a UI-facing path and was missed.
Scheduler timers already use `TryEnter` (`plugins/scheduler.go:206`).

**Fix plan.**

1. `plugins/plugin.go` `buildCompletionWrapper`: switch `enterFrame()` to
   `tryEnterFrame()`; on a busy frame return no completions instead of waiting.
   Completion is a best-effort suggestion recomputed on the next keystroke, so
   skipping costs nothing and blocking costs the whole keyboard.
2. Return the skip as an explicit "busy" signal rather than a silent empty
   slice, so the caller can keep the previous candidate list instead of
   flashing the popup empty mid-keystroke — mirroring how segment render keeps
   last-good text.
3. Audit the other UI-facing JS entry points for the same defect. Hotkeys and
   segment render are already non-blocking; verify no other bridge invoked from
   the commandLoop (tooltips, footer hints) still uses the blocking form.

**Status: FIXED** (main `22865e64`, merged into `feature/webui` as `49b23076`).

Step 1 landed as planned in `plugins/plugin.go`. Step 2 was reconsidered and
deliberately dropped: returning a nil slice is already the honest "no
suggestions right now" answer, and a keep-previous-candidates cache would
require threading per-command state back through the registry for a gain that
only shows when the prime overlaps a single keystroke — one keystroke's
suggestions are recomputed on the very next one. Simpler surface, same
behaviour. Step 3 audit: the JS entry points reachable from the commandLoop are
completion (fixed), segment render (`bridge_extended_surface.go:74`,
already `tryEnterFrame`) and hotkeys (already non-blocking); scheduler timers
use `TryEnter` (`plugins/scheduler.go:206`). No other blocking call site
remains on a keystroke path.

**Test approach — as executed.**

- `plugins/completion_frame_test.go` `TestCompletion_DoesNotBlockWhileFrameHeld`:
  registers a completer via the real `goa.registerCompletion` path, holds the
  runtime frame with `enterFrame()` (the state the quota prime leaves behind),
  then calls the registered completer from a second goroutine and asserts it
  returns within 500 ms with no candidates instead of blocking.
- Behaviour guard in the same test: after the frame is released, the completer
  must return the real JS result, proving the skip is a busy signal and not a
  permanently broken completer.
- RED was verified before the fix (it failed on the blocking call with the
  stall message), and the test was mutation-checked afterwards: reverting only
  the `tryEnterFrame`→`enterFrame` change makes it fail again, so it genuinely
  pins the defect rather than passing vacuously.
- Gates run separately per the guidelines: `go vet ./...` clean;
  `staticcheck ./plugins/...` reports only the two pre-existing findings
  (`bridge_extended_surface.go:117` S1021, `vm_frame.go:95` U1000), both
  confirmed present on `main` before this change and untouched by it;
  `gocognit -over 15 .` and `gocyclo -over 12 .` clean; `gofmt` clean;
  `go build ./...` and `go test -count=1 -race -cover ./...` green.
- Not yet done: the interactive terminal check with the bundled quota plugin
  and an unresponsive provider endpoint (typing `/qu` must extend on the first
  keystroke). This needs a real provider credential to make the prime actually
  park, so it stays open — the unit test covers the mechanism, but the
  end-to-end symptom is unverified.

**Validation.** The mechanism is proven by the test above (frame held → no
block; frame free → real completions). What remains is confirming in a real
terminal run with the quota plugin enabled that `/` extends on the first
keystroke during startup, and that `/quota` completions still populate — the
`plugins/quota_*_test.go` suite, which exercises the same wrapper, is green.

_No other open items._
