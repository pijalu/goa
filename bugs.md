<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking

## Guideline

1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

# Open Bugs

## Quota — provider switch still shows previous provider's limits (follow-up)

Switching to a local provider via /model (e.g. lmstudio google/gemma-4-e4b)
still shows the previous provider's quota (`[9%|30%]` from Kimi) in the footer,
even though the model line correctly shows the new provider.

### Root cause
Footer plugin segments are cached strings: `pushPluginSegments` only
re-evaluates the JS `Render()` when the plugin calls `goa.ui.refreshSegment`.
A /model provider switch fires `FooterRefresh` → `refreshFooterFromConfig`,
which rebuilds `FooterData` but PRESERVES the cached `PluginSegments` (by
design, so token-stats churn doesn't blank them) — so the stale quota string
persists until the plugin's next 60s tick. The earlier fix's test
(`TestQuota_ProviderSwitchUpdatesSegment`) called `renderSegment()` directly
and bypassed this app-level cache, so it passed while the live path stayed
broken.

### Fix plan
- In `refreshFooterFromConfig` (internal/app/events.go), re-push plugin
  segments via `pushPluginSegments` before rebuilding FooterData, so a
  provider/model change re-evaluates the segment against the new config.
- Test approach: app-level Filmstrip test that changes `cfg.ActiveProvider`
  and calls only `refreshFooterFromConfig` (no plugin `refreshSegment`),
  asserting the footer segment text switches and the stale value is gone.
- Validation: prove the test is RED without the fix (temp-revert), GREEN with
  it; then live PTY check of /model to a local provider.

**Status:** FIXED — see `docs/archive/bugs.2026-07-19.md` (quota section) and
`TestFilmstrip_ProviderSwitchRefreshesSegment`.

## /plugin does not work as expected (interactive enable/disable + screen corruption)

`/plugin` currently dumps its list as raw multi-line text via `ctx.Writef`,
which corrupts the screen (lines overwrite/duplicate outside the layout — see
capture). It should instead show an interactive list of plugins and let the
user enable/disable each, the same way `/config` → Tools toggles optional
tools, and persist the choice to disk.

### Current broken output (screen corruption)
```
│ ⚡ Switched to model: k3
  /plan  Manage structured work plans
› /plugin  Manage plugins
│ │ /plugin
  /profile  Alias for /mode
── Modifiers ──
│ Installed plugins:   provider-quota (enabled, hash b916e0aa)  → /plugin disable provider-quota
╰───╯│ │ /plugin                                                          │╰───╯   ← duplicated/overwritten rows
```

### Root cause
`PluginCommand.list` writes a multi-line string with `ctx.Writef`. The TUI
renders chat content as laid-out components; a raw multi-line dump bypasses
that layout and collides with the completion popup / input border, producing
duplicated and overwritten rows.

### Fix plan
- Make `/plugin` (no args) open an interactive selector (like
  `/config` → Tools `settingTools`): one row per installed plugin showing its
  enabled/disabled state; selecting a row toggles it.
- Persist via the existing `plugins.Manager.Enable/Disable` (which already
  write the lockfile to disk) — no new persistence needed.
- Keep the text subcommands (`enable|disable|install|remove|list`) for
  scripting; only the no-arg form becomes interactive.
- Test approach: unit-test the selector items builder (one item per plugin,
  state label) and the toggle handler (Enable/Disable called, lockfile saved,
  re-opens selector); prove the no-arg path uses SelectOption instead of
  Writef.
- Validation: live PTY — `/plugin` shows a navigable list, toggling a plugin
  persists across restart, no screen corruption.

## /plugin enable|disable completion should filter by state

`/plugin enable <Tab>` and `/plugin disable <Tab>` must complete installed
plugin ids, but the candidates must be filtered by current state: `enable`
offers only DISABLED plugins, `disable` only ENABLED ones. Today all ids are
offered for both (see `completePluginIDs` in core/commands/plugin.go, which
ignores the subcommand).

### Fix plan
- Pass the subcommand into `completePluginIDs` and filter `Manager.List()` by
  `Enabled` (enable → !Enabled, disable → Enabled).
- Test: table-driven over enable/disable with a mixed enabled/disabled set,
  asserting only the correct subset is offered.

**Status:** FIXED — `completePluginIDs` takes a `*bool` state filter; enable→
disabled-only, disable→enabled-only, remove→all. Tests:
`TestPluginCommand_CompletesSubcommandsAndIDs`,
`TestPluginCommand_CompletionFiltersByState`.

## /tools help renders raw markdown instead of styled output

`/tools:<name>` prints the tool's long description as raw markdown (headers
`#`, `##`, emphasis markers `_…_` are shown literally) instead of rendered
styling. The chat markdown pipeline is not applied to the tool-help output.

### Fix plan
- Route the tool long-description through the same markdown renderer used for
  assistant/chat content, so headers/bold/italic/lists render.
- Depends on the font-style extension below for italic/bold support.

**Status:** FIXED — `printToolDocs` now routes `LongDoc()` through
`tui.NewMDStreamRenderer` (`renderMarkdownForTerminal`), so headings/emphasis
render as SGR styling. Test: `TestPrintToolDocs_RendersMarkdown`.

