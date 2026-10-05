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

## Closed

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
