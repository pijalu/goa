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

## B2 — Web UI: screen corrupted after `/quota` (double/triple output)

**Observed.** Typing `/quota` in the browser leaves the whole screen corrupted
with output appearing two or three times over.

Evidence: `docs/bugs/2026-10-04-webui-quota-corruption.png` (real browser, after
`/quota`).

Reproduced partially: with a 1280×900 window the transcript+grid form one
continuous log (no duplication), so the corruption depends on the window
geometry/scroll state the screenshot was taken in — the screenshot is currently
the authoritative evidence. Two concrete mechanisms are already visible in the
same area and must be ruled in/out first:
1. the web flow ships rows that were pushed into the emulator's scrollback by a
   *geometry change* as if they were history (see B3), so a later repaint can
   duplicate them;
2. row patches and transcript rows are separate messages, so any path that
   re-sends a row (or applies a patch whose row index the client has already
   moved) paints the same content twice.

**Expected.** `/quota` (and any long output) renders each line exactly once, in
chronological order, with the transcript holding the scrolled-off lines and the
grid holding the live screen.

**Plan.**
- Reproduce headlessly with the existing web harness (`newWebCommandSession` in
  `internal/app/webui_commands_parity_test.go`: real `webui.VirtualTerminal`, real
  engine, real `EncodeKey` path): type `/quota` (or `/help` as a model-free
  stand-in), then assert the *concatenation of transcript rows + grid rows*
  contains each content line exactly once and in order. That is the invariant the
  screenshot violates.
- Fix the duplicates at their source (whatever the harness exposes) — likely in
  `VirtualTerminal.publish`/`TakeScrollback`/frame sequencing rather than the
  client.
- Test approach: the harness assertion above, run against the pre-fix code to
  see it fail, plus a browser pass.
- Validation: real browser at the screenshot's window size, `/quota`, screenshot
  + DOM dump showing every line once; `e2e/w1_webui_browser.sh` mechanics section
  still green.

## B4 — Web UI: input line / bottom status bar not rendered correctly

**Observed.** The bottom of the page (input line, separators, status and model
lines) is not rendered correctly in the browser.

Formal measurement so far (DOM, 1280×900): `#screen` clientHeight 876 vs grid +
transcript heights, rows all 16 px with no overlap and no horizontal overflow, so
the *cell* layout is not the problem — the suspicion is the same rows being laid
out with the wrong width/height after a geometry change, and/or the footer rows
falling outside the visible area (screenshot structure analysis shows one
separator rule and a gap before the status line, with no model line inside the
captured area).

**Expected.** The footer renders exactly like the terminal's: input row(s),
separator, status row with the right-aligned mode, model line — all inside the
viewport, one row per line, never clipped.

**Plan.**
- Compare browser vs terminal row by row at the same geometry: capture the TUI's
  own screen at N columns/rows (PTY + `tui.TermEmulator` replay) and assert the
  web grid's text equals it (this is the "fully verify the screen" check; it also
  covers B3).
- Fix whatever the comparison exposes (measurement of cols/rows, footer heights,
  or the grid height accounting for the footer band).
- Validation: equality check at several geometries in a real browser (cells, row
  count, `#rows` height vs `16 + rows*16`), plus the screenshot.

## B5 — Web UI: selecting text works, copy/paste does not

**Observed.** Text can be selected in the page, but copying it does not reach the
system clipboard and pasting does not insert anything (reported; not yet
reproduced — headless Chrome has no real clipboard).

