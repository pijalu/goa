# bugs-20260823-config-tool-settings — /config tool fixes are not saved / do not survive next load

**Reported:** 2026-08-23 · **Fixed:** 2026-08-23 · **Commit:** fix(config): merge execution.auto_heal_tool_calls across config cascade layers

## Symptom

Toggling *tool_call_fixing* in `/config` → Tools reported
"Set execution.auto_heal_tool_calls = true", but after restarting goa the
setting was off again. The value had been written to `~/.goa/config.yaml`
(`execution: auto_heal_tool_calls: true`) yet the loaded config still said
false. Tool enable/disable toggles (`tools.enabled.*`) persisted fine.

## Root cause

The config cascade merges each section through a hand-written merge step.
`mergeExecution` copied every `ExecutionConfig` field from the upper layer
— except `AutoHealToolCalls`. The field was therefore dropped at every
merge boundary: embedded default (false) → home (true) produced false,
and no later layer carried it either. The write path was correct; only the
read/merge side lost the key. Neighbours `AutoSaveModel` /
`DisableToolBudget` were already copied; `AutoHealToolCalls` was simply
missing from the list.

## Fix plan (as planned before executing)

1. Add `dst.AutoHealToolCalls = src.AutoHealToolCalls` to
   `mergeExecution`, matching its two sibling booleans.
2. Config-layer regression test: home layer pins
   `execution.auto_heal_tool_calls: true` over embedded defaults → merged
   config must report true (and stay true with a project layer that omits
   the key).
3. Menu-level regression test reproducing the user flow: drive
   `configMenu.showRoot → onSel("tools") → onSel("enabled_tools")…`
   and the `tool_call_fixing` toggle against a real `CascadeLoader` in
   temp dirs; reload via a fresh `Load()` and assert both the
   `tools.enabled.todo_list` toggle and `auto_heal_tool_calls` survive.

Test approach: table-driven config-layer cases + one end-to-end menu flow
test using the existing menu test harness (real loader, temp HOME/project).

Validation steps: run new tests, full package suites for `config`,
`core/commands`; quality gates separately (vet, staticcheck, gocognit,
gocyclo, race tests); commit.

## Fix

- `config/config_merge.go`: `mergeExecution` now copies
  `AutoHealToolCalls`.
- `config/config_execution_merge_test.go`: new regression tests
  (`HomePinAutoHealOverridesDefault`, project layer omitting the key keeps
  the home pin, explicit project override wins).
- `core/commands/config_tools_persist_test.go`: end-to-end toggle tests —
  enabled-tools toggle and tool_call_fixing toggle both survive
  `Load()` on a fresh cascade.
- `config/config_test.go`: made `TestDefaultConfig_AutoHealToolCalls`
  hermetic (`t.Setenv("HOME", …)`) — it previously leaked the real user
  HOME into a "shipped defaults" assertion and only passed by accident
  while the merge bug swallowed any home pin.

## Validation

- `go test ./config ./core/commands -count=1` — pass (incl. the four new
  tests; the pre-fix repro failed exactly as reported).
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
  `gocyclo -over 12 .`, `go test -race ./config ./core/commands` — all
  green; staticcheck reports one pre-existing SA1019 warning in an
  unrelated file (noted per guideline), gocognit/gocyclo findings match
  the pre-existing baseline in unmodified files.
