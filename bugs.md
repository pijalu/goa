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

## B22 — web blocks: a collapsible is always created CLOSED, so the server's expand state is ignored

**Reported**: "thinking block should be expanded by default".

**Observed (code path, client)**: `renderBlock` computes the previous open state as

```js
var wasOpen = b.body && b.body.open;   // app.js — null for a block's FIRST render
```

and `collapsible(wasOpen, metaOpen)` reads `if (wasOpen !== undefined) det.open = wasOpen;`
(`internal/webui/assets/app.js:550`). For a brand-new block `b.body` is `null`, so
`wasOpen` is `null` — which is **not** `undefined` — and the element is created with
`det.open = null`, i.e. closed, whatever the server said. The `meta.expanded` the
server ships (TUI truth: `newThinkingBlock` starts `expanded: true`, tools per the
`tui.tools.view` policy) only ever applies to a block whose body already exists —
never, in practice. So every thinking block and every tool card opens collapsed
regardless of the TUI's state, and a reader has to click each one.

**Expected**: the first render honours the server's `meta.expanded` (thinking
expanded by default, exactly as the TUI and `tui.transparency.thinking_collapsed`
say), and a later re-render keeps the reader's own toggle.

**Also reported (same surface)**: "for the collapse: follow the rules: if a collapse
is open (or closed) subsequent item should follow the same logic" — once the reader
toggles a collapsible, later collapsibles of the same kind must adopt that choice
instead of reverting to the server default.

**Fix plan / test approach**

1. `wasOpen` becomes `b.body ? b.body.open : undefined`, so `collapsible` falls back
   to `meta.expanded` exactly once (the first render).
2. A `toggle` listener on each collapsible records the reader's choice as the sticky
   preference for that kind (`thinking`, `tool`); `collapsible` uses the sticky
   preference for new blocks of that kind, the server meta otherwise.
3. Goja tests (real `app.js`): a thinking op with `meta.expanded=1` renders an open
   `<details>`, one with `"0"` renders closed, and after a synthetic toggle on a
   first block a second block of the same kind follows it. RED before the fix.

## B23 — web blocks: tool cards do not match the TUI widget

**Reported**: "tool calls will show a lot of content *then* be collapsed — the tool
call should follow the TUI: some details are visible (type, file, action, couple
line preview)".

**Observed**: the block plane ships the tool's identity (`tool`, `args`, `status`,
`duration`) and its whole output, and the page builds a `<details>` whose collapsed
state shows `headLines(b.text, 4)` — a hard-coded 4-line preview — while the TUI's
widget shows the tool header plus the policy's preview count
(`tui.tools.preview_lines`, default 10, `defaultToolPreviewLines`), and while the
collapse state itself is broken by B22. The TUI also renders the *tail* semantics
per renderer (read/write/bash previews differ), which the page cannot see at all:
only the raw text is exported.

**Expected**: a tool card reads like the TUI's — type + file/args + status, then the
same number of preview lines the TUI uses — and never streams the whole output
before collapsing. Since the preview count is view policy, the server has to ship
it (the page cannot guess it).

**Fix plan / test approach**

1. Export the policy's preview line count in the block meta
   (`tui/chat_viewport_blocks.go`, `applyViewMeta` for `*ToolExecutionComponent`) and
   have the page use `meta.preview` (falling back to 4) for `headLines`.
2. Keep the card collapsed-by-default (B22 fix) so no content is streamed before the
   collapse; the preview updates live while the tool runs.
3. Tests: block-export test asserting the meta carries the configured preview count;
   goja test that a 20-line tool output renders at most `preview` lines in the
   collapsed card and everything when open.

## B24 — web band: the status bar is cut

**Reported**: "status bar is cut - current status show: `coding-posture - yolo`".

