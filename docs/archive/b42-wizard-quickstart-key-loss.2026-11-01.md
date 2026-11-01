# B42 — Quick-start setup wizard: API key loss and question overload

Closed: 2026-11-01

## Report

The start wizard lost the API key (and custom endpoints): the
`advanceFromEndpoint` / `advanceFromKey` transitions called `commitTextInput()`
— which commits the field and clears the line editor — and then re-assigned the
field from the already-cleared editor, so both values saved as empty. A second
defect in the same input path swallowed every digit key `1`–`9` while a text
editor was open: the digit quick-pick handler intercepted them before the
editor, so API keys and endpoint ports saved truncated (`sk-test-1234567890`
became `sk-test-0`).

Beyond the input bugs, the flow contradicted the intended quick start: it asked
companion questions (with companion enabled by default), execution mode, skill
mode, webfetch summary, memory dream, advanced options, and prompt/workflow
copying — ten screens past the model selection.

## Resolution

The wizard is pruned to a genuine quick start:

- **Flow**: welcome → provider (presets + custom) → (endpoint, custom only) →
  (API key, when required) → provider review → model select (live `/v1/models`
  catalog, manual setup fallback) → model advanced (reasoning, thinking level —
  default `high`) → done.
- **Single commit point**: `commitTextInput()` is the only place the editor is
  copied into the slot; transitions no longer re-read the cleared editor.
- **Digits are content**: quick-pick digits apply only on selection screens;
  while an editor is open (endpoint / API key / model fields) digits reach the
  editor.
- **Fixed quick-start posture**, applied without asking: `coding-posture`
  major with `yolo` autonomy (both the canonical mode state and the legacy
  `execution.mode` fallback), thinking level `high` (explicit `off` respected),
  companion never enabled and never asked, and skills / tools / webfetch /
  dream / compression settings left untouched — the wizard writes only the
  provider, model, thinking options, and posture.
- Companion, dream, webfetch, mode, skill-mode, advanced-options, and
  preview/copy states and fields were deleted along with their renderers and
  handlers.

## Validation

- RED tests (all failed before the fix): key survives key entry with digits;
  custom endpoint survives entry with derived provider identity; flow visits
  only provider+model states; saved config carries coding-posture/yolo, unset
  companion, the verbatim key, and `thinking_level: high`; skills/tools config
  untouched; saved-file round trip through the real home config.
- End-to-end terminal-loop test drives the whole flow through the real
  `runSetupWizardWithTerminal` against a local model-catalog HTTP server:
  every screen renders, digits in the endpoint port and key survive, the done
  screen shows the resolved config path, the transcript contains no removed
  screens, and the saved `~/.goa/config.yaml` (sandboxed home) carries the
  verbatim key, endpoint, posture, and thinking level.
- Invariant suite updated to the 9-state flow: every state renders, advances
  on Enter, has an Escape back path, has a step number, option screens respond
  to Up/Down, advertised quick-picks are handled.
- `go vet ./...`, `staticcheck ./...`, full `go test -count=1 -race` repository
  suite, and complexity budgets (gocognit/gocyclo) pass; the two wizard tests
  that exceed budgets were refactored into step-helpers. Remaining gocognit/
  gocyclo reports are pre-existing unrelated hotspots
  (`provider/manager_auth_startplan_test.go`, `core/goal_driver.go`,
  `core/agentmanager_turns.go`, `internal/app/subsystems_goal.go`).
