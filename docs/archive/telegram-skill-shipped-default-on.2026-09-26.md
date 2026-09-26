# Embedded telegram skill must ship enabled by default

Archived: 2026-09-26 (fixed, tested, committed)

## Observed
Embedded skills ship OFF by default (config commit "skills: every embedded
skill is OFF by default (telegram + dream included)"), so the telegram skill
had to be enabled by hand on every install (`skills.embedded_enabled:
[telegram]` or /config → Skills → Embedded) before its telegraphic style
applied.

## Expected
The embedded `telegram` skill is ENABLED by default in the shipped defaults
(config/skills defaults), so its behavior applies out of the box. Every other
embedded skill stays OFF. The toggle stays live and user-changeable
(enable/disable persists as today); a home config that already pins
`skills.enabled`/`skills.disabled` must not be overridden by the defaults.

## Fix plan
1. Ship the default in the embedded config: `config/configs/default.yaml`
   gains `skills.enabled: [telegram]` (documented in-file).
2. Keep the shipped default from behaving like a user allowlist:
   - `SkillsConfig.EnabledFromDefaults` (yaml:"-") marks a merged
     `skills.enabled` list that came solely from the embedded layer
     (set in `loadDefaults`, cleared by `mergeSkills` on the first explicit
     pin).
   - `SkillsConfig.SkillGateLists()` partitions the gate: default-provided
     lists are applied embedded-scoped (merged into the embedded opt-ins,
     never a global allowlist — file skills stay loadable); explicit pins are
     returned as the allowlist verbatim.
   - `mergeSkills`: an explicit pin replaces a default-owned list (a home pin
     is never overridden); user layers among themselves keep the concatenating
     cascade (toggle persistence partitions the allowlist across home/project
     by skill source).
3. Wiring (`internal/app`: newSkillRegistry, reloadSkills, ReloadSkills) uses
   `SkillGateLists()`; ReloadSkills also refreshes the provenance flag from
   the fresh load.
4. Toggle semantics (`core/commands/config_skills.go`):
   - `skillEnabledIn`: under a default-provided list, embedded skills are on
     while listed (or opted in), file skills unaffected; explicit off still
     wins; an explicit pin keeps the legacy allowlist semantics.
   - `skillAllowListActive`: the default-provided list is not a pin.
   - `persistSkillToggle`: never persists the default-owned list (a persisted
     pin would flip later loads into global-allowlist mode). Disabling
     telegram writes `skills.disabled: [telegram]` to the home layer;
     re-enabling drops it (embedded opt-in routing unchanged for all other
     embedded skills).

## Test approach and validation
- config: shipped-default YAML pins (exactly `[telegram]`, no disabled/
  embedded_enabled), cascade provenance flag, home-pin precedence (enabled
  replaces the default; disabled keeps telegram off), merge transition
  (default-owned → replaced by pin → concatenated with later layers),
  `SkillGateLists` partitioning (config/config_skills_default_test.go).
- core/commands: `skillEnabledIn` matrix (default-only vs explicit pin,
  embedded vs file, opt-in, off-wins), `skillAllowListActive` not-a-pin,
  menu-level persistence regression (disable writes only `skills.disabled`,
  re-enable writes only the embedded opt-in — never an `enabled` pin),
  menu shows 1/N on with only telegram "on" under a defaults-only load
  (core/commands/config_skills_default_test.go).
- skills: shipped wiring mirror — default-off set + embedded-scoped telegram
  opt-in loads telegram with its sticky body alongside file skills, other
  embedded skills stay off (skills/embedded_default_test.go).
- internal/app: end-to-end through InitSubsystems — telegram on + project
  file skill unaffected + all other embedded skills off; home pin replaces
  the default at the wiring level; ReloadSkills refreshes both the lists and
  the provenance flag (internal/app/skills_default_integration_test.go,
  internal/app/dream_integration_test.go).
- Gates run separately, all clean: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...` (0 FAIL, 87 packages ok).

## Resolution
Implemented as planned. The telegraphic style now applies with no config
edit; a fresh /config → Skills view shows telegram on and every other
embedded skill off; pre-existing home pins win over the shipped defaults.
