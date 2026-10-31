# B41 — Console-only project configuration cleanup

Closed: 2026-10-31

## Report

Goa uses both `~/.goa/config.yaml` and `<project>/.goa/config.yaml`, but project
configuration could retain duplicate or conflicting active selections, provider
entries, model entries, and shared sections. There was no command to identify
redundant project data or safely reconcile it with the inherited home layer.

The requested workflow had to run as an ordinary console wizard without starting
the TUI, preserve intentional overrides by default, confirm every removal, leave
configuration unchanged on cancellation, and never expose credentials.

## Resolution

`goa cleanup` now dispatches before normal flag parsing, subsystem construction,
or TUI startup. It compares only the home and project YAML files; embedded
defaults, local configuration, environment and CLI overrides, and provider/model
registries are not treated as removable project data.

The planner identifies:

- `active_provider` and `active_model` values present in both layers;
- same-ID provider and model entries, using the effective last entry when an ID
  is repeated;
- other shared, known top-level sections.

Each candidate is classified as redundant or conflicting. The console displays
only its type, identifier, and differing field names, never configuration values.
Every candidate has its own `(y/n) [n]` prompt. Empty input and negative answers
preserve the item, invalid input is retried, and EOF cancels the entire wizard.
Approved removals are held in memory until all prompts are answered, then written
together in one project-file update. Declining everything is a byte-for-byte
no-op. Home and local files are never written.

`CleanupPlan.Apply` validates candidate keys and compares both files with their
planning snapshots before writing, so an intervening edit is rejected instead of
being overwritten. Project-only entries and unknown project data are ignored.
Rerunning cleanup after approved removals is idempotent.

CLI help, the full manual, short usage, and `docs/CLI.md` document the new
console-only surface and its safety behavior.

## Issues found during implementation and validation

- YAML document nodes initially reached mapping-only analysis directly, yielding
  no candidates. Planning now analyzes each document's mapping node.
- Repeated project catalog IDs initially compared the first entry rather than the
  effective last entry. The planner now classifies the effective value and an
  approved removal deletes all project entries with that ID.
- Real terminal validation exposed loss of a leading file comment when the first
  setting was removed. YAML deletion now transfers that comment to the next
  setting, or to the mapping when no settings remain.
- Final review found the same comment-loss edge when an approved provider/model
  removal emptied the first catalog section. A dedicated RED regression failed
  with only `execution` remaining; routing empty-catalog deletion through the
  comment-preserving helper fixed it.
- The B40 OAuth filmstrip could fail when legitimate pre-refresh scrollback
  remained visible beside the refreshed card. Its negative assertions now target
  the latest quota card, while positive rendering assertions remain unchanged.
- One unrelated timing-sensitive `TestWriteStreamingLagRepro/delta=64B` run
  crossed its 4x threshold. The isolated reproduction and subsequent full app
  run passed without rendering changes; the final race-enabled repository suite
  also passed.

## Regression coverage

Configuration tests cover exact duplicates, conflicting selections and catalog
entries, same-provider/different-key conflicts, project-only preservation,
unknown fields, shared top-level sections, duplicate IDs, approved-only writes,
idempotent reruns, concurrent home/project edits, missing files, and leading
comments for both first-setting and emptied-catalog deletion.

Console tests cover accept, decline, default-no, invalid-input retry, EOF after
an earlier approval, byte-identical declines, empty plans, concise summaries,
credential redaction, and the absence of TUI escape sequences. Help tests cover
short usage, manual content, and cleanup help-topic routing.

The final B40 OAuth/quota regressions were also rerun after the filmstrip
assertion correction:

- `go test -timeout 60s ./plugins -run 'TestQuota_(OAuthLoginRefreshesCachedAuthRequired|UnconfiguredProvidersStayHidden|ProviderRemovalPrunesCachedRows)' -count=1 -v` — PASS.
- `go test -timeout 30s ./internal/app -run 'TestQuotaPlugin_(ZaiOAuthThenQuota|ExtendedInfoInTUI)_Filmstrip' -count=1 -v` — PASS.

## Real console validation

A production binary at `/tmp/goa-b41` was driven through Expect PTY scripts using
isolated home/project fixtures under `/tmp/goa-b41-validation.DO1Oui`. The
transcripts verified the actual command rather than only its internal function:

1. EOF after an earlier approval cancelled the wizard and left the project file
   unchanged.
2. Declining all five candidates left the project file byte-identical.
3. A mixed run with invalid input removed three approved items and kept two.
4. Accepting the remaining two conflicts removed both; the summary reported two
   removed and zero kept.
5. A final rerun reported no candidates.

No transcript contained a fixture secret or TUI CSI/OSC/DCS escape sequences.
Home and local configuration hashes remained unchanged. The final project file
retained its leading comment, project-only provider/model, and
`execution.retries: 7`. No validation server or browser was started, and the
final process check found no Goa cleanup or Expect process left running.

## Final verification

- Focused config cleanup and console regressions — PASS.
- `go test -count=1 -timeout 180s ./config ./internal/app` — PASS.
- `go test -count=1 -race -cover ./...` — PASS, all packages.
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `git diff --check` — PASS.

`gocognit -over 15 .` and `gocyclo -over 12 .` contain no new B41 findings. The
remaining unrelated, pre-existing gocognit reports are
`TestResolveStartPlanAPIKey` 23, `GoalDriver.Drive` 21, `initGoalSystem` 16, and
`AgentManager.executeRunner` 16. The remaining gocyclo reports are
`TestDeviceMid_PersistsAndStable` 13 and `GoalDriver.Drive` 13.
