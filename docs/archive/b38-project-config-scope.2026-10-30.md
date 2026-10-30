# B38 — Keep provider configuration global unless a project explicitly overrides it

Closed: 2026-10-30

## Report

Project `.goa/config.yaml` files could acquire provider definitions, API keys,
headers, model catalogs, and general settings copied from the merged global
configuration. Besides exposing credentials in project files, those copied keys
became high-precedence pins that prevented later home-config changes from taking
effect.

## Root cause and resolution

`AgentManager.saveModelThinkingLevel` correctly saved the changed model catalog
to the home layer, then called `SaveProjectProvidersAndModels` when automatic
project model persistence was enabled. That project writer unmarshaled an
existing project file into a full `Config`, replaced its selection and catalogs
from the merged in-memory config, and marshaled the entire structure. This both
copied global providers/models and materialized zero-valued general sections.

Thinking-level persistence now uses `SaveProjectActiveModel` for the project
write. The catalog remains in `~/.goa/config.yaml`; only `active_provider` and
`active_model` may be pinned in the project. Field-scoped YAML edits preserve
all intentional project overrides without serializing unrelated settings.

The exported `SaveProjectProvidersAndModels` compatibility wrapper is retained
for existing callers but is deprecated and delegates to the same selection-only
operation. It can no longer copy provider credentials or model catalogs.
Explicit project provider/model definitions, credentials, selections, tool
flags, and compression overrides continue to participate in the normal cascade
and survive selection persistence.

## Regression coverage

- `TestAgentManager_SetThinkingLevel_KeepsGlobalConfigOutOfProject` uses real
  home/project files with an API key, secret header, provider endpoint, model
  catalog, and global compression setting. It failed before the fix with copied
  `providers`, `models`, `execution`, and other general sections; it now proves
  only the selection and pre-existing project tool override remain. A fresh
  project inherits the global selection and saved thinking level without a
  config file being created by loading.
- `TestCascadeScope_FreshProjectInheritsWithoutWriting` covers global provider
  credentials, model selection, and general settings with no project file.
- `TestProjectProviderOverrides_SurviveSelectionPersistence` proves explicit
  project provider/model credentials and selections, compression, and tool
  overrides still win and remain intact, while no global secret or unrelated
  section is added by the compatibility wrapper.
- `TestAgentManager_SetThinkingLevel_AutoSaveModel` now distinguishes an
  intentional active-selection save from a forbidden project catalog save.

## Verification run

- RED reproduction: `go test -count=1 -timeout 30s ./core -run '^TestAgentManager_SetThinkingLevel_KeepsGlobalConfigOutOfProject$'`
  failed on the original writer and passed after the fix.
- `go test -count=1 -timeout 120s ./config ./internal/app ./core/commands` — PASS
- `go test -count=1 -timeout 120s ./core` — PASS
- `go vet ./...` — PASS
- `staticcheck ./...` — PASS
- `go test -count=1 -race -cover -timeout 300s ./...` — PASS
- `git diff --check` — PASS

Complexity and file-size scans contain no new B38 findings. Existing unrelated
reports remain `TestResolveStartPlanAPIKey` 23, `GoalDriver.Drive` 21,
`initGoalSystem` 16, and `AgentManager.executeRunner` 16 for gocognit;
`TestDeviceMid_PersistsAndStable` 13 and `GoalDriver.Drive` 13 for gocyclo; and
the pre-existing `internal/webui/browserclient2_test.go` hard file-size report.
