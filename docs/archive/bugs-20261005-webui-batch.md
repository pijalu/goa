# Web UI batch: layout, colour, clipboard and startup-input defects

**Status:** fixed & validated (2026-10-05). Tracker items closed: B5, B7, B8, B9,
B10. Fix commits: `8a9176cd` (status line, B11 predecessor), and the batch commit
that carries this file (see `git log -- docs/archive/bugs-20261005-webui-batch.md`).

Method note: the browser was driven **headless** (`agent-browser`, never
`--headed`), and once the hazards below were known, **no Meta (Cmd) chord was
injected** — a headed Chrome takes an unclaimed Cmd chord as a macOS menu key
equivalent, which is how stray "About" dialogs appear. The clipboard driver
(`e2e/webclip`) is the only place real Cmd chords are injected; it was run once
by hand (copy/paste/cut all PASS) and then left out of the automated loop.

---

## B8 + B9 — the live screen (input box + status band) scrolled out of the viewport

**Observed.** `e2e/w1_webui_browser.sh` assertion `footer_band` failed
(`inside:false, overlap:true`), and a reproduction measured the live grid
**29,844 px below the scroller's viewport** with `scrollTop=1268` — the user is
left looking at mid-transcript with no input line and no status band. This is the
"UI completely mixed up" report, and it also explains B8's "a screen-taller view
command drops the transcript above it": with the live screen gone the transcript
is all that is left on the page.

**Root cause.** Two coupled defects:

1. `.grid` was only bottom-anchored (`margin-top:auto`) — it stayed in the
   viewport while the content was short, and scrolled away with the transcript
   once the transcript grew.
2. `resizeRows()` mutates the row list; a shrink clamps `scrollTop`, which fires
   a `scroll` event, and the scroll listener read **any** scroll as "the user
   scrolled away" — so a window resize silently disarmed follow-tail for the rest
   of the session, and nothing ever brought the view back to the tail.

**Fix.**

