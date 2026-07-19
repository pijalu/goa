<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Archived Bugs — 2026-07-19

All three open bugs closed. Fix plans, tests, and validation below.

---

## Bug 1 — Plugin: two commands, no enable/disable UX, no no-plugins CLI

**Report.** `/plugin` and `/plugins` appeared as two commands; the command
should list plugins and allow enable/disable; there should be a command line
to run without any plugins enabled.

**Root cause.** `collectCmdNames` (internal/app/tui.go) appended every command
alias as a separate completion entry, so the popup listed `/plugin` AND
`/plugins`. Bare `/plugin` listed plugins but gave no enable/disable hint and
offered no argument completion. No `--no-plugins` flag existed.

**Fix.**
- `collectCmdNames` no longer lists aliases as completion entries (they still
  resolve when typed); aliases keep their description for help.
- `/plugin` list output now appends a `→ /plugin enable|disable <id>` hint per
  plugin, and `CompleteArgs` completes subcommands (list/enable/disable/
  install/remove) and plugin IDs.
- New `--no-plugins` CLI flag: skips plugin loading entirely
  (`loadEnabledPlugins` gates on `subsystems.noPlugins`) and excludes
  plugin-contributed skill dirs (`initSkillAndCommandLayer(pluginsEnabled)`).

**Tests.**
- `TestCollectCmdNames_HidesAliases` (internal/app/tui_test.go)
- `TestPluginCommand_ListShowsToggleHint`,
  `TestPluginCommand_CompletesSubcommandsAndIDs` (core/commands/plugin_test.go)
- `TestLoadEnabledPlugins_NoPluginsFlag` (internal/app/plugins_integration_test.go)

**Validation (PTY, real terminal).**
- `/plug` popup shows only `/plugin` + modifiers — no `/plugins` duplicate.
- `/plugin` prints `provider-quota (enabled, hash …) → /plugin disable provider-quota`.
- `goa --no-plugins` starts with no quota segment in the footer at all.

---

## Bug 2 — Quota not working on kimi (no message, "0 tok")

**Report.** Quota on kimi does not work — no message and shows "0 tok".
Should show a clear message when auth is required/failed, and show
session(5h)/weekly/monthly percent quota usage.

**Root causes (three, stacked).**
1. `fetchers/kimi.js` was written against a guessed API shape
   (`data.session.used`) and the wrong host (`api.moonshot.ai`). The real
   endpoint is `GET https://api.kimi.com/coding/v1/usages` returning
   `{usage, limits:[{window,detail}], user:{membership:{level}}}` with
   string-typed numerics — parsing yielded zero limits, so the provider
   dropped out of the carousel and the footer fell back to local "0 tok".
2. `providerConfigFor("kimi")` never matched the user's configured provider
   (`id/provider: kimi-code`), so the fetcher got an empty config (no API
   key, no endpoint).
3. `refreshProviderList()` excluded all errored entries from the carousel, so
   an `auth_required` provider was invisible in the footer; the `authMark()`
   helper was dead code.

**Fix.**
- Rewrote `fetchers/kimi.js` against the verified live API (API-key auth —
  the same key used for inference; no OAuth needed). Parses `limits[]` by
  window duration → "Session (5h)" / "Weekly (Nd)" / "Monthly (Nd)" labels,
  top-level `usage` as Weekly, plan from `user.membership.level`
  ("LEVEL_ADVANCED" → "Advanced"). 401/403 → `auth_required`; missing key →
  `no_api_key`.
- `providerConfigFor` now normalizes ids and maps known aliases
  (`kimi-code`/`kimi-for-coding`/`moonshot` → `kimi`).
- Carousel keeps `auth_required` providers in rotation; `statusRender` renders
  them as `Kimi ∇ auth`; `formatShort` handles error entries (`∇ auth`,
  `⚠ <err>`).
- `MaterializeBundled` fast path now verifies the on-disk content hash against
  the lockfile and re-materializes on drift — the stale `provider-quota@1.0.0`
  copy kept serving the old fetcher even after the source was fixed (found
  during validation). Plugin version bumped to 1.1.0.

