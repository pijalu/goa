<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Fixes — 2026-07-19

All open bugs from `bugs.md` addressed. Each entry below states the root cause,
the fix, the test approach, and the validation performed. Quality gates
(`go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`,
`go test -count=1 -race -cover ./...`) were run separately; no new violations
were introduced (the single `applyToolResultToWidget` U1000 note is
pre-existing and unchanged at HEAD).

## 1. hotkey — uppercase → lowercase

**Root cause.** User-facing hotkey labels were hardcoded with capital letters
(`Ctrl+O`, `Alt+E`, `Ctrl+x`, `Ctrl+C/D`) across tool renderers and TUI hints,
while internal key names were already lowercase.

**Fix.** Lowercased every display label and centralized the expand hint:
- Added `tools.KeyExpandLabel = "ctrl+o"` (`tools/renderer_common.go`) and used
  it in all six tool renderers + `python_renderer.keyExpand()` +
  `writefile.go` preview hint.
- `tui/chat_viewport_components.go`: `(Alt+E to edit)` → `(alt+e to edit)`.
- `tui/orchestrator/content.go`: `Ctrl+x tabs` → `ctrl+x tabs`.
- `tui/header.go`: `Ctrl+C/D exit … Tab complete` → `ctrl+c/d … tab complete`.
- `tui/pty_view.go`, `core/commands/pty.go`: `Ctrl+C to close` → `ctrl+c …`.

**Tests.** Updated display-assertion tests to the new spec
(`readfile_renderer_test`, `writefile_renderer_test`, `chat_viewport_test`,
`tool_streaming_repro_test`, `orchestrator/content_test`). Pass-through tests
that used `Ctrl+O` only as a function argument were intentionally left.

**Validation.** Live PTY boot: header renders `ctrl+c/d exit`, no `Ctrl+C/D`.

## 2. alt-e does not work on macOS

**Root cause.** `tui/keys.go` `optionKeyAliases` maps macOS Option-key glyphs
back to `alt+<base>`, but had no entry for `ê` (produced by Option+e on
international layouts), so Alt+E was dead there.

**Fix.** Added `"ê": "alt+e"` to `optionKeyAliases`. (Did **not** rebind to
ctrl+e as the bug suggested — ctrl+e is already `KbCursorLineEnd`; the alias
fix is minimal and preserves the existing shortcut.)

**Tests.** `TestAltKeyName` table extended with `{"ê", "alt+e"}` (RED → GREEN).

## 3. inputline search does not work

**Root causes (two).**
- `ctrl+s` was only consumed when already in search mode
  (`tui/editor_input.go`); otherwise it fell through and the literal string
  `ctrl+s` was inserted as printable text.
- `HistorySearcher.Complete("")` returned `nil`, so `ctrl+r` showed no popup
  until the user typed.

**Fix.**
- `tui/editor_input.go`: `ctrl+s` now enters search mode when not already in
  it (mirrors ctrl+r); cycles only when already searching.
- `tui/editor_search.go`: `Complete("")` returns the last 10 unique history
  entries (new `recent()` helper), so the popup is populated immediately and
  stays populated when the query is emptied.
- `tui/editor_autocomp.go`: `scheduleAutoComp` no longer early-returns on a nil
  base completer when in search mode (search has its own searcher).

**Tests.** New `tui/editor_search_test.go`: empty-query shows last-10, dedupe,
ctrl+s enters search (no literal leak), ctrl+r shows popup on empty query,
popup survives emptying the query, arrow-key navigation. All RED → GREEN.

## 4. terminal resize

**Status (updated).** Initially mis-diagnosed as "already fixed" — the existing
tests only asserted the visible window and the resize byte stream, never the
terminal scrollback. A concrete repro (long content > screen, then widen)
proved the bug was REAL.