* `internal/webui/assets/app.css` — `.grid` is `position: sticky; bottom: 0` with
  an opaque background, so the input box and status band stay pinned exactly like
  a terminal's bottom rows while the transcript scrolls under them, and the
  single-scroller model (and the caret's containing block) is unchanged.
* `internal/webui/assets/app.js` — `resizeRows` early-returns when the row count
  is unchanged (preserving the "one layout read per painted frame" contract) and,
  when it does change, re-asserts the follow intent synchronously: by the time the
  clamp's scroll event is delivered the view is already back at the bottom.

**Validation.** Harness `footer_band` PASS ("53 rows all inside the viewport,
model line last"); `grid_height`, `resize_trims`, `scroll_follow_tail`,
`transcript_bounded`, `scroll_detach`, `scroll_container` all PASS; browser
re-check at 1280×900: `{state:live, rows:53, inside:true, overlap:false}`.

---

## B10 — scrollback rows rendered in a different colour from the live screen

**Observed.** The same table rule rendered `#8b949e` on the live screen and
**`#8957e5` (purple)** in the transcript. Systematic same-text A/B across the two
containers: **26 mismatched of 27** rows — only the border colour matched.

**Root cause.** Two independent mis-mappings:

1. `internal/webui/theme.go` mapped the page's `--dim` to the theme token
   **`token_thinking`** (`#8957e5`) and `--fg` to `toolOutput` (a dim grey) — so
   "dim" text rendered purple and the page's foreground was the secondary colour.
2. `.scrollback { color: var(--dim) }` recoloured history: every run the server
   sends **without** an explicit colour inherits the container's colour, so
   transcript rows resolved to purple while the identical rows in the grid
   resolved to `--fg`.

**Fix.** `--dim` now mirrors the theme's dim/secondary text (`toolOutput`) and
`--fg` its normal text (`assistant_msg`); `.scrollback` declares **no** colour at
all, so history resolves colours exactly like the live grid (the invariant a
terminal has: scrollback keeps the colours it was painted with).

**Validation.** Same-text A/B now **matched 6, mismatched 0**; computed
`--dim=#8b949e`, `--fg=#c9d1d9`; `getComputedStyle(scrollback).color ==
getComputedStyle(grid).color`. Unit guards: `TestThemeVars_DimIsNotTheThinkingColour`,
`TestScrollbackDoesNotRecolourHistory`.

---

## B7 — keystrokes typed while the session is still starting were not acted on

**Observed.** Dialing `/ws` the instant the listener answers and sending
`/quit` + Enter: the server stayed up (3 of 4 runs). With a ≥50 ms delay it always
exited. Measured window: **< 50 ms**, i.e. the gap between the listener binding
and the session being wired.

**Root cause.** Three windows, all closed:

1. The page dropped keys when the socket was still `CONNECTING` (`send()`
   silently returned).
2. The server-side `inp.SetOnSubmit` was wired in `setupEventHandlers`, *after*
   `engine.Start()` — and `engine.Start` replays the pre-start input the
   VirtualTerminal had held, so a replayed Enter was consumed with no handler
   attached.
3. Even with both closed, the app is not fully wired at `Start` (engine loops,
   focus and app handlers come up in sequence), leaving a sub-50 ms race no
   amount of replaying can win deterministically.

**Fix.**

* `internal/webui/assets/app.js` — messages produced while the socket is
  `CONNECTING` are held in a bounded queue (256) and flushed on open.
* `internal/app/tui.go` / `events.go` — the submit path (`SetOnSubmit`,
  `OnImagePaste`) is wired **before** `engine.Start()`.
* `internal/webui/server.go` + `internal/app/{webui,app}.go` — new
  `ServerOptions.Ready`: every request is **held** until the app signals a fully
  wired session (`markWebReady`, idempotent, called after `setupEventHandlers`).
  Waiting is deterministic where replaying into a half-built engine is not: the
  request is answered by a session that can act on its input.

**Validation.** `TestGoaE2E_ServerBrowserQuitStopsProcessWithStatusZero` —
the re-send loop was **removed** (a gate that only works when the test retries is
not a gate) and the test now passes **5/5**; the Ctrl+C and SIGTERM e2e tests
still pass. Unit guards: `TestServer_ReadyGateHoldsRequestsUntilWired`,
`TestServer_NilReadyDoesNotGate`,
`TestClientJS_InputTypedWhileConnectingIsHeldAndFlushed`.

---

## B5 — copy/paste in the page (verified, plus a driver fix)

**Observed.** The harness's `clipboard_real` step failed with "no visible grid
row with text to select" / "typing did not reach the input line
(focus=\"\", line tail=\"\")" — i.e. `e2e/webclip` was driving a page whose grid
had not painted yet.

**Root cause.** `e2e/webclip`'s `prepare()` waited only for `#rows .row` to
*exist*; the row divs are created by the first frame, before their runs arrive, so
the driver measured an empty grid. (The pre-fix off-screen grid made it worse.)

**Fix.** `prepare()` now waits until a row actually carries text. The page's
clipboard ownership was already correct and is unchanged.

**Validation.** Driven by hand once (the only entry point that injects real Cmd
chords): `copy PASS — Cmd+C put 32 selected characters on the clipboard`,
`paste PASS — Cmd+V inserted the clipboard text exactly once`,
`cut PASS — Cmd+X put the selection on the clipboard and removed it from the
input line`. The page's own chord path stays pinned by `clipboard_chords` PASS in
the harness.

---

## Residual / still open

* **B6** (terminal copy/paste does not support images) is a separate terminal
  feature gap, untouched by this batch.
* `e2e/webclip` injects real Meta chords; per the browser-validation rules it must
  only ever run against a headless browser (and is deliberately not part of the
  default loop here).
* The transcript `content-visibility:auto` and the bounded transcript keep their
  measured costs (1.40 ms/frame at 10 000 rows).
