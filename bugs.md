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

## B26 — `tui.modeline` is dead configuration: nothing reads it

**Reported** (while answering B24): "I would expect WEB to follow TUI" — for the
status bar, whose segments come from `tui.modeline`.

**Observed**: `config/config_tools.go:119` defines `ModeLine
ModeLineSegmentConfig` with `left`/`right` segment lists (the user's config sets
`left: [app-badge, mode-badge, current-file]`, `right: [token-bar, activity,
help-hint]`), and **no production code references `ModeLine` at all** — a repo-wide
search for the identifier finds only its declaration. The status bar both the TUI and
the web band show is `tui.Footer`'s own fixed layout (cwd + mode / model + stats), so
a user who configures the modeline sees nothing change, in either renderer.

**Expected**: either the configured segments drive the status bar in the TUI (and the
web band follows it, since it renders the same cells), or the dead config is removed
and the schema stops advertising it.

**Note (2026-10-10, awaiting a decision — do not close silently)**: reported by the
user while answering B24, with the explicit expectation "I would expect WEB to follow
TUI". What the user sees today in the web band is the TUI's own footer —
`<cwd> … <mode> │ <autonomy>` and `<provider> <model> • <thinking level> •
<tokens|context|loop>` — because the band renders the same cells the engine paints;
the configured segments (`app-badge`, `mode-badge`, `current-file` / `token-bar`,
`activity`, `help-hint`) appear nowhere, in either renderer. Two ways to close it,
and the choice is a product decision:

1. **Implement** the segments in `tui/footer_render.go` (the ladder in
   `dropFooterTier` already handles narrow widths) — the web band then follows for
   free, being the same cells; or
2. **Remove** `ModeLine` from `config/config_tools.go` and its docs, so the schema
   stops offering a knob that does nothing.

Until then the item stays open: it is the only entry in this file.

**Fix plan / test approach**

1. Decide with the user which way to close it: implement `modeline.left/right`
   segments (app-badge, mode-badge, current-file, token-bar, activity, help-hint) in
   `tui/footer_render.go`, or drop the field from the config schema plus its docs.
2. If implemented: a footer render test per segment, the ladder (dropFooterTier) kept
   for narrow widths, and a web check that the band shows the same row (it is the same
   cells, so the cells-plane golden is the pin).
3. If dropped: config migration note + `config_tail`/completion entries removed.

## Closed

Closed 2026-10-30 — B34, login panics escaped the host command recovery boundary
through background OAuth and picker-continuation goroutines; direct synchronous
login calls were unguarded too. Panic-to-error boundaries now cover all three
paths. The exact `/login:opencode:oauth` production route returns its unsupported
provider error without crashing. RED-before/GREEN-after panic tests, rendered
output assertions, race-enabled package suites, vet, staticcheck, and the full
repository race suite validate the fix. See
[`docs/archive/b34-login-panic-boundaries.2026-10-30.md`](docs/archive/b34-login-panic-boundaries.2026-10-30.md).

Closed 2026-10-23 — B33, z.ai business-token exchange rejected the provider's
successful `code: 200` response as `Operation successful`, so neither the business
token nor the broker-issued zcode JWT reached the auth store and quota reported
`coding_plan_reset_zcode_jwt_required`. Business exchange now accepts its documented
omitted/0/200 success codes while broker endpoints retain omitted/0. Exact-response
replays, failure-path tests, managed-credential quota tests, and a production-path
filmstrip verify both persisted credentials and a success-only notification. See
[`docs/archive/b33-zai-business-code-200.2026-10-23.md`](docs/archive/b33-zai-business-code-200.2026-10-23.md).

Closed 2026-10-23 — B32, OSC-8 hyperlink targets were counted as visible terminal
columns and overwidth preformatted panel rows were clipped, hiding the z.ai
authorization URL. ANSI truncation/wrapping now measures only visible graphemes and
preserves hyperlink state across rows; login panels wrap long URLs instead of
clipping them. Unit tests and a live-engine production-path filmstrip verify the
complete visible URL and successful credential storage. See
[`docs/archive/b32-zai-oauth-url-wrapping.2026-10-23.md`](docs/archive/b32-zai-oauth-url-wrapping.2026-10-23.md).

Closed 2026-10-23 — B31, z.ai business-exchange failures discarded their cause
and exposed only `zai_oauth_required`. Broker initialization, authorization
polling, business exchange, transport, malformed-response, denial, and
cancellation paths now retain sanitized actionable diagnostics while
`errors.Is(err, ErrZaiOAuthRequired)` remains true. Poll/OAuth/zcode credentials
and authorization-URL secrets are redacted. z.ai and BigModel success still mint
both credentials, the existing auth-store persistence test passes, and an
asynchronous visible-terminal filmstrip proves the actionable flash and a
responsive engine. See
[`docs/archive/b31-zai-oauth-diagnostics.2026-10-23.md`](docs/archive/b31-zai-oauth-diagnostics.2026-10-23.md).

Closed 2026-10-23 — B30, the reported low cache ratios reflected rapid
append-only growth, not cache-key churn or eviction. Ten completed requests
reused 98.65%–100.00% of the previous prompt and reached 687,616 cached versus
184,108 fresh prompt tokens (78.88% weighted reuse), with an empty cache-miss
journal, no bust/shrink flags or compaction, and no evidence of duplicate
result replay. The 53 unique tool results account for 578,751 characters;
large outputs were bounded or explicitly broad, with no exact repeated reads
to deduplicate. The cache
report now distinguishes current-request hits from previous-prompt reuse and
prints weighted session reuse, with test-first regression coverage and all
applicable repository gates run. No production tool or history code changed.
See
[`docs/archive/b30-cache-export-context-growth.2026-10-23.md`](docs/archive/b30-cache-export-context-growth.2026-10-23.md).