**Observed**: in the blocks plane the bottom chrome band is cell-rendered from the
grid, and the grid's width is the geometry the *client* reported (`/resize`). Any
row wider than the band is clipped by the page (`.grid { overflow: hidden }`), so a
status row that no longer fits loses its right-hand segments (model, tokens,
context, time) while the left segment survives — the reported
`coding-posture - yolo`. **Not reproduced yet**: this is where the cut must come
from given the geometry, but the exact mismatch (reported cols vs band width) still
has to be measured in a real browser — the B20 validation page had a wide viewport,
so it did not show the symptom.

**Expected**: the status bar shows the same segments the TUI does, laid out for the
width the browser actually has.

**Fix plan / test approach**

1. Reproduce in the real browser at a narrow viewport, and check what geometry the
   page reports versus the width the band renders at (`measureCols()` vs the band's
   own box) — a mismatch is the cut.
2. Fix the geometry contract (report the band's width, or re-report on a chrome/band
   height change), not the symptom.
3. Test: goja harness asserting the resize the page sends matches the measured band
   width; real-browser re-check at 700px.

## B25 — web input: macOS composed characters (Option / Shift+Option) are not inserted

**Reported**: "special mac char like pipe (shift+option+l) are not rendering".

**Observed**: on macOS the browser reports the *composed* character — Shift+Option+L
arrives as `key: "|"`, `code: "KeyL"`, `alt: true`, `shift: true` (there is no
browser setting for "Option as Meta"; Terminal.app's default is off, which is why a
real terminal inserts `|`). `EncodeKey` sends every Alt chord as `ESC` + character
(`encodeAltPrintable`), so the engine receives `alt+|` — an unbound key — and the
pipe never appears. Every Option-produced glyph (`|`, `∫`, `€`, `¡`, …) is
unreachable in the web UI.

**Expected**: an Alt chord whose character the physical key could not have produced
was composed by the platform and must be inserted as text, exactly as the terminal
does; a plain Alt chord (Alt+b on Linux/Windows, where the browser reports the
unshifted letter) stays a Meta chord.

**Fix plan / test approach**

1. In `EncodeKey`, before the ESC-prefix path: if `alt` is held, the key is a single
   printable rune, and that rune differs from what the US layout produces for
   `code` + `shift`, the platform composed it → emit the rune itself.
2. Unknown `code` (or punctuation codes) keeps today's Meta behaviour, so no existing
   binding regresses.
3. Table test: `KeyL`+shift+alt+`"|"` → `"|"`; `KeyB`+alt+`"b"` → `"\x1bb"`;
   `KeyB`+alt+`"∫"` → `"∫"`; `Digit2`+shift+alt+`"€"` → `"€"`; alt+`ArrowLeft`
   unchanged (CSI-u).

## Closed

