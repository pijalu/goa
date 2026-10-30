# B26 — `tui.modeline` consumption and default-footer compatibility

Date: 2026-10-30  
Status: **Fixed — configured modelines work and the required legacy footer remains the default**

## Original report

The user expected WEB to follow the TUI status bar configured through
`tui.modeline.left/right`. Investigation found that configuration loading and cascade
merging read `ModeLine`, but production rendering never consumed it. Both interfaces
therefore displayed `tui.Footer`'s fixed layout, and changing the configured segments
had no visible effect.

## Product decision

Implement configured left/right segments in the TUI footer. WEB follows the same
footer cells through `webui.VirtualTerminal`; it does not need a second modeline
implementation.

The follow-up regression report added a compatibility requirement: enabling modeline
consumption must not change the established default footer. The default must retain
both original rows, their ordering, and all live statistics.

## Initial implementation

Commit `dea61981` wired `cfg.TUI.ModeLine.Left/Right` into every production footer and
added the configurable renderer:

- `tui.Footer` owns a copied modeline configuration independently of `FooterData`, so
  token, context, activity, and other agent-event updates cannot reset it.
- Configured first-row segments render in their declared left/right order:
  - `app-badge` → `⬡ goa`
  - `mode-badge` → profile, goal/todo markers, and autonomy mode
  - `current-file` → the existing working-directory/git display
  - `token-bar` → the existing tiered token/cache/cost/tool/context statistics
  - `activity` → the current activity label
  - `help-hint` → `/help`
- Unknown and empty entries render nothing. Explicit empty lists produce empty sides;
  two nil lists preserve the legacy fixed footer. YAML omission continues to inherit
  the lower cascade layer.
- With a configured modeline, the second row retains steering, model, team, workflow,
  and plugin state. When `activity` is configured, main, companion, and delegated model
  formatters suppress duplicate model-row activity; otherwise the model row remains the
  activity fallback.
- Orchestration output continues to replace normal footer chrome. The additional
  agent-stats line remains intact.
- Narrow-width rendering reuses the footer segment ladder. `/help` has the lowest tier
  and drops first; final truncation guarantees that rows never exceed the terminal.

## Default-footer regression

The initial implementation also activated the previously inert shipped segment lists.
That changed the default rather than only enabling customization.

Observed:

```text
⬡ goa coding-posture │ YOLO ~/dev/goa (⎇ feature/zai) /help
(kimi-code) k3.1-pro • xhigh • [8%|3%]
```

Required:

```text
~/dev/goa (⎇ feature/zai) coding-posture │ YOLO
↑636.7K ↓101.8K 10.1 tok/s CH:97.4%▸99.4% TC:360 8.4%/1.0M c:0m-1 (kimi-code) k3.1-pro • xhigh • [8%|3%]
```

The configured renderer put every configured segment on row 1 and built row 2 only
from steering/model/plugin state. Consequently, the default gained an application
badge and help hint, reordered workdir and mode state, and displaced the complete
second-row token, throughput, cache-hit, tool-count, context, and compaction fields.
Existing tests proved configuration consumption and TUI/WEB parity but had encoded the
new layout instead of protecting the required default.

The compatibility scope included existing generated home configs: the same six-segment
lists had shipped before the renderer existed and were still present in
`~/.goa/config.yaml`. Changing only embedded YAML would therefore leave those
installations on the regressed path.

## Final resolution

- The embedded defaults are now `left: null` and `right: null`. Two nil lists select the
  original footer renderer, preserving the standard layout and its established width
  degradation exactly:
  - Row 1: workdir/git on the left; goal/profile/todo/mode state on the right.
  - Row 2: complete live stats/activity/steering on the left;
    model/team/workflow/plugin state on the right.
- `configureFooterModeLine` is the shared production config-to-footer boundary. It
  passes custom selections to `Footer.SetModeLine`, but treats an unchanged copy of
  the exact pre-renderer six-segment defaults as no customization. Existing generated
  configs therefore retain the footer those configs originally produced. Changing or
  reordering the selection opts into the configured renderer.
