<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Feature fix report: stall warning 30s / auto-retry 45s, both visible and editable from /config

Date: 2026-09-26 · Status: CLOSED

## Request (bugs.md `# To fix`)

Change the silent-provider stall timing. The user-visible progress notice was:

```
provider quiet for 15s — still waiting; will auto-retry after 30s of silence
```

It had to become **30s warning / 45s auto-retry**, and both values had to be
visible and changeable from `/config`.

## Root cause / where the numbers came from

- Single producer of that message: `internal/agentic/agent_streaming.go`
  `emitQuietWarning` (formerly `agent_streaming.go:422-427`), armed by
  `quietAfter := stallTimeout / 2` (hard-coded half-window rule).
- `stallTimeout` came from `effectiveEventStallTimeout(opts)` = `opts.IdleTimeout`,
  which `provider/manager_streamopts.go` fills from
  `execution.activity_timeout` — shipped default `"30s"`.
- Neither number was reachable from `/config`: the 15s was a hard-coded fraction
  with no config key behind it, and `execution.activity_timeout` had no
  `/config:set` setter and no menu entry. A mid-session change would not have
  taken effect either: stream options are sampled once at session start
  (`internal/app/prompt.go` → `StartSession`).

## Resolution

1. **New config key `execution.activity_warn_after`** (duration string) paired
   with the existing `execution.activity_timeout`; embedded defaults now ship
   `activity_timeout: "45s"` and `activity_warn_after: "30s"` with explanatory
   comments. Both are merged like any execution scalar
   (`config/config_merge.go`).
   Validation is **layer-scoped** (`config/loader_yaml.go checkActivityPairLayer`,
   called from `mergeFile`/`mergeProjectFile`): a single config source that
   explicitly sets BOTH keys must be self-consistent — `activity_warn_after`
   shorter than `activity_timeout` — and a contradictory explicit pair is
   reported at load time naming the file. The MERGED config is only checked for
   parseability (`config/config_validate.go`). Reason: the two keys cascade
   independently, so a home/project pin of `activity_timeout: 30s` under the
   shipped `activity_warn_after: 30s` is a legitimate install (observed against a
   real `~/.goa/config.yaml` — a merged-pair rule refused to start); the ordering
   invariant is additionally enforced where the values are consumed (see 2).
2. **Proportional fallback instead of a hard-coded half**
   (`internal/agentic/agent_streaming.go`):
   `effectiveStallWarnAfter(opts)` returns the configured
   `ActivityWarnAfter` when it is `> 0` and strictly inside the stall window,
   otherwise two thirds of the window. Shipped: 30s of 45s; a pinned 30s window
   additionally derives 20s; the 2-minute provider default derives 1m20s. The
   warning therefore always precedes the retry it announces.
3. **Wiring**: `provider.BuildStreamOptions` parses the key into the new
   `StreamOptions.ActivityWarnAfter`; `consumeStream` arms the quiet timer with
   `effectiveStallWarnAfter` while `emitQuietWarning` keeps printing the real
   pair (so the message can never drift from the configured values).
4. **Visible and editable live**:
   - `/config:set execution.activity_timeout` and
     `execution.activity_warn_after` setters (duration-validated, sharing the
     new `validateDurationValue` helper), plus `/config:set` completion entries;
   - new `/config → Retry settings` entries ("Auto-retry after provider
     silence", "Stall warning after provider silence") whose descriptions show
     the values in effect — including the derived `30s (derived: 2/3 of 45s)`
     when the lead is unset;
   - runtime sync: both keys (and the existing provider idle/retry-cap entries)
     now rebuild and push stream options into the RUNNING session via the shared
     `syncStreamOptions` helper (`AgentManager.SetStreamOptions`, which keeps the
     conversation's cache identity), so a change applies without a restart.

## Test approach & validation

- `config/stall_timing_test.go`
  - `TestDefaultConfig_StallTimingDefaults` — shipped pair is 45s/30s, parses,
    and the lead is two thirds of the window;
  - `TestMergeExecution_ActivityWarnAfter` — layer override and inheritance
    (direct merge plus a full `Config.DeepMerge`);
  - `TestValidate_ActivityWarnAfter` — merged config: unparseable values
    rejected, cross-layer combinations accepted; layer-scoped: an explicit
    contradictory pair in one source is rejected naming the key, while a source
    that pins only the window combines freely (`loadHomeLayerOnly` drives a real
    cascade load).
- `provider/manager_activity_timeout_test.go`
  - `TestBuildStreamOptions_ActivityWarnAfterIsConsumed` — configured lead
    consumed, unset/invalid leaves 0 (agent derives), window untouched.
- `internal/agentic/agent_stall_warn_test.go`
  - `TestStallWarnAfter_DefaultsToTwoThirdsOfWindow`,
    `…_ExplicitOverride`, `…_IgnoredAtOrBeyondStallWindow` (including negative
    leads; asserts the derived lead stays strictly inside the window);
  - `TestQuietWarningMessage_ByDefaultReports30sAnd45s` — asserts the exact
    message `provider quiet for 30s — still waiting; will auto-retry after 45s
    of silence`, and `…_FollowsConfiguredTiming` for a custom 20s/60s pair.
- `core/commands/config_stall_timing_test.go`
  - `TestConfigSet_ActivityWarnAfterAppliesAndPersists` — the shipped pair
    reaches a RUNNING session; `/config:set` to 20s updates config, persists
    (verified through a real `CascadeLoader`) and updates the live agent;
    an unparseable value changes nothing;
  - `TestConfigSet_ActivityTimeoutPushesLiveOptions` — 60s applies live and does
    not disturb the warning lead;
  - `TestRetrySettingsMenu_ShowsStallTiming` (both entries visible with the
    values in effect; editing from the menu persists and re-renders),
    `…_DerivesWarningWhenUnset`, `…_Labels`,
    `TestConfigKeyCompletions_StallTiming`, `TestRetrySettingsRootLabel_ShowsStallRetry`.
- RED-before-fix evidence: with the pre-fix half-window rule restored, the new
  agentic tests fail (22.5s instead of 30s; 15s instead of 20s; the exact-message
  assertions fail), and with `activity_timeout` reverted to `"30s"` the defaults
  test fails. Both were restored and re-run green.
- Existing suites updated where comments/semantics pinned the old half-window
  rule: `internal/agentic/agent_quiet_warning_test.go` (F5 quiet-warning tests
  still pass with the 2/3 lead).

Gate (run separately, post-change):

- `go vet ./...` — clean;
- `staticcheck ./...` — clean;
- `gocognit -over 15 .` — clean;
- `gocyclo -over 12 .` — clean;
- `go test -count=1 -race -cover -timeout 600s ./...` — 87 packages ok, 0 FAIL,
  0 panic.

## Closure

A silent provider now produces `provider quiet for 30s — still waiting; will
auto-retry after 45s of silence` by default; both numbers are shown in
`/config → Retry settings`, editable there and via `/config:set`, and apply to
the running session immediately. Closed 2026-09-26.