## Terminal font styles: bold/italic/underline/strikethrough + markdown support

The TUI only supports a subset of SGR attributes. Add first-class support for
bold (`\e[1m`), italic (`\e[3m`), underline (`\e[4m`), and strikethrough
(`\e[9m`) in the style/ANSI layer, and wire the markdown renderer to emit them
(so `_italic_`, `**bold**`, `~~strike~~` work). Sample escapes:
```
echo -e "\e[1mbold\e[0m"
echo -e "\e[3mitalic\e[0m"
echo -e "\e[4munderline\e[0m"
echo -e "\e[9mstrikethrough\e[0m"
```

### Config
Add a terminal config to enable/disable these extensions (esp. italic, which
some terminals render poorly). e.g. `tui.font_styles` with per-style toggles
(bold/italic/underline/strikethrough), default on except italic if detection
is unreliable.

### Fix plan
- Extend the internal/ansi style model + theme to carry the 4 attributes and
  emit the matching SGR codes (with a matching disable on reset).
- Markdown renderer: map `**`/`*`/`_`/`~~` to the new attributes, honoring the
  config toggles.
- Tests: ANSI sequence unit tests per style; markdown rendering tests showing
  `_x_`→italic, `**x**`→bold, `~~x~~`→strike; config-off paths emit no codes.

**Status:** FIXED —
- Added `ansi.Strikethrough` + per-style resets and an `ansi.FontStyles` gate
  (`internal/ansi/fontstyles.go`) with `Style{Bold,Italic,Underline,
  Strikethrough}` helpers.
- Markdown inline renderer emits the gate for bold/italic/underline/
  strikethrough; `_italic_` now works (with CommonMark flanking so snake_case
  stays literal); `~~x~~` uses real `\e[9m` (was faint).
- Config `tui.font_styles.{bold,italic,underline,strikethrough}` (default all
  on), wired at startup via `initFontStyles`; `mergeTUI` now deep-merges
  `font_styles` so a config layer without them doesn't clobber the home layer
  (this was the reason the toggle initially appeared not to work live).
- Tests: `TestRenderInline_Strikethrough`, `_ItalicUnderscore`,
  `_ItalicUnderscoreNotIntraWord`, `_FontStyleGate`, `TestDeepMergeFontStyles`.
  Live PTY confirmed `italic:false` suppresses italic while bold stays.

## Startup feels slow — init should be async for a "direct" first frame

Goa's startup does too much synchronous work before the first usable frame:
plugin loading, and "time-dependent" tasks (history scan, token-stats priming,
quota fetch, dream scheduler, etc.) block the boot sequence. Startup should
show the first frame / accept input as fast as possible and run plugins +
time-dependent work in the background.

### Current behavior
Boot is serialized: config load → subsystem init (incl. plugin manager +
plugin load) → TUI build → first render. Any slow step (plugin VM spin-up,
disk scans, network-ish priming) delays the first interactive frame, so the
app feels sluggish on open.

### Fix plan
- Profile the boot path to find the real blocking steps (plugin load,
  history scan, scheduler start, any synchronous I/O before first render).
- Move plugin activation and time-dependent background tasks (history load,
  schedulers, quota refresh) off the critical path: render the first frame
  and accept input immediately, then start them in a goroutine that reports
  back via the existing event loop.
- Preserve correctness: no data races on shared subsystems; the UI must show
  a sensible placeholder (e.g. quota "[…]") until the async work lands, and
  tests must still see deterministic completion (sync hooks for tests).
- Test approach: a startup ordering test proving first render happens before
  plugin/background init completes; keep existing tests deterministic via the
  sync path.
- Validation: time-to-first-frame before/after; live boot feels immediate.

**Status:** FIXED —
- Profiled the boot: config+subsystem init was ~550ms; narrowed it to
  `loadEnabledPlugins` inside `assembleSubsystems` (goja VM + fetcher
  `require`s + quota prime = ~0.5s). Everything else was sub-millisecond.
- Moved the plugin load off the critical path: `assembleSubsystems` no longer
  calls it; `Run()` now spawns `startAsyncPluginLoad` AFTER the first
  `RenderNow`. The load runs in a goroutine and activates the plugin UI on the
  command loop via `ApplySync` (TUI stays single-owner).
- Thread safety: `pluginRT` is now guarded by `pluginRTMu` (set/getPluginRT);
  `pushPluginSegments`/`activatePluginUI` read via the accessor; segment shows
  "[…]" until the load lands (existing placeholder).
- Result: time-to-first-frame ~550ms → ~8ms (~65×). The quota segment appears
  asynchronously once loaded (validated live: `[28%|34%]` after switching to a
  quota-backed provider).
- Tests: `TestStartAsyncPluginLoad_LoadsInBackground` (returns immediately,
  `pluginsLoaded` closes, runtime+command registered),
  `TestStartAsyncPluginLoad_NoPluginsFlagSkips`. Existing plugin/quota tests
  updated to the setter and pass with `-race`.