- Custom segment ordering, nil-versus-explicit-empty behavior, activity deduplication,
  orchestration, dynamic footer state, and narrow-width handling are unchanged.
- The UI scenario harness uses the same production configuration boundary. WEB tests
  cover default, copied pre-renderer defaults, and genuinely customized modelines.
- `config/configs/default.yaml` and `docs/CONFIGURATION.md` document the null defaults,
  the standard two-row contract, a working custom example, and the copied-default
  compatibility rule.

## Regression coverage

- Original RED: `TestNewFooter_AppliesConfiguredModeLine` could not find `⬡ goa` or
  `/help` despite configuring them. It passed after production wiring was added and
  still passes after the compatibility fix.
- Default-layout RED: `TestNewFooter_DefaultModeLinePreservesLegacyLayout` loads the
  real embedded configuration and supplies the reported live values. Before the fix it
  produced the badge/help first row and model-only second row. It now compares both
  complete rows, with only adaptive padding normalized, against the required content
  and ordering.
- Copied-default coverage: `TestNewFooter_CopiedPreRendererDefaultsPreserveLegacyLayout`
  writes the old six-segment lists into an isolated home `config.yaml`, loads the real
  cascade, and applies the same exact two-row assertion. The pre-fix reproduction
  used an equivalent constructed config and was RED; the final cascade-level test
  passes after the fix. It covers the existing-user path that embedded-default-only
  coverage would miss.
- Footer tests retain coverage for every segment, left/right ordering, unknown/empty
  entries, explicit-empty versus nil behavior, widths 8–240, help-first degradation,
  dynamic goal/team/plugin/steering state, orchestration, and activity deduplication.
- `TestModeLineFilmstrip_DefaultFooterFollowsAgentState` drives real app token, context,
  and thinking events through the production component tree. The observed wide footer
  at 120 columns was:

  ```text
  project                                                                                                     coder │ SOLO
  ↑400 ↓20 2.0%/16.0K                                                                       ⬡ test-model thinking • medium
  ```

  The observed 40-column footer was:

  ```text
  project                     coder │ SOLO
  ↑400 ↓20 2.0%/16.0K        ⬡ test-model
  ```

  Neither frame contains `⬡ goa` or `/help`; live statistics remain on row 2 beside
  the model. The filmstrip also records each event-to-frame transition.
- `TestWebUI_ModeLineMatchesTerminalCells` compares terminal-emulator replay with WEB
  virtual-terminal cells at `120x30`, `60x12`, and `40x10` for all three configuration
  cases: null defaults, copied pre-renderer defaults, and custom segments. Wide-frame
  assertions additionally check content, default row membership, and absence of the
  default app/help badges.

## Validation evidence

All commands were run against the final implementation:

- Targeted B26 production, filmstrip, and WEB tests — PASS.
- `go test -count=1 -timeout 120s ./tui` — PASS.
- `go test -count=1 -timeout 120s ./internal/app` — PASS.
- `go test -count=1 -timeout 120s ./config` — PASS.
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `go test -count=1 -race -cover -timeout 300s ./...` — PASS across the repository.
- `gocognit -over 15 .` — no new B26 violations. Existing unrelated entries remain:
  `TestResolveStartPlanAPIKey` (23), `GoalDriver.Drive` (21),
  `AgentManager.executeRunner` (16), and `initGoalSystem` (16).
- `gocyclo -over 12 .` — no new B26 violations. Existing unrelated entries remain:
  `TestDeviceMid_PersistsAndStable` (13) and `GoalDriver.Drive` (13).

Validation used structured terminal `AgentFrame`/`Filmstrip` output, terminal-emulator
replay, and WEB virtual-terminal grids. No validation server, browser, PTY application,
or live LLM process was started, so no validation process required cleanup.