What is already verified: the page claims the right chords (`Ctrl/Cmd+C/V/X` stay
the browser's, `Ctrl+C` is the terminal interrupt only with no selection), a
*synthetic* paste inserts its text into the editor, and an idle page performs 0
DOM mutations with a stable selection — so neither the chord logic nor repaint
churn is the obvious cause.

**Expected.** Cmd+C with a selection puts the selected text on the system
clipboard; Cmd+V pastes clipboard text into the input line; Cmd+X cuts; the
engine's own Ctrl chords keep working.

**Plan.**
- Reproduce with a real clipboard: drive Chrome with real chord events over CDP
  and read back `navigator.clipboard.readText()` with the clipboard permission
  granted, both with and without a selection, before touching code.
- Test approach: once the failing step is known, add it to
  `e2e/w1_webui_browser.sh` (real clipboard, not synthetic events) so it is
  regression-checked; keep the goja chord test as the unit-level guard.
- Validation: real browser, select → Cmd+C → clipboard contains the selection;
  Cmd+V inserts text; Ctrl+C with no selection still interrupts the turn.

## B6 — Terminal copy/paste does not support images

**Observed.** Pasting an image into the terminal inserts nothing: there is no
path from the terminal clipboard to an attachment. The web page has this path
(`app.js` `paste` → `/upload` → image path inserted as input), but the terminal
does not.

**Expected.** The terminal can bring an image in the same way the web page does,
and the resulting attachment is inserted as a path in the input line — i.e. image
paste is a first-class capability of the session, not of one front end.

**Plan.**
- Decide the mechanism per platform (e.g. a hotkey that reads the OS clipboard via
  the platform tool: `pbpaste`/`osascript` on macOS, `xclip`/`wl-paste` on Linux,
  PowerShell on Windows), then upload/store it through the same image store the
  web path uses, and insert the stored path into the editor.
- Keep the platform-specific part behind one interface (small primitive) so the
  editor only ever sees "insert this path".
- Test approach: unit test the "clipboard image → stored path → editor text" chain
  with a fake clipboard reader; skip the OS call where it is unavailable.
- Validation: real terminal, copy an image in a browser, press the hotkey in goa,
  the input line shows the stored path; and the same via the web page for parity.

## B7 — Web UI: keystrokes typed while the session is still starting are not acted on

**Observed.** A browser that connects the moment the listener answers — i.e.
before the session has finished wiring itself — can type without effect. Measured
while fixing B1 with a real `goa server` over a websocket client: sending the
`/quit` keys immediately after `goa web UI ready: ...` is ignored (the process
stays up), while the same keys sent 350 ms+ after startup always quit it. The
listener binds and accepts clients before `App.RunContext` builds the engine, so
the window is the session's startup time (tens of milliseconds on this machine).

**Expected.** Typing always has an effect: bytes that arrive before the engine
exists are held and delivered when it does, and once they are delivered the
submit path is there to act on them.

**Partial fix already in tree.** `webui.VirtualTerminal` now holds pre-`Start`
bytes (bounded, 4 KiB) and replays them in `Start`, instead of dropping them
silently (`TestVirtualTerminal_InputBeforeStartIsReplayedOnStart`). The remaining
half is the submit path: `inp.SetOnSubmit` is wired in `setupEventHandlers`,
*after* `buildTUI` has already started the engine — so an Enter replayed at
`Start` is consumed with no handler attached (the typed text stays in the editor,
it is not lost). Making the first keystrokes fully effective needs either the
submit wiring moved before `engine.Start` or the listener to start serving only
once the session is ready; both touch session startup, so they are left here.

**Plan.**
- Decide the gate: either (a) wire `inp.SetOnSubmit` inside `buildTUI` before
  `engine.Start`, proving no replayed key can reach unwired app state, or (b) keep
  the listener bound but start `srv.Serve` only after the session signals ready.
- Test approach: the existing e2e (`cmd/goa/e2e_server_signal_test.go`
  `TestGoaE2E_ServerBrowserQuitStopsProcessWithStatusZero`) sends `/quit` exactly
  at connect time; drop its re-send loop once the gate exists, which is the
  deterministic assertion this needs.
- Validation: repeated runs of that test with no re-send, plus a browser test that
  types immediately on load.

## Closed
Closed 2026-10-04 — B1, `goa server` could not be stopped from its own console:
Ctrl+C/SIGTERM are now wired into the session's own stop path, the listener
closes after the session ends, deferred profiling flushes and the process exits 0.
See [`docs/archive/2026-10-04-webui-server-shutdown.md`](docs/archive/2026-10-04-webui-server-shutdown.md)
for the root cause, the RED/GREEN measurements and the residual risks.

Closed 2026-10-04 — web UI rendering (scroll / caret / resize / clipboard) and
the O(history) per-frame cost. See
[`docs/archive/webui-rendering-and-frame-cost.2026-10-04.md`](docs/archive/webui-rendering-and-frame-cost.2026-10-04.md)
for the root causes, the measurements and the verification, and
[`docs/webui-perf-assessment.md`](docs/webui-perf-assessment.md) for the full
performance record.
