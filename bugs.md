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

## B16 — Provider Quotas shows *that* a window will run out, not *when*: add "exhausted at HH:MM"

Requested 2026-10-05.

**Observed.** In `/quota`'s "Provider Quotas" table the per-window Status cell
says only `over budget` when the current pace projects past the limit
(`plugins/bundled/provider-quota/plugin.js:875` `windowStatus` →
`projectedRatio(lim) > 1.0`), and the "At reset" column gives the projected
usage at window end (`atResetPct`, `:866`). Both are projections that already
know the window will be exhausted before it resets — `projectedRatio` (`:464`)
scales `used/limit` by the elapsed fraction of the window, derived from
`resetsAt` + `periodMs` — but the run-out *moment* is never shown, so the user
has to derive "how long do I still have" from a percentage and the "Resets in"
column. The footer segment is no help either: it carries percentages and colours
only (`parts.push({ pct: pct + "%" })`, `:382`).

**Expected.** The over-budget state names the moment it happens, in local time:

* the Status cell reads `over budget — exhausted at 14:32`, next to the existing
  words (so `plenty of room` / `close to limit` are unchanged and no window that
  is not over budget grows a time),
* the time is the projection's own: the window runs out when the current pace
  consumes the remaining budget, i.e. before `resetsAt` by exactly the margin the
  ratio already carries — one projection, two renderings, never two formulas
  that can disagree,
* the clock is the user's local time, and a minute is enough precision,
* no time is shown when the projection cannot be made (no `resetsAt`/`periodMs`,
  zero elapsed, a window already past its reset) — the cell degrades to today's
  `over budget` rather than inventing a time,
* `/quota:json` exposes the same moment as a machine-readable field so scripts do
  not parse the sentence.

**Test approach.** Unit (JS, plugin test harness or a Go-loaded runtime with the
plugin's own clock seam `format._setNow`): a window at 2× the sustainable pace
yields `exhausted at` the derived clock time and exactly `over budget — exhausted
at HH:MM`; a window at 0.5× stays `plenty of room` with no time; a window whose
snapshot lacks `resetsAt`/`periodMs` reports `over budget` with no time when its
raw usage exceeds the limit; and the JSON field matches the rendered minute.
Integration: `internal/app/plugins_quota_filmstrip_test.go`'s harness
(`quotaCommandOutput` through the real registry) renders a fixture whose window is
over budget and asserts the sentence reaches the visible TUI. Validation: run the
real `/quota` against a stubbed over-budget snapshot and read the rendered table.

## Closed

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
