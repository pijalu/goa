# Bugs archived 2026-08-04

All six OPEN items from `bugs.md` were fixed (or, for the z.ai entry, verified)
in one session. Each entry keeps its original report and gains a
**Resolution** section naming the change and its tests. Quality gates
(guideline 6) were run separately for every fix and again repo-wide at the end:
`go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
`go test -count=1 -race -cover ./...` — all green, no new violations.

---

## Context-compression cascade: per-layer `strategies` never merged, `micro_compaction` replaced wholesale, `enabled: false` ignored, home `Save` full-dumps merged config — FIXED

- **Original report**: four latent config-merge/persist defects:
  1. `mergeContextCompression` (`config/config_merge.go`) never merged the
     `Strategies` block: `context_compression.strategies.{soft,trigger,hard}`
     in a home/project/local file was silently dropped on cascade load.
  2. `MicroCompaction` merged wholesale: one micro key in a higher layer reset
     every other micro key to zero/SDK-default.
  3. `if !cc.Enabled { return }` made `enabled: false` in a home/project file
     a NO-OP — compression could not be disabled from any config file.
  4. `CascadeLoader.Save` full-dumped the merged in-memory config to
     `~/.goa/config.yaml` from the model-manager and orchestrator flows,
     baking project/embedded values into the home file.
- **Resolution**:
  - `ContextCompressionConfig.Enabled` changed from `bool` to `*bool`
    (tri-state, matching the `Goals.AutoUnblock`/`FreshContext` pattern) with
    an `EnabledValue()` accessor (nil = default true). All readers updated
    (`config_validate.go`, `config/loader.go`, `core/agentmanager.go`,
    `core/agentmanager_lifecycle.go`, `multiagent/agent_pool.go`,
    `core/commands/config_compression.go`, `config_cli.go` — the CLI setter
    now uses `setBoolPtr`).
  - `mergeContextCompression` now: applies `Enabled` tri-state (explicit
    false disables); merges `Strategies` field-wise (non-empty wins per
    layer) via `mergeCompressionStrategies`; merges `MicroCompaction`
    field-wise (non-zero scalars per field) via `mergeMicroCompaction`;
    keeps the zero-value-layer guard via `contextCompressionLayerEmpty`.
  - Replaced the full-dump `m.saveConfig()` callers: model-manager
    (`config_models.go` add/edit/remove) now persists via
    `saveProvidersAndModels` → `SaveHomeProvidersAndModels`; orchestrator
    flows (`config_orchestrator.go`) via `saveOrchestratorSection` /
    `saveGoalsSection` → `SaveHomeFieldValue`. `promptRetentionDays` now
    takes the section save/reopen callbacks so it serves both sections.
- **Tests** (RED first): `config/compression_cascade_test.go` (5 tests:
  strategies survive home+project cascade; micro merges field-wise;
  `enabled: false` disables; home-file integration via `NewCascadeLoader`;
  zero-value layer does not wipe the block). `core/commands/config_menu_test.go`:
  `TestConfigMenu_OrchestratorSaveIsSectionScoped` and
  `TestConfigMenu_ModelSaveIsFieldScoped` prove no merged-state leakage into
  the home file. Existing compression tests migrated to the tri-state field.
- **Interactive validation (guideline 5)**: a home config with
  `enabled: false`, `strategies.trigger: summarize`,
  `micro_compaction.min_context_ratio: 0.45` loads as `EnabledValue=false`,
  `Trigger="summarize"`, `MinContextRatio=0.45` with the other embedded micro
  defaults (`KeepRecentMessages=20`, `CacheMissThreshold="1h"`) intact.

---

## No CLI way to relocate `~/.goa` / No `--home` CLI param — FIXED

(two OPEN entries for the same gap; resolved together)

- **Original report**: no `--home` flag nor `GOA_HOME` env override;
  `NewCascadeLoader` hardcoded `os.UserHomeDir()` and 14 sites called it
  directly, so first-run isolation testing was impossible without juggling
  `HOME=`.
- **Resolution**: new single resolution point `internal.GoaHome()` /
  `internal.GoaHomeDir()` (`internal/home.go`) with the documented order
  `--home` flag (`internal.SetGoaHome`, applied in `runApp` right after
  `ParseCLIFlags`, before the cascade loader) → `GOA_HOME` env →
  `os.UserHomeDir()`. All 14 direct `os.UserHomeDir()` sites now go through
  it: `config/loader.go`, `config/config.go`, `config/defaults.go`,
  `internal/app/app.go`, `internal/app/crash_log.go`, `internal/usage/usage.go`,
  `internal/agentic/provider/schema/loader.go`, `internal/spinner/spinner.go`,
  `internal/logs/export/bundle.go`, `internal/sandbox/manager.go`,
  `tools/renderer_common.go`, `tools/memento.go`,
  `tools/common/search_priority.go`, `tools/readfile.go`. The helper lives in
  `internal` (not `config`) because `config` imports `tools` — placing it in
  `config` would have cycled. `-home` is registered in
  `internal/app/bootstrap.go` and shows in `--help`.
- **Tests** (RED first): `config/home_test.go` — resolution order
  (flag > env > OS), cascade home layer relocated, first-run detection
  follows the override (fresh root = first run, root with config.yaml = not),
  `GoaHomeDir`, `DefaultSkillDirs` follow the override.
- **Interactive validation (guideline 5)**: `goa --home /tmp/clean` on a
  clean root triggers "First run detected — launching setup wizard" while the
  real `~/.goa/config.yaml` stays byte-identical (md5 checked); `GOA_HOME=…`
  behaves the same; `-home` appears in `--help`.

---

## /config Skills row shows the mode value ("Skills inline") but opens a submenu — FIXED

- **Original report**: the top-level `/config` row `Skills` showed the
  execution-mode value (`inline`/`subagent`), reading like a binary toggle,
  but opens a submenu.
- **Resolution**: `skillsLabel` (`core/commands/config.go`) now takes the
  skill registry and returns a neutral submenu hint — `embedded <on>/<total>
  on · local <on>/<total> on` (or `settings` when no registry is available) —
  mirroring the per-source counts already shown inside the submenu.
- **Tests**: `TestConfigMenu_SkillsRowIsSubmenuHint` (RED first) asserts the
  row no longer shows the raw mode value and mentions both sources.

---

## Skill enable/disable state is lost / unstable across sessions — FIXED

- **Original report**: `/skill:enable` / `/skill:disable` and the `/config`
  Skills toggles did not reliably stick; skills reverted to a different
  enabled/disabled set after restart.
- **Reproduction**: with `skills.enabled: [review]` (allow-list of one),
  disabling review then re-enabling it deleted the `enabled` key — the merged
  config came back as `enabled=[] disabled=[]`, i.e. *all skills on*: the
  allow-list was destroyed (RED: `review flipped on after disable/re-enable
  round trip; allow-list was lost`).
- **Root cause**: `setSkillEnabled` removed the last allow-list member on
  disable; an empty `Enabled` list loads as "no allow-list = all on", so the
  persist layer deleted the key and the mode could never be restored.
- **Resolution** (`core/commands/config_skills.go`):
  - `setSkillEnabled` on disable now keeps the name in `Enabled` when it is
    the last allow-list member — a name in both lists is disabled (explicit
    off wins everywhere: `skillEnabled`, registry `allowed`), so the off
    state is preserved AND the allow-list survives for a later re-enable.
  - It gained an `allowListActive` parameter; callers pass
    `skillAllowListActive(...)` which checks the live list and, on the
    re-enable path with an empty live list, the persisted layer on disk
    (`skillEnabledKeyOnDisk` via `skillLayerConfigPath`, honoring the
    `internal.GoaHome()` override).
- **Tests** (RED first): `TestConfigMenu_SkillToggleSurvivesReload` (toggle →
  persist → fresh cascade load → same state, for embedded/home and
  local/project layers), `TestConfigMenu_SkillAllowListSurvivesDisableReenable`
  (the allow-list regression), `TestSetSkillEnabled` updated for the new
  semantics. Hypothesis 1 of the original report (merge replaces) was
  disproven along the way: `mergeSkills` unions.
- **Interactive validation (guideline 5)**: a cascade+registry probe over a
  `GOA_HOME`-relocated home showed session 1 `enabled=[review]` → review
  loaded; after-disable state (`enabled=[review] disabled=[review]`) → review
  NOT loaded with the allow-list intact; after re-enable → review loaded
  again.

---

## Input line not resized to content when items are deleted — stays multiline even when empty — FIXED

- **Original report**: the multiline editor kept its peak height
  (`stableMaxLines`) after deleting content back to empty; blank lines
  remained.
- **Resolution**: new `Editor.shrinkPending` flag set by every user deletion
  op (`backspace`, `deleteForward`, `killWordBack`, `killWordForward`,
  `killToStart`, `killToEnd`). In `computeLayout` (`tui/editor_render.go`),
  when the flag is set and the recomputed visual-line count is below
  `stableMaxLines`, the reserved height decays to the content
  (`stableMaxLines = maxLines`). Programmatic `SetText` (history recall) does
  not set the flag, so browsing history keeps the anti-jump stability; growth
  and terminal-resize behavior are unchanged; submit/clear already reset.
- **Tests** (RED first): `tui/editor_shrink_test.go` — backspace /
  delete-forward / kill-to-end shrink to single-line, partial delete shrinks
  proportionally, `SetText` keeps stability (pins the history behavior),
  shrink-then-regrow. `tui/editor_shrink_tui_test.go` — end-to-end through
  the TUI engine: the `Editor` node's rect grows past 3 rows on wrapped input
  and returns to 3 rows after backspacing it away (the footer-gap symptom).
  Pre-existing `TestEditor_RenderHeightIsStableWhileTyping` still passes.
- **Complexity note**: `computeLayout` stayed well under the gocognit/gocyclo
  budgets.

---

## z.ai GLM-5.2 quota burned rapidly — verify provider-prompt caching — VERIFIED

- **Original report**: ~50% of GLM-5.2 quota consumed in minutes; question
  whether provider prompt caching is defeated. Plan: verify the prefix stays
  stable and the provider reports cache usage; capture diagnostics.
- **Verification results** (`internal/agentic/provider/protocol/zai_cache_test.go`):
  1. **Prefix stability** — `TestZaiPrefixStableAcrossTurns`: a turn-2 request
     body contains turn-1's messages array verbatim as its prefix and an
     identical tools array (server-side automatic prefix caching's
     requirement); z.ai correctly sends no OpenAI-specific `prompt_cache_key`
     nor `prompt_cache_retention` on the default short retention.
  2. **Determinism** — `TestZaiSameBodyIsDeterministic`: identical
     (model, context, options) marshal byte-identically.
  3. **Cache-usage parsing** — `TestZaiUsageParsesCachedTokens`: z.ai's
     OpenAI-style `usage.prompt_tokens_details.cached_tokens` parses to
     `Usage.CacheReadTokens` (4800 in the fixture) through the streaming
     protocol path.
- **Diagnostics already in place** (confirmed, not added): the
  cache-forensics journal (`internal/agentic/provider/cache_forensics.go`)
  records complete requests per conversation sequence, detects misses
  (zero read after establishment, or collapse beyond the 1024-token
  block-quantization tolerance), surfaces notices into the agent log via
  `drainCacheMissNotices`, and exports reports as
  `logs/cache_miss_requests.json` in the debug bundle — exactly the
  instrumentation needed to catch a live prefix-bust (memory-block churn,
  tool elision, premature compression) against z.ai.
- **Verdict**: the client-side caching contract holds — serialization is
  prefix-stable and cache usage is read correctly. Any real-world quota burn
  comes from runtime prefix churn (which the journal now flags per miss) or
  retry storms on 429 re-billing a full context, not from the request/usage
  plumbing. No code change was needed beyond the tests; closed as verified.
