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

## B15 — Session logs are kept forever: prune them after 7 days by default, behind a config flag

Requested 2026-10-05.

**Observed.** Session logs accumulate with no retention at all. They are written
by `core.SessionStore` to `<project>/.goa/sessions/<timestamp>_<name>.jsonl`
(`core/sessionstore.go:174,212`; the store is rooted by
`internal/app/subsystems_agent.go:149`), and the only ways a file leaves that
directory are:

* `SessionStore.DeleteSession` — an explicit user action
  (`core/sessionstore.go:384`),
* `SessionStore.SaveCurrent` removing its own file when the session held no
  events (`core/sessionstore.go:311`).

There is no age- or size-based pruning, so the directory grows for the lifetime
of the project. The same codebase already has the pattern this needs: pasted
images live in a durable store with `ImageStoreLifetime` (30 days) and
`PruneImages`, called opportunistically at startup (`internal/app/app.go:255`).

**Expected.** A session file that has not been updated within the retention
window is removed, so a project's `.goa/sessions` cannot grow without bound:

* default to **7 days** (matching the request: "removed after 7days not updated
  by default"),
* the window is a **configuration flag**, so it can be widened, disabled or
  shortened without a code change — an explicit `0` meaning "keep everything",
  confirmed with the requester on 2026-10-05: retention is counted in days from
  the file's **last write**, default 7, `0` = keep forever,
* pruning is opportunistic and best-effort like `PruneImages`: never fatal, and
  never in the streaming hot path,
* the flag survives the config cascade (embedded → home → project → local → env
  → flags) and is documented with the other limits,
* a session currently being written is never pruned at any age: its mtime is the
  guard and the active session id is the belt, because the writer holds the file
  open across a long turn while its mtime stands still during a long tool call.

**Test approach.** Unit: retention selection by mtime (inside the window kept,
outside pruned, exact boundary kept), `0` disables, the active session id is
spared at any age, directories and unreadable entries are skipped, and a prune
failure is non-fatal. Config: the flag's default is 7 days and it round-trips
through the cascade. Integration: a temp store seeded at t-8d, t-7d, t-6d plus a
live session, run through the startup prune, asserting exactly the t-8d file
gone. Validation: run the real binary against a scratch project with back-dated
session files and confirm the expected file is removed and that the live session
keeps appending to its own file across the prune.

## Closed

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
