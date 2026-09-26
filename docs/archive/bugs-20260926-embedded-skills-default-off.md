<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug fix report: embedded skills were active by default (telegram sticky-injected into every session; dream reported ON)

Date: 2026-09-26 · Status: CLOSED

## Symptom (bugs.md `# To fix`)

The embedded default-off policy kept two exceptions, so a fresh install had built-ins
active without asking:

- `skills/loader_loading.go` had `DefaultOnEmbeddedSkill = "telegram"`: the telegram skill
  (inline, knowledge, **sticky: true**, `skills/telegram/SKILL.md`) stayed ON and its body
  was persisted into EVERY agent's history by the sticky-skill provider, besides being
  advertised in `<available_skills>`;
- hidden/internal skills (dream, `skills/dream/SKILL.md`) were deliberately excluded from the
  default-off set and therefore loaded and reported ON in `/config → Skills → Embedded`.

## Resolution

1. **Every embedded skill is OFF by default.** `DefaultEmbeddedOffNames` is now simply
   `EmbeddedSkillNames(efs)` — no telegram exception, no hidden-skill exemption — and the
   `DefaultOnEmbeddedSkill` constant is gone. The set stays derived from the embedded FS, so a
   newly added built-in is inactive by construction.
2. **One predicate for "is this skill on".** `skillEnabledIn(cfg, name, source, reg)` is the
   single source of truth: disabled wins, a global allowlist governs when present, embedded
   skills are on only while listed in the embedded-scoped `skills.embedded_enabled` opt-in,
   file skills keep the legacy all-on-unless-disabled rule. `skillEnabled` delegates to it
   (resolving the source from a concrete registry), and the menu, the `/skill:` commands and
   the toggles all go through it, so what is displayed always matches what a toggle does.
3. **Toggle routing.** `setSkillEnabled` keeps the embedded opt-in list in step (enable adds,
   disable drops) and additionally clears an allowlist membership when one governs the skill —
   with the existing never-collapse guard (removing the last allowlist member would turn the
   list into "empty = all on", so the name stays listed and an explicit `disabled` entry keeps
   it off). Configs written under the old policy (`skills.disabled: [telegram]`) keep working:
   the entry is honoured and re-enabling clears it.
4. **Dream is off by default with actionable entry points.** Both `/dream` and the CLI
   (`--dream` / `--dream-apply`) report `DreamSkillDisabledMessage`: it names
   `skills.embedded_enabled: [dream]`, `/config → Skills → Embedded → dream` and the restart
   caveat, replacing the bare "dream skill not found". `internal/app/dream.go` was split into
   `runDream` (CLI shell) + `executeDream` (returns errors) so the message is testable without
   `os.Exit` killing the test binary.
5. **Reload parity.** `ReloadHandler.ReloadSkills` now refreshes `Skills.EmbeddedEnabled` from
   the reloaded on-disk config like the other skill lists, so an in-session "skill on"
   produces exactly the registry a fresh start would build.
6. **Toggles tell the truth.** `skillToggleApplied` checks the requested state against the live
   registry; when the reload could not apply it (no reload hook, or the skill is still there),
   the user is told "<skill> will be <state> after a restart (in-session reload unavailable)"
   instead of a success flash.
7. Comments/docs updated (`skills/loader.go`, `skills/loader_loading.go`,
   `internal/app/subsystems_skills.go`, `core/commands/config_skills.go`,
   `config/config_features.go`) and the embedded default config no longer advertises
   `telegram.enabled: true`.

## Test approach & validation

New/updated tests (RED demonstrated by restoring the pre-fix default-off set, which made the
skills tests report "7 entries, want all 9", `map[dream:true telegram:true]` loaded and a
sticky body injected):

- `skills/embedded_default_test.go`: `TestDefaultEmbeddedOffNames_CoversEveryEmbeddedSkill`,
  `TestShippedEmbeddedSkills_AllOffByDefault` (nothing loads; `StickyBodies()` empty;
  `Get` misses for review/telegram/dream), `TestEmbeddedSkill_OptInTelegram` (opt-in loads it
  **with** its sticky body), `TestEmbeddedSkill_OptInDream`,
  `TestHiddenEmbeddedSkill_ResolvableWhenOptedIn`, `TestLegacyTelegramDisableStillHonored`,
  `TestReenableAfterLegacyDisable`, updated `TestListEmbeddedDiscoverable`.
- `core/commands`: `TestConfigMenu_SkillsShowEmbeddedOffByDefault` (embedded source reports
  `0/N on`, every entry "off", telegram+dream listed), `TestSkillToggle_ReportsRestartWhenNotApplied`,
  `TestSkillToggle_AppliedInSessionClaimsSuccess`, updated
  `TestSetSkillEnabled_EmbeddedRouting` (opt-in semantics + legacy disable clearing),
  `TestConfigMenu_SkillToggleEmbeddedPersistsToHome` / `_SkillToggleSurvivesReload` /
  `_SkillAllowListSurvivesDisableReenable` / `TestSkillEnableDisableCommand[_RealRegistry]`
  (enable→opt-in→disable round trip per source), `TestDreamCommand_SkillDisabledReportsHowToEnable`,
  `TestDreamCommand_RunsWhenSkillOptedIn`. Test doubles added: `newRegistryKeepingEverySkill`,
  `newToggleableSkillRegistry`, `stubReloadHandler` (+`removeAllSkills`).
- `internal/app`: `TestDreamCLI_SkillDisabledReportsHowToEnable`,
  `TestReloadSkills_PicksUpEmbeddedEnabled`; the dream integration tests now opt the skill in
  (`skills.embedded_enabled: [dream]`) and call `executeDream` instead of the exiting shell.

Gate (each command run separately, post-change): `go vet ./...` clean; `staticcheck ./...`
clean (the now-unused `embeddedToggleInfo` was deleted); `gocognit -over 15 .` clean;
`gocyclo -over 12 .` clean; `go test -count=1 -race -cover -timeout 900s ./...` → 87 packages
ok, 0 FAIL.

## Closure

Nothing compiled into the binary is active by default: telegram no longer injects its style
into every session, dream stays dormant, and both can be enabled explicitly (in-session via
`/config` / `/skill:enable`, persisted through `skills.embedded_enabled`) with the user told
when a change needs a restart. Closed 2026-09-26.