**Root cause.** `Scene.compose` only materializes canvas rows in
`[placeStart, visibleEnd)`; older history rows stay `""`. On a width change,
`drawWindowResetScrollback` wipes scrollback (`\x1b[3J`) and re-emits the whole
off-screen transcript from that canvas — so the unmaterialized rows were
re-emitted as BLANK, erasing visible history (the "black screen"). The
byte-level test (`TestCompositor_WidthResizeResetsScrollback`) passed because
it only counted `\r\x1b[2K` writes, not their content; the filmstrip tests only
checked the visible window, never scrollback.

**Fix.** On a width-change frame the compositor now tells `compose` (via a new
`Scene.WidthChanged` flag, computed before `compose` runs) to materialize the
FULL canvas from row 0, so the scrollback reset re-emits real content instead
of blanks. Minimal, stateless (no cross-frame field — the TUI builds a fresh
Scene each frame).

**Tests.** New `tui/compositor_resize_emulator_test.go`
(`TestCompositor_ResizeReflowsHistoryIntoScrollback`) replays the writes
through `TermEmulator` (scroll-region + scrollback aware) across a growth
sequence + width resize and asserts the oldest history survives in scrollback
and is not replaced by blank rows. RED before the fix, GREEN after.

**Validation.** Live PTY 94→116 col resize after `/hotkeys`: the top-of-history
content (`Navigation`, first table row) survives; previously it was wiped.

## 5. Quota — provider switch / local ∞ / unknown hidden

**Root cause.** The footer segment re-resolves the active provider on each
render (`activeFetcherId()` reads `goa.config().activeProvider` live), so the
stale-read was transient; but local providers showed a bare token count and
unknown providers silently fell back to local with no explanation.

**Fix** (`plugins/bundled/provider-quota/plugin.js`).
- Local providers now render `[∞]` (green) in the segment.
- `/quota` prints "_Quota tracking is not supported for provider `<id>`_" when
  the active provider has no quota API (`appendUnsupportedNote`).

**Tests.** New `TestQuota_ProviderSwitchUpdatesSegment` (segment tracks the new
provider after a switch), `TestQuota_UnsupportedProviderStatesNotSupported`,
`TestQuota_SegmentColorLocalInfinite` (local → green ∞). Updated color/format
tests. All PASS.

## 6. Quota color — unexplained orange

**Root cause.** The footer color is derived from *projected window-end usage*
(green/orange/red), not the raw percentage, and a fetch error also renders
orange — so the color's meaning was invisible to the user.

