# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

## B-CfgStaleModel — dangling model references in teams config make startup fatal

**Observed:** goa refuses to start and exits with:

```
Config error: validation errors (2):
teams.definitions.Loco.members.main.model: model "qwen3-8-27b" not found in models list
teams.definitions.Loco.members.companion.model: model "qwen3-8-27b" not found in models list
```

after the referenced model was deleted from the models list. The only
recovery is hand-editing the YAML.

**Root cause:** two halves —
1. Startup is all-or-nothing: `LoadConfig` (`internal/app/bootstrap.go:412`)
   treats every `internal.ValidationError` as fatal (`os.Exit(1)`), so a
   stale reference in an unused team definition bricks the whole app
   (`config/teams.go:495`, same pattern for orchestrator roles at
   `config/config_validate.go:520`).
2. Model deletion does not clean up references: both delete paths
   (`core/commands/model.go` `removeModelFromConfig`,
   `core/commands/config_models.go` `doRemoveModel`) drop the model and the
   per-model compression override, but leave
   `teams.definitions.*.members.*.model` (and `orchestrator.roles.*.model`,
   `orchestrator.pool.max_agents_per_model` is cleaned in one place only)
   dangling.

**Expected:**
- Deleting a model removes or resets every config reference to it (team
  member models, orchestrator role models, per-model pool/compression
  entries), with the cleanup covered by the same heal-on-load pattern used
  for `compression.per_model` (`sanitizeDanglingCompressionModels`).
- A validation error caused solely by dangling model references does not
  stop goa: the references are healed (cleared/reset) with a logged warning
  and the app starts; only genuinely invalid config stays fatal.

### Fix plan
1. Catalog every config field that references a model ID: team member
   `model` (`config/teams.go`), orchestrator role `model` and pool entries
   (`config/config_validate.go`), compression `per_model` (already healed by
   `sanitizeDanglingCompressionModels`), `active_model`, mode/profile model
   overrides if any.
2. Heal-on-load (startup resilience): extend the existing sanitize pass in
   the cascade loader to clear dangling model references in team member
   definitions and orchestrator roles/pool entries, collecting a warning per
   healed field (surfaced to the user, e.g. stderr/flash) instead of
   failing validation. Validation keeps failing for any remaining genuine
   errors.
3. Delete-time cleanup: factor a `removeModelReferences(cfg, id)` helper
   that clears team member models, orchestrator role models, per-model pool
   entries, compression per-model entries, and resets `ActiveModel`; call it
   from both delete paths (`removeModelFromConfig`, `doRemoveModel`).
4. Tests (RED first): config-level tests that loading a config with a team
   member / orchestrator role referencing a deleted model heals + warns and
   does not error; command-level tests that both delete paths leave zero
   dangling references; regression test that a config with only dangling
   model references starts (LoadConfig does not exit) while a config with a
   genuine error (bad provider, unknown mode) still fails.
5. Validate actual terminal output: delete a model via /config and /model
   in a running TUI, restart, confirm goa boots and shows the heal warning
   (interactive shell / filmstrip per guideline 5).
6. Quality gates (each run separately): `go vet ./...`, `staticcheck ./...`,
   `gocognit -over 15 .`, `gocyclo -over 12 .`,
   `go test -count=1 -race -cover ./...`.
7. Commit with a descriptive message, then move this section + plan to
   `docs/archive/` per guideline 4.
