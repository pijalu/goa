<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B50 — thoughtfull ships default-on; new installs should keep only telegram

Closed 2026-11-08. Archived from `bugs.md`.

## Reported

The embedded skills allowlist default (`skills.enabled` in
`config/configs/default.yaml`) shipped `[telegram, thoughtfull]` — both
sticky knowledge skills applied on a fresh install with no config edit.
thoughtfull (thinking discipline) is opinionated workflow guidance that a
default install should not impose.

## Expected behavior

The shipped default list keeps only `telegram`: thoughtfull becomes opt-in
via `/config` → Skills → Embedded or `skills.embedded_enabled` (or an
explicit `skills.enabled` pin). Existing home configs that pin
`skills.enabled` are untouched; the default-provided gate semantics (default
list applies embedded-scoped, explicit pins replace it) stay exactly as
they are.

## Fix and validation

- config: `config/configs/default.yaml` `skills.enabled` flipped to
  `[telegram]`, comment updated (telegram stays ON out of the box;
  thoughtfull opt-in per bugs.md B50).
- config: `config/config_skills_default_test.go` RED-then-green — embedded
  default, defaults-only cascade, and disable-pin-survival assertions now
  expect exactly `[telegram]`; RED observed against the old shipped default
  (3 failing tests: embedded default `[telegram thoughtfull]`,
  shipped cascade, disabled-pin survival), then green after the yaml flip.
- core/commands: shipped-default mirror (`shippedDefaultSkillsCfg` →
  `[telegram]`), toggle matrix now asserts thoughtfull stays opt-in, Skills
  sub-menu shows `1/<N> on` with only telegram on and thoughtfull off
  (core/commands/config_skills_default_test.go).
- internal/app: end-to-end through InitSubsystems — telegram on with one
  sticky body injected + project file skill unaffected + thoughtfull and
  every other embedded skill off; home pin replaces the default at the
  wiring level (telegram and thoughtfull both off)
  (internal/app/skills_default_integration_test.go,
  internal/app/dream_integration_test.go).
- Comments: `config/config_skills_gate.go` and
  `internal/app/subsystems_skills.go` updated to the single shipped-on
  skill (telegram); thoughtfull called out as opt-in.
- Docs: docs/CONFIGURATION.md already showed the telegram-only shipped
  allowlist (no change needed); docs/SKILLS.md gained the missing
  `thoughtfull` row in Built-in Skills (marked opt-in / off by default)
  and the embedded location list. No doc claimed both skills ship on by
  default.
- Gates run separately, all clean: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...` (0 FAIL, all packages ok).

## Resolution

Implemented as planned: the shipped default skills.enabled list contains
only telegram; thoughtfull is opt-in via `/config` → Skills → Embedded or
`skills.embedded_enabled`; existing explicit pins still replace the
default; pre-existing home configs are untouched.