Closed 2026-10-10 — B21, the `goa/protocol` `max_tokens` note: it called a
model's *maximum output* a "reasoning-ceiling default", named no reason a reader
could act on, and printed on every request that omitted `max_tokens` — straight onto
stderr while the TUI owned the screen. `noteMaxTokensDefault` now states the field,
value, reason and identifiers in one sentence, the source strings are constants
shared with the resolver, and a `defaultNotes` map reports each `(model, source)`
once per process. Pinned by the reworked
`TestDefaultMaxTokens_LogsDefaultedField` and a new
`TestDefaultMaxTokens_ReasoningCeilingWording` (the reported line's exact case). See
[`docs/archive/b21-max-tokens-log-clarity.2026-10-10.md`](docs/archive/b21-max-tokens-log-clarity.2026-10-10.md).

Closed 2026-10-10 — B20, the web UI's caret on a keystroke that changes no cell:
`CellGrid.Process` decided "did the screen move" from the emulator's dirty *rows*
alone, so the frame the compositor emits for a space typed over a blank cell —
whose only byte is the absolute cursor CUP — was never published, and the browser's
caret froze until the next keystroke that changed a cell (`agent-browser` measured
`16.44px → 16.44px → 33.31px` across `a`, space, `b`). The grid now carries state
outside the rows (`stateDirty`: cursor position, cursor visibility, title), ships a
patchless state-only frame when only that moved — cheaper than the whole-screen
`emu.MarkDirty` it replaces — and `publish` no longer reads an empty patch set as
"not drained yet". Pinned by three grid tests, a two-plane publish test (RED before
the fix: "a cursor-only write published 0 cells frames") and a goja test that a
patchless frame still repositions `#caret`; re-validated in a real browser
(`a`, four spaces, `z` moved the caret one cell per space, row `a    z`). See
[`docs/archive/b20-webui-caret-cursor-only-frame.2026-10-10.md`](docs/archive/b20-webui-caret-cursor-only-frame.2026-10-10.md).

Closed 2026-10-10 — B19, a `-race` failure in `internal/webui/supervisor`
(found while validating B18, and reproducible on the tree before it): the test
scaffolding's `fakeChild.Stop()` incremented a plain `int` from the reaper
goroutine while the test read the same field, and the `sup.mu` the read held
guards the supervisor's child map, not the fake's counter. `stopped` is now an
`atomic.Int64` read through `stops()`; no production code changed.
`go test -race ./internal/webui/... -count=2` and the full `-race` suite are green.
See
[`docs/archive/b19-supervisor-test-race.2026-10-10.md`](docs/archive/b19-supervisor-test-race.2026-10-10.md).

Closed 2026-10-10 — B19, a flaky `-race` failure in
`internal/webui/supervisor` (test scaffolding, not production): `fakeChild.Stop()`
incremented a plain `int` from the reaper goroutine while the test read it — the
mutex the read held guards the supervisor's child map, not the fake's counter. The
counter is now an `atomic.Int64` read through `stops()`. Also closed in the same
session — B18, image paste with `Cmd+V`: see below. See
[`docs/archive/b19-supervisor-test-race.2026-10-10.md`](docs/archive/b19-supervisor-test-race.2026-10-10.md).

Closed 2026-10-10 — B18, image paste with `Cmd+V`: a terminal only pastes for
you when its own chord can carry the clipboard's *text*, so with a screenshot on
the clipboard macOS leaves the terminal's Paste menu item disabled and the chord
falls through — where a Kitty-protocol terminal reports it as the key event
`ESC [ 118 ; 9 u` (`v` + `super`). The modifier table named values 2..8 only, so
`super` was dropped and the decoder emitted the *bare* `"v"`, which the editor
typed into the input line; `KbPaste` was bound to `ctrl+v` only, so even a
correctly decoded `Cmd+V` could not paste. The table now decodes the whole Kitty
bitmask (locks ignored, unnameable modifiers dropped rather than leaked, shifted
letters normalised so `ctrl+shift+v` is reachable again), `KbPaste` carries
`super+v`, and a decoded *chord* name can no longer be inserted as text.
Validated by `e2e/clipimg.sh`, which now drives both chords through a real PTY and
asserts the image-store path is the whole input line. See
[`docs/archive/b18-cmd-v-image-paste.2026-10-10.md`](docs/archive/b18-cmd-v-image-paste.2026-10-10.md).

Closed 2026-10-05 — B17, the web UI's VT-simulation seam: the browser re-derived UI semantics
(comparator wipes, caret, colours, band geometry) from compositor bytes — the root every fixed
webui bug shared — and could never meet the v2 goals (browser-native resize/history). The reported
symptoms themselves (broken mascot, misplaced input, blinking text, missing status bar) reproduced
only on the stale repo-root binary built before 32c395af; rebuilding removed them, and the block
plane (specs/webui.md §22) removed the seam: `goa server` now ships semantic conversation blocks
rendered as HTML by the browser (native reflow, native scroll, journal resume) with the editor
band as a cell footer; input-capturing overlays fall back to full-cell frames; `--server-cells`
serves the v1 pipeline unchanged. Pinned by the tui block-export tests, the webui BlockTracker/
publish/codec tests (webui at 91.2% coverage), `e2e/w2_webui_blocks.sh` (8 checks, all PASS in a
real Chrome) and w1 pinned to `--server-cells`.
See [`docs/archive/b17-webui-block-plane.2026-10-05.md`](docs/archive/b17-webui-block-plane.2026-10-05.md)
for the assessment, the root causes and the validation record.


**Observed.** On `feature/webui`, a user reports the web UI as unusable: the
logo/mascot area shows a blinking element, typed input does not appear inside
the input line, text blinks, and the bottom status bar is missing.

**Assessment (2026-10-05, headless Chromium at 1280×800 / 700×900 / 1280×300).**
At HEAD the page renders the mascot, the input band and the status bar
correctly, follows the tail, scrolls history and resizes without visible
corruption. The reported symptoms reproduce on the stale repo-root binary
(built 2026-10-04 10:44, *before* commit 32c395af which fixed B5/B7/B8/B9/B10 —
pinned live band, colour parity in history, startup input gate): rebuild
eliminates them. **However** the visual assessment also confirmed the
structural critique: every one of the eight webui bugs closed so far
(B2, B3, B4, B5, B7, B8, B9, B10) lives at the same seam — a browser
re-deriving UI semantics (transcript wipes, DECTCEM caret, colour
inheritance, geometry resets) from an escape-byte stream built for a glass
TTY. The cell pipeline also structurally cannot meet the stated goals:
resize re-renders and re-ships the whole transcript server-side, history is
a simulated bounded row list, and layout is fixed-cell.

**Fix plan (block plane — spec §22).** Keep the TUI engine as the single
renderer, but ship the conversation as *semantic blocks* from the Scene
(the protocol-free IR the compositor already consumes) instead of shipping
transcript cells:

1. `tui`: `SceneBlock` IR (`ID/Kind/Text/Meta/Lines`), `ChatViewport`
   exports its entries (the Model/View split already stores content
   width-independently), the header exports its styled art lines, and
   `renderOneFrame` notifies an optional `SceneObserver` on the Terminal.
2. `internal/webui`: a `Plane` switch (`blocks` default in production,
   `cells` preserved verbatim behind `?mode=cells` and a CLI escape flag).
   A `BlockTracker` diffs Scene snapshots into id-keyed upserts
   (append-delta when text grows, full reset when history is rewritten).
   `publish` ships only the bottom chrome band (editor + status rows) as
   cells — the transcript cells and scrollback are no longer shipped —
   plus the block deltas. Input-capturing overlays (config selector,
   confirm cards) fall back to full-cell frames; the autocomplete popup
   (no input capture) just extends the band.
3. `assets`: the page becomes an HTML document — blocks render as real
   flow content (markdown, tool cards, collapsible thinking) so resize is
   pure browser reflow and history is the browser's own scroll. A small
   cell-rendered footer keeps the editor band pixel-identical to the TUI.
   A minimal SGR→span converter and a dependency-free markdown renderer
   stay inside the page (no framework, no external fonts/CDN).

**Tests.** `tui`: block export golden (kinds, tool meta, header art),
SceneObserver tap. `webui`: BlockTracker (set/append/reset), styled-line
SGR→runs converter, band-filtered publish, overlay transitions, blocks-mode
attach, codec round-trip; all existing cells-plane tests keep passing.
Browser: real-Chromium checks of blocks DOM, band, typing echo, overlay
swap, and resize without transcript re-ship.

**Validation.** `go vet`, `go test -count=1 -race -cover`, staticcheck,
gocognit/gocyclo on touched packages; headless-browser visual pass at three
geometries in both planes; existing `e2e/w1_webui_browser.sh` pinned to the
cells plane, new `e2e/w2_webui_blocks.sh` for the blocks plane.


**Observed.** On `feature/webui`, a user reports the web UI as unusable: the
logo/mascot area shows a blinking element, typed input does not appear inside
the input line, text blinks, and the bottom status bar is missing.

**Assessment (2026-10-05, headless Chromium at 1280×800 / 700×900 / 1280×300).**
At HEAD the page renders the mascot, the input band and the status bar
correctly, follows the tail, scrolls history and resizes without visible
corruption. The reported symptoms reproduce on the stale repo-root binary
(built 2026-10-04 10:44, *before* commit 32c395af which fixed B5/B7/B8/B9/B10 —
pinned live band, colour parity in history, startup input gate): rebuild
eliminates them. **However** the visual assessment also confirmed the
structural critique: every one of the eight webui bugs closed so far
(B2, B3, B4, B5, B7, B8, B9, B10) lives at the same seam — a browser
re-deriving UI semantics (transcript wipes, DECTCEM caret, colour
inheritance, geometry resets) from an escape-byte stream built for a glass
TTY. The cell pipeline also structurally cannot meet the stated goals:
resize re-renders and re-ships the whole transcript server-side, history is
a simulated bounded row list, and layout is fixed-cell.

**Fix plan (block plane — spec §22).** Keep the TUI engine as the single
renderer, but ship the conversation as *semantic blocks* from the Scene
(the protocol-free IR the compositor already consumes) instead of shipping
transcript cells:

1. `tui`: `SceneBlock` IR (`ID/Kind/Text/Meta/Lines`), `ChatViewport`
   exports its entries (the Model/View split already stores content
   width-independently), the header exports its styled art lines, and
   `renderOneFrame` notifies an optional `SceneObserver` on the Terminal.
2. `internal/webui`: a `Plane` switch (`blocks` default in production,
   `cells` preserved verbatim behind `?mode=cells` and a CLI escape flag).
   A `BlockTracker` diffs Scene snapshots into id-keyed upserts
   (append-delta when text grows, full reset when history is rewritten).
   `publish` ships only the bottom chrome band (editor + status rows) as
   cells — the transcript cells and scrollback are no longer shipped —
   plus the block deltas. Input-capturing overlays (config selector,
   confirm cards) fall back to full-cell frames; the autocomplete popup
   (no input capture) just extends the band.
3. `assets`: the page becomes an HTML document — blocks render as real
   flow content (markdown, tool cards, collapsible thinking) so resize is
   pure browser reflow and history is the browser's own scroll. A small
   cell-rendered footer keeps the editor band pixel-identical to the TUI.
   A minimal SGR→span converter and a dependency-free markdown renderer
   stay inside the page (no framework, no external fonts/CDN).

**Tests.** `tui`: block export golden (kinds, tool meta, header art),
SceneObserver tap. `webui`: BlockTracker (set/append/reset), styled-line
SGR→runs converter, band-filtered publish, overlay transitions, blocks-mode
attach, codec round-trip; all existing cells-plane tests keep passing.
Browser: real-Chromium checks of blocks DOM, band, typing echo, overlay
swap, and resize without transcript re-ship.

**Validation.** `go vet`, `go test -count=1 -race -cover`, staticcheck,
gocognit/gocyclo on touched packages; headless-browser visual pass at three
geometries in both planes; existing `e2e/w1_webui_browser.sh` pinned to the
cells plane, new `e2e/w2_webui_blocks.sh` for the blocks plane.


Closed 2026-10-05 — B16, quota run-out time: the Provider Quotas Status cell now
reads `over budget — exhausted at HH:MM` (local time), derived from the same
`projectedRatio` pace projection that produced the words and the "At reset"
column, with `exhaustedAt` (epoch ms) added to `/quota:json` and the cell
falling back to the bare words when there is nothing to project. See
[`docs/archive/b16-quota-run-out-time.2026-10-05.md`](docs/archive/b16-quota-run-out-time.2026-10-05.md).

Closed 2026-10-05 — B15, session-log retention: `sessions.retention` (default
`enabled: true, days: 7`, counted from each file's last write, `0`/`enabled:
false` = keep forever) with `SessionStore.PruneSessions` sweeping at startup and
hourly, never touching the session this process has open. The two retention
fields are tri-state so an explicit `false`/`0` from a higher config layer wins —
the `Days != 0 || Enabled` rule the three older retention structs use cannot
express that. See
[`docs/archive/b15-session-log-retention.2026-10-05.md`](docs/archive/b15-session-log-retention.2026-10-05.md).

Closed 2026-10-05 — B12/B13/B14, the clipboard backends brought to parity with
the reference agent (pi) and with goa's own copy side: a three-state backend
result (`found`/`absent`/`unavailable`) plus a platform seam so every platform's
backends run in tests on any host, no unadvertised type probes, WSL detection with
the Windows clipboard as its last resort, `xsel`/`termux-clipboard-get` text
reads, and image bytes stored verbatim through the shared
`internal.StoreImage` writer instead of being re-encoded (which is what made a
WebP-only clipboard paste nothing). See
[`docs/archive/b12-clipboard-backend-contract.2026-10-05.md`](docs/archive/b12-clipboard-backend-contract.2026-10-05.md),
[`docs/archive/b13-clipboard-read-symmetry.2026-10-05.md`](docs/archive/b13-clipboard-read-symmetry.2026-10-05.md)
and
[`docs/archive/b14-clipboard-image-format-parity.2026-10-05.md`](docs/archive/b14-clipboard-image-format-parity.2026-10-05.md).

Closed 2026-10-05 — B6, terminal image paste: no code change was needed (the
chain `Ctrl+V` → file paths → image → text already existed); the gap was coverage
and discoverability, now closed with
`internal/clipboard_paste_chain_test.go`, `tui/editor_image_paste_chain_test.go`,
`internal/app/pasted_image_test.go`, the real-terminal `e2e/clipimg.sh` check and
`docs/HOTKEYS.md`. See
[`docs/archive/b6-terminal-image-paste.2026-10-05.md`](docs/archive/b6-terminal-image-paste.2026-10-05.md).

Closed 2026-10-04 — B2, the web UI painting a long output two or three times
after `/quota`: the compositor wipes the terminal's scrollback and re-emits the
whole transcript at a new width (and on a mid-transcript edit). A terminal loses
its transcript to that wipe and the re-emitted rows replace it; the browser kept
its own list and appended them, so every already-scrolled line came back — and
B3's erase-and-suppress workaround left a seam that repeated (or dropped) the
boundary rows. `tui.TermEmulator.ScrollbackGeneration` now reports the wipe, the
grid ships the batch that follows it as a *replacement* (wire `sbr`), and
`app.js` clears its transcript before appending. Pinned by
`internal/app/webui_b2_longoutput_test.go` (three scenarios through the real
`EncodeKey` path, including the reported `/quota`-during-streaming case),
`TestClientJS_TranscriptReplace*` for the page, and a real-browser DOM dump at
the screenshot's geometry (984×692: 79 content lines, 0 duplicates). See
[`docs/archive/webui-b2-long-output-once.2026-10-04.md`](docs/archive/webui-b2-long-output-once.2026-10-04.md).
Closed 2026-10-04 — B4, the web UI's input line / bottom status bar: the bottom
band (input row, separators, status row with the right-aligned mode, model line)
now renders exactly like the terminal's at the same geometry and stays inside the
viewport. Root cause: the same startup-canvas overflow as B3 (the boxed banner
made the canvas taller than the browser's grid, so the compositor scrolled the
header into the emulator's scrollback and the transcript pushed the footer band
below the fold); B3's single-row startup info entries removed it. Pinned by
`internal/app/webui_b4_footer_test.go` (web grid == terminal screen, row for row,
at 10 geometries, plus the band's presence/order) and an `e2e/w1_webui_browser.sh`
`footer_band` check in a real browser. See
[`docs/archive/webui-b4-bottom-band.2026-10-04.md`](docs/archive/webui-b4-bottom-band.2026-10-04.md).

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