**Tests.**
- `TestFetcherKimi_ParsesRealAPIShape`, `TestFetcherKimi_NoAPIKey`,
  `TestFetcherKimi_AuthFailed`, `TestFetcherKimi_MonthlyWindow`,
  `TestQuota_KimiCodeConfigMatchesKimiFetcher`,
  `TestQuota_SegmentShowsAuthMarker`,
  `TestQuota_SegmentShowsWindowedPercent` (plugins/quota_kimi_test.go)
- `TestMaterializeBundled_RematerializesOnContentDrift`
  (plugins/bundled_load_test.go)

**Validation (PTY, real terminal, live API key).**
- Footer: `Kimi 5h:6% / wk:21%` rotating with `OpenCode ∇ auth`.
- `/quota`: `Kimi (Advanced) Session (5h) █░░░░░░░░░ 6% → +1h 57m, Weekly
  ██░░░░░░░░ 21% → +4d 23h`.
- Live endpoint confirmed with curl: `GET https://api.kimi.com/coding/v1/usages`
  returns 200 with the documented shape using the inference API key.

---

## Bug 3 — Quota crash (nil pointer in goja VM)

**Report.** `panic: runtime error: invalid memory address or nil pointer
dereference [recovered, repanicked]`, SIGSEGV in
`plugins.(*JSBridge).setupUI.func1.1` (bridge_extended.go:280) via
`pushPluginSegments` on the `drainSegmentRefreshes` goroutine.

**Root cause.** The segment `Render` closure called the JS render function
WITHOUT acquiring the global VM lock (`lockVM()`), unlike scheduler callbacks
(`invokeSafe`), hotkeys, tool/command/observer wrappers. The app render loop
(`drainSegmentRefreshes` goroutine) ran JS concurrently with a scheduler timer
callback on the same goja runtime; the timer's VM mutation clobbered `vm.prg`
mid-`__call`, and goja's `vm.halted()` dereferenced nil `vm.prg.code`
(goja vm.go:610). The comment at app.go:207 ("acquires the VM lock inside the
bridge") described intent, not reality.

**Contributing factor (found during testing).** The bridge's JS test file was
named `bridge_js_test.go` — the `_js` suffix makes the Go tool treat it as
GOOS=js-constrained and silently EXCLUDE it on darwin/linux, so the bridge
tests never ran in CI (they were also broken: read `vm.Get("__result")` while
the JS assigned `goa.__result`). Renamed to `bridge_runtime_test.go`, fixed
the bit-rot via a `goaResult` helper, and removed the outer `lockVM()` around
`segs[0].Render()` in `TestJS_RegisterSegmentAndHotkey` (vmMu is not
reentrant; Render now self-locks).

**Fix.** `def.Render` in `setupUI` now acquires `lockVM()` and recovers
panics, matching every other JS entry point — a misbehaving plugin render
returns "" instead of crashing the UI goroutine.

**Tests.**
- `TestSegmentRender_SerializedWithScheduler` — hammers the render closure
  from 4 goroutines × 200 iterations while a JS interval timer mutates state,
  under `-race` (plugins/segment_render_test.go).
- `TestSegmentRender_JSPanicContained` — throwing render returns ""; healthy
  segment unaffected.
- All previously-dead bridge tests now run and pass
  (plugins/bridge_runtime_test.go).

**Validation.**
- `go test -race ./plugins/` green; full `go test -race ./...` green.
- PTY run of the real TUI: no crash, quota segment renders and rotates.

---

## Code-quality gates (guideline #6)

- `go vet ./...` — clean.
- `staticcheck ./...` — 8 pre-existing `unused` (U1000) warnings, all in code
  untouched by this change (verified against baseline; one, `saveInputHistory`,
  dates to the initial commit).
- `gocognit -over 15 .` — identical set to baseline (no new entries).
- `gocyclo -over 12 .` — baseline identical after extracting `newSkillRegistry`
  from `initSkillAndCommandLayer` (the added `pluginsEnabled` branch had
  pushed it to 13; the extraction brought it back under budget).
- `go test -count=1 -race -cover ./...` — all green.
