# B45 — Configured modeline degrades ungracefully on resize

Closed: 2026-11-01

## Report

With `tui.modeline` configured (the recent template feature), resizing the
terminal produced three defects the original two-row status bar does not have:

1. **Partial `SetData` wiped the autonomy badge.** Routine footer rebuilds
   that construct a fresh `FooterData` carrying only the field they own — the
   companion orchestrator's `SetData(FooterData{CompanionActivity: ...})`
   ticks and the orchestration tab's `SetData(FooterData{OrchestrationStats: ...})`
   — passed through `preserveFooterData`, which preserved git/minor-mode,
   models, workflow, goal, team and active-agent state but not `Mode`/`Profile`.
   The modeline's mode badge (`coding-posture │ YOLO`) blanked mid-stream and
   stayed blank until the next full stats tick.
2. **Plugin segments were cut mid-escape.** `stripPluginSegments` located the
   cut index on the ANSI-stripped text (`LastIndex(ansi.Strip(s), text)`) but
   sliced the raw styled string at that index. A stripped-coordinate index has
   no meaning in raw bytes: the slice landed inside an SGR sequence and
   dangled a partial `ESC [ 3 8 ; 2 ; 6` into the status bar, leaking color
   state whenever the width ladder stripped plugin segments on a narrow
   terminal.
3. **The steering hint squeezed the model identity.** In
   `renderConfiguredFooter` the steering hint was built as the whole left side
   of the model row and passed to `fitStatusLineSegments` with `nil` left
   segments — the documented "never dropped" contract. Under width pressure
   the ladder exhausted every tier-8 decoration and then ellipsized the model
   name (`⟳ deepsee…`) while the hint kept its 13 columns.

## Resolution

- **`preserveFooterModeAndProfile`** (tui/footer_data.go): `Mode` and `Profile`
  join the preservation chain. Neither field has a dedicated setter — every
  writer passes the values it computes — so an empty field always means "this
  caller does not know", and preserving the previous value keeps the badge
  stable while a rebuild carrying the real values still overrides it.
- **Raw-boundary plugin strip** (tui/footer_render.go): the strip now matches
  each segment's raw bytes (`LastIndex(s, seg.Text)`). `appendPluginSegments`
  appended `seg.Text` verbatim, so its byte offset is always a clean boundary;
  cutting there and backing off the raw `" • "` separator can never split an
  SGR sequence.
- **Steering hint joins the shared ladder** (tui/footer_modeline.go,
  footer_render.go): the model row's left side is now a tiered segment
  (`FooterSegment{Tier: FooterTierHelp}`) and `fitStatusLineSegments` gained
  the tier-0 drop as its first casualty, ahead of the amount/provider/tokens
  ladder. The hint is dropped whole when the row does not fit; the model
  identity is shortened only after every droppable field is gone. Legacy
  callers (no tier-0 segments) are unaffected.

## Validation

- RED (all failed before the fix, observed in `-v`): partial-SetData badge
  wipe (`YOLO`/`coding-posture` gone from the modeline row); plugin strip
  remainder was `\x1b[38;2;63;1` instead of `llama3`; steering squeeze kept
  `type to steer` while the model showed `⟳ deepsee…`; width sweep found
  dangling escape bytes in the visible line at width 16.
- GREEN (tui/footer_resize_test.go): badge preserved across a
  `FooterData{CompanionActivity}` rebuild and still overridden by an explicit
  mode change; strip cuts at the clean style boundary; at width 24 the hint is
  dropped and the full model identity survives; a 1–200 column sweep of the
  configured footer asserts every line fits its terminal and no strip ever
  leaves a dangling escape; the modeline degradation-order guard pins
  help → activity → tokens dropping while badge/quota survive.
- Filmstrip (internal/app/footer_modeline_filmstrip_test.go): the production
  component tree at 46×12 with a configured modeline keeps the badge visible
  and every viewport line inside the terminal across the initial SetData, a
  companion tick in the exact partial shape the orchestrator issues, and a
  full stats rebuild.
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`
  and the full `-race` repository suite pass; the only complexity reports are
  the documented pre-existing hotspots unrelated to this change
  (provider `TestResolveStartPlanAPIKey`, core `(*GoalDriver).Drive`,
  `initGoalSystem`, `(*AgentManager).executeRunner`, startplan
  `TestDeviceMid_PersistsAndStable`).