**Fix.** Added `budgetSummary()` to the plugin; `/quota` now appends a
human-readable status line (e.g. "_plenty of room_", "_close to Session (5h)
limit_", "_over budget — projected to exceed …_") derived from the same
projection as the color, so the color is explained.

**Tests.** New `TestQuota_BudgetSummaryPlentyOfRoom`. Existing color threshold
tests (`TestQuota_SegmentColor*`) unchanged and PASS.

### Quota display refinements (follow-up, same session)

Four display changes requested on the `/quota` screen + footer segment:

1. **Per-window color.** The footer segment `[8%|30%]` now colors each
   window's percentage by its OWN projected level (green/orange/red), not one
   shared worst-window color. Added `goa.segmentColor(name)` to the JS bridge
   (`plugins/bridge_extended.go`) so the plugin builds a pre-colored string
   (`colorizedSegment`); falls back to the single-color `{text,color}` object
   on hosts without the new bridge fn.
2. **Merged Usage + % columns.** `/quota` no longer shows the redundant
   `42/100` fraction plus a separate `%` column; Usage is now `bar + 42%`.
3. **Per-provider/per-window Status.** Replaced the single global
   `_plenty of room_` footer line (which followed only the active provider and
   appeared/disappeared) with a per-window `Status` column ("plenty of room" /
   "close to limit" / "over budget") driven by the same pace projection.
4. **"At reset" column.** `/quota` now shows the projected usage % at window
   reset (`atResetPct`, same projection as the footer color).

**Tests.** New `TestQuota_TableMergesUsageAndPct`,
`TestQuota_TableHasAtResetAndStatus`, `TestQuota_SegmentPerWindowColorsDistinct`;
updated `TestQuota_SegmentColorInBudget` to assert per-window coloring. All
PASS. Live PTY: footer shows `[green 8%|orange 30%]`; `/quota` shows the merged
Usage, At reset, and Status columns.

## 7. Steering status line (redundant ⏳)

**Root cause.** Pending steering was shown twice: a footer `⏳ <text>` line and
the pinned chat bubble. The bubble is the source of truth.

**Fix.** Removed the `FooterData.SteeringPending` field and its render block
(`tui/footer_render.go`), plus all set/clear wiring in
`internal/app/submithandler.go` and `internal/app/events.go`. The chat bubble
(`AddSteeringPending`/`ClearSteeringPending`) is unchanged and remains visible
until the message is consumed.

**Tests.** Added `ChatViewport.HasSteeringPending()` and updated
`steering_test.go` to assert the bubble (not a footer field) is cleared on edit
recall. Footer tests updated (`footer_git_test.go`). All PASS.

## 8. Quota simplification on short screen

**Fix.** The footer segment now uses the compact form always:
`[8%|24%]` (session|weekly percentages, window labels dropped) instead of
`[5h:8% / wk:24%]`; single-window providers show `[8%]`. This is the
"compressed" target from the bug and fits short screens. The full windowed form
remains in `/quota`.

**Note on spec.** The bug text labelled the format "session - weekly -
monthly" but showed only two values, and no monthly window exists in any
current fetcher; the implemented two-value compact form matches the concrete
`[8%|24%]` example. True width-adaptive rendering (a third, even-smaller tier)
would require passing footer width into the JS segment `render()` — a bridge
API change that does not exist today; noted as a possible follow-up.

**Tests.** Updated `TestQuota_SegmentShowsWindowedQuota`,
`TestQuota_SegmentShowsWindowedPercent`, `TestQuota_SegmentColorInBudget`,
`TestQuota_CarouselPrefersAPIProvidersOverLocal`,
`TestQuota_SegmentMultiProviderShowsActiveOnly`,
`TestQuota_SegmentTracksActiveProviderOnly`. All PASS. Live PTY: footer shows
`[1%|28%]`.

## 9. Additional spinner animations

**Fix.** Added three spinners to `internal/spinner/spinners.json` (verified
absent before). Frame lists are palindromes where the bug asked for
back-and-forth, because spinner playback is strictly cyclic
(`frames[i % len]`):
- `orbit`: ⊙ ⊚ ⊛ ⊚
- `quadrant`: ◴ ◷ ◶ ◵
- `flare`: ✴ ✳ ✵ ✷ ✸ ✹ ✺ ✹ ✸ ✷ ✵ ✳

**Tests.** New `internal/spinner/spinner_test.go`: builtin load, the three new
spinners exist with exact frames and positive interval, `Names()` lists them.
All PASS.

## 10. Full model stats (/usage)

**Fix.** Implemented a cross-session usage feature backed by SQLite
(`mattn/go-sqlite3`, already a dependency):
- New `internal/usage` package: `Store` (SQLite at `~/.goa/usage.db`) recording
  per-turn `Record{Project, Provider, Model, PromptN, PredictedN, CacheRead,
  CacheWrite}`; `Query(ByProject|ByProvider|ByModel)` and `Sum` aggregations.
- App records each completed turn's tokens via `recordTurnUsageLocked()`
  (best-effort, lazily opened, closed on shutdown) in `internal/app/stats.go`.
- New `/usage` command (`core/commands/usage.go`): global totals plus
  per-project / per-provider / per-model tables; scopes `all|project|provider|
  model|here`. Help at `core/commands/help/usage.long.md`; registered in
  `core/commands/register.go`.

**Tests.** `internal/usage/usage_test.go` (add/query/filter/sum/empty/idempotent
open) and `core/commands/usage_test.go` (global shows all sections, scope
filtering, `here` scoping, empty-store guidance, unknown scope, token
formatting). All PASS.

**Validation.** Live PTY: `/usage` renders `## Global usage (all projects)` and
appears in the `/`-completion list.
