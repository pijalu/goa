# B26 — `tui.modeline` was dead configuration

Date: 2026-10-30  
Status: **FIXED — implemented and validated**

## Report

The user expected WEB to follow the TUI status bar configured through
`tui.modeline.left/right`. Investigation found that configuration loading and cascade
merging read `ModeLine`, but production rendering never consumed it. Both interfaces
therefore displayed `tui.Footer`'s fixed layout, and changing the configured segments
had no visible effect.

## Product decision

Implement the configured left/right segments in the TUI footer. WEB follows the same
footer cells through `webui.VirtualTerminal`; it does not need a second modeline
implementation.

## Resolution

- `internal/app/tui.go` now passes `cfg.TUI.ModeLine.Left/Right` to every production
  footer at construction.
- `tui.Footer` owns a copied modeline configuration independently of `FooterData`, so
  token, context, activity, and other agent-event updates cannot reset it.
- The first footer row renders configured segments in their declared left/right order:
  - `app-badge` → `⬡ goa`
  - `mode-badge` → profile, goal/todo markers, and autonomy mode
  - `current-file` → the existing working-directory/git display
  - `token-bar` → the existing tiered token/cache/cost/tool/context statistics
  - `activity` → the current activity label
  - `help-hint` → `/help`
- Unknown and empty entries render nothing. Explicit empty lists produce empty sides;
  two nil lists preserve the legacy fixed footer for programmatic callers without
  configuration. YAML omission continues to inherit the lower cascade layer.
- The second row retains steering, model, team, workflow, and plugin state. Token stats
  move to the configured modeline. When `activity` is configured, main, companion, and
  delegated model formatters no longer duplicate it on the model row; when it is not
  configured, the model row remains the activity fallback.
- Orchestration output continues to replace the normal footer chrome. The additional
  agent-stats line remains intact.
- Narrow-width rendering reuses the existing footer segment ladder. `/help` has the new
  lowest tier and drops first; essential context/model information is retained as far
  as possible, with final truncation guaranteeing that rows never exceed the terminal.
- The shipped YAML and `docs/CONFIGURATION.md` now enumerate the supported names and
  describe empty, inheritance, width, and WEB behavior.

## Regression coverage

- The original production-boundary test was RED before implementation:
  `TestNewFooter_AppliesConfiguredModeLine` could not find `⬡ goa` or `/help` despite
  configuring them. It passed after wiring `newFooter` to `SetModeLine`.
- Footer tests cover every segment, left/right ordering, unknown/empty entries,
  explicit-empty versus nil compatibility, widths 8–240, help-first degradation,
  dynamic goal/team/plugin/steering state, and orchestration behavior.
- Main, companion, and delegated activity-deduplication tests were RED when the
  filmstrip exposed duplicate activity, then passed after the model formatter fix. A
  separate test protects the no-`activity`-segment fallback.
- `TestModeLineFilmstrip_DefaultSegmentsFollowAgentState` drives real app token,
  context, and thinking events and captures `AgentFrame`/`Filmstrip` output at 120 and
  40 columns. The final wide footer was:

  ```text
  ⬡ goa coder │ SOLO project … ↑400 ↓20 2.0%/16.0K thinking /help
                                                             ⬡ test-model • medium
  ```

  At 40 columns, low-priority app/activity/help content was gone while context and the
  model remained visible.
- `TestWebUI_ModeLineMatchesTerminalCells` compares terminal-emulator replay with the
  WEB virtual-terminal grid at `120x30`, `60x12`, and `40x10`, proving cell-for-cell
  parity rather than merely checking a shared configuration value.

## Validation evidence

All commands were run against the final implementation:

- `go test -count=1 -timeout 120s ./tui` — PASS
- `go test -count=1 -timeout 120s ./internal/app` — PASS
- `go vet ./...` — PASS
- `staticcheck ./...` — PASS
- `go test -count=1 -race -cover -timeout 300s ./...` — PASS across the repository
- `gocognit -over 15 .` — no new B26 violations. Existing unrelated entries remain:
  `TestResolveStartPlanAPIKey` (23), `GoalDriver.Drive` (21),
  `AgentManager.executeRunner` (16), and `initGoalSystem` (16).
- `gocyclo -over 12 .` — no new B26 violations. Existing unrelated entries remain:
  `TestDeviceMid_PersistsAndStable` (13) and `GoalDriver.Drive` (13).

No validation server, browser, or live LLM process was started, so there is no
validation process to clean up.
