# Bugs closed 2026-07-30

Archived from bugs.md per guideline 8. All fixes implemented test-first
(RED → GREEN), validated against actual terminal output (guideline 5), and
the full quality gate ran separately per tool (guideline 6): `go vet` clean;
`staticcheck`/`gocognit`/`gocyclo` report only pre-existing hits in unrelated
files; `go test -count=1 -race -cover ./...` 79/79 packages ok, zero FAIL.

## Bug A — webfetch rejects goa:// embedded doc URLs — CLOSED

- **Reported**: "create an orchestration goa setup" failed: the model fetched `goa://ORCHESTRATION-DESIGN` / `goa://ORCHESTRATOR` via webfetch → `✗ webfetch goa://ORCHESTRATOR (fetch)` (`scheme_not_allowed`).
- **Root cause**: `tools/webfetch.go` `validateURL` only allowed http/https; unlike `read`, webfetch never intercepted `goa://`. The system prompt steered the model to `goa://` URLs without naming a tool.
- **Fix**:
  1. `tools/webfetch.go`: `ExecuteContext` intercepts `goa://` via `docs.ParseGoaURL` before `validateURL`; new `goaDoc` serves `docs.Get` content (no fetcher/cache/HTML conversion); `renderGoaDocEntry` honors start/end/max_lines with an `embedded` footer marker; deliberate errors for `summarize` (unsupported) and unknown actions; schema description/examples + `webfetch.short.md`/`long.md` document it; `buildSelfDocSection` names read/webfetch.
  2. `docs/ORCHESTRATOR.md`: new Configuration section — which file to write (`.goa/config.yaml` project, `~/.goa/config.yaml` global), how role `model`/`provider` IDs resolve, full 3-role example (orchestrator/reviewer/coder), role-name semantics (`orchestrator` reserved for hub).
- **Tests**: `TestWebFetchGoaDoc` (fetcher t.Fatalf if network touched, Cache nil), `...Variants` (4 URL forms + case-insensitive), `...LineRange`, `...NotFound`, `...Actions`. `./tools` suite + race GREEN, 80.5% coverage.
- **Terminal validation**: harness print showed `webfetch goa://ORCHESTRATOR:1:8` + doc body + `(end — 8 lines shown, 140 remaining; embedded)`, plus deliberate `doc_not_found` and `summarize_unsupported` errors with hints.

## Bug B — companion model/provider selection split; status bar wrong provider — CLOSED

- **Reported**: companion selection asked for model AND provider separately (contradiction possible); status bar showed `(opencode-go) glm-5.2` — the ACTIVE provider — for a zai-bound companion.
- **Root cause (status bar)**: `companionModelDisplay` (internal/app/models.go) passed `cfg.ActiveProvider` to `modelDisplay` and resolved against the active provider config.
- **Root cause (config UI)**: `settingMultiAgent` exposed independent `companion_model`/`companion_provider` rows; the "— other model —" flow dropped the chosen provider from its callback.
- **Fix**:
  1. `internal/app/models.go`: `companionModelDisplay` uses `MultiAgent.CompanionProvider` (legacy fallback to active when empty) and resolves against the companion provider's `ProviderConfig`.
  2. `core/commands/config.go` + `config_models.go`: one "Companion model" row with `provider / model` summary; provider-carrying picker chain (`selectModelPageFull`/`promptOtherModelFull`/`resolveModelFull`/`promptCustomModelFull`); old model-only chain kept as adapters; `settingCompanionProvider` removed.
- **Tests**: `TestCompanionModelDisplay_*` (companion provider shown, legacy fallback, no-model empty); `TestConfigMenu_CompanionModelSetsProviderAndModel` (single row, provider shown in picker, both keys set); `TestConfigMenu_MultiAgentSubMenu` updated to the intended 2-row structure. Note: the duplicate-ID resolution discriminator was dropped after verifying model config IDs are unique config-wide (`GetModelByID`/`providerIDForModel` invariant) — the real defect was the provider prefix.
- **Terminal validation**: footer render with the reported data now shows `(opencode-go) deepseek-v4-flash • high | ⟳ (zai) glm-5.2 (companion) • medium`.

## Bug C — /companion completion proposes :on while companion is active — CLOSED

- **Reported**: companion already on (footer `coding-posture(companion)`), `/companion:` completion proposed `/companion:on` first.
- **Root cause**: `CompanionToggleCommand.CompleteArgs` derived mode ONLY from `ctx.ForegroundOrchestrator.Mode()`; companion state lives in `AgentManager` minor mode (`SetMinorMode("companion", …)`) and is merely mirrored to the orchestrator when non-nil.
- **Fix**: new `companionEffectiveMode` consults BOTH sources (orchestrator mode when non-inactive, else `AgentManager` minor mode ⇒ agent-driven); added exported `AgentManager.MinorMode()` delegating to `modeMgr.CurrentMinorMode()`.
- **Tests**: `TestCompanionToggleCommand_CompleteArgsWithoutOrchestrator` (nil orchestrator ⇒ {off, framework}), `...UnmirroredOrchestrator` (session-restore shape), existing `...CompleteArgs` unchanged and GREEN.
- **Terminal validation**: harness print — disabled → `on agent framework`; ON → `off framework`; ON with nil orchestrator → `off framework`.