Closed 2026-10-23 — B29, the reported multi-tool
`elapsed / waiting / elapsed / elapsed` sequence was accurate concurrency state,
not a queue-position
rendering bug. The exported bash/bash/search/search events start bash1 and both
searches while bash2 remains conflict-queued. New production-scheduler
filmstrips prove both that mixed sequence and a fully serialized four-call batch
(one `elapsed`, three `waiting`) on the visible terminal; no production code
changed. See
[`docs/archive/b29-multi-tool-waiting-elapsed.2026-10-23.md`](docs/archive/b29-multi-tool-waiting-elapsed.2026-10-23.md).

Closed 2026-10-16 — B27/B28, `/login:codex` → "Sign in with OAuth (browser)" froze the
whole TUI, and the auth-kind picker ignored its declared order. The selector callback is
delivered ON the TUI command loop, and `pickAuthKind` ran `c.Run` inline from it, so
`selectCodexMethod` parked that loop waiting for the very callback it alone could
deliver (the same defect hit `/login:<p>:oauth` typed inline and the API-key clarify
prompt). Fixed with one off-loop dispatch helper for the picker continuation plus
`core.AsyncCommand` on `LoginCommand` (`AsyncHint` opts the blocking invocations into the
host's background path); every picker item now sets `PreserveOrder`. Pinned by
`core/commands/login_loop_block_test.go` on a live engine with the production selector
wiring (RED before: `responsive = false`, flow never started) and validated against the
rendered frames. See
[`docs/archive/b27-b28-login-oauth-loop-freeze.2026-10-16.md`](docs/archive/b27-b28-login-oauth-loop-freeze.2026-10-16.md).

Closed 2026-10-10 — B24, the web band's clipped right edge ("YOLO" cut to
"YOL"): the page reported the terminal geometry only when the socket opened and on
`window.resize`, so a classic (width-taking) scrollbar appearing — the conversation
grew past the viewport — narrowed `#screen` with no resize event at all. The server
kept the wider grid, the band rows were wider than the box showing them, and
`.grid { overflow: hidden }` cut the last cells. `sendGeometry()` now reports only
when `cols×rows` changed, and a `ResizeObserver` on `#screen` re-reports on any box
change (scrollbar, zoom, font swap). Real-browser A/B: pre-fix client, box narrowed
to 844px → `statusCells=104` in a 100-cell box, `clipped=TRUE`; fixed client →
`statusCells=100`, `clipped=false`, tail "│ YOLO" intact. Pinned by
`TestClientJS_ReReportsGeometryWhenTheBoxNarrows` (RED before the fix: "the page does
not watch the scroll container") plus the new `ResizeObserver` stub. See
[`docs/archive/b24-webui-band-clipped-geometry.2026-10-10.md`](docs/archive/b24-webui-band-clipped-geometry.2026-10-10.md).

Closed 2026-10-10 — B25, macOS composed characters in the web UI: the browser
reports the glyph the Option layer produced (`Shift+Option+L` → `key:"|"`, `code:"KeyL"`,
alt+shift held), and `EncodeKey` sent every Alt chord as `ESC`+character, so the engine
received the unbound `alt+|` and the pipe (like `∫`, `€`, `¡`, ``) never appeared.
`composedAltKey` now tells the two cases apart from the event itself — a Meta chord
reports the character the physical key makes, a composed one reports a glyph that key
could not have produced (`usLayoutRune(code, shift)` is the yardstick; an unknown code
keeps the Meta path) — and inserts the glyph as text. Pinned by table cases in
`TestKeyEncoder_SpecTable` (RED before the fix: `= "\x1b|", want "|"`) and validated on
the live page: the pipe lands in the input line with the caret advancing, while Alt+B
still acts as the engine's chord. See
[`docs/archive/b25-macos-composed-option-chars.2026-10-10.md`](docs/archive/b25-macos-composed-option-chars.2026-10-10.md).

Closed 2026-10-10 — B22 / B23, the web block plane's collapsibles: every
collapsible was created CLOSED (`wasOpen = b.body && b.body.open` yields `null` for a
new block, which `collapsible` took as a state to restore), so the server's
`meta.expanded` — the TUI's own state, a thinking block starts expanded — was never
read; the reader's toggle could not reach later blocks; the ▸/▾ indicator selectors
matched nothing (the `open` attribute lives on the `<details>`, not the wrapper); and
a tool card previewed the hard-coded *first* 4 lines while the TUI previews the
**tail** with `tui.tools.preview_lines` (now exported as `meta.preview`, default 10).
Fixes: `wasOpen` falls back to `undefined` so the meta applies once, a per-kind
`collapsePref` makes the reader's choice sticky, `tailLines`+`previewLinesFor` mirror
the TUI, and `ToolExecutionComponent.effectivePreviewLines()` is the single source of
the preview count. Pinned by four goja tests (RED before the fix with the reported
symptoms) and the tui export test; the indicator was verified in a real browser. See
[`docs/archive/b22-b23-webui-block-collapse-and-tool-card.2026-10-10.md`](docs/archive/b22-b23-webui-block-collapse-and-tool-card.2026-10-10.md).

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
