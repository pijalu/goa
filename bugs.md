# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
    - `go vet ./...`
    - `staticcheck ./...`
    - `gocognit -over 15 .`
    - `gocyclo -over 12 .`
    - `go test -count=1 -race -cover ./...`
    Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

# To fix

## Loop minimum length must be configurable (default: min 50 characters)
The minimum length of a repeated loop unit is hardcoded in the stream-loop detector; it must be configurable via the config file and the /config loop menu, with a default minimum of 50 characters.

**Current state (traced):**
- `internal/agentic/agent_streaming.go`: `streamLoopExactMinPeriod = 60` — smallest repeated unit (in characters) Detector A considers; below it only punctuation/connector noise lives. Hardcoded const, not configurable.
- Companion consts in the same block (`streamLoopSmallPeriod = 8`, `streamLoopLongPeriod = 1024`, `streamLoopTailWindow = 4096`, Detector B `streamLoopMinWords = 80` etc.) — decide per setting which stay const; the user-facing ask is the min loop length.
- Existing precedent for exposing stream-loop knobs: `execution.stream_loop_max_repeats` already flows config file → CLI (`config_cli.go` case) → agentic config.
- Config file: `config/config.go` `ExecutionConfig` (yaml under `execution:`); defaults in `config/configs/default.yaml`.
- Menu: `/config → Loop detection` (`core/commands/config.go` `settingLoopDetection`, label `loopDetectionLabel`) — new row must open a picker or value prompt (no dead rows; see archived compression-menu bug).

**Requirement:**
- New config key (suggested: `execution.stream_loop_min_period`, int, characters) with default 50 in `default.yaml`.
- 0/unset → default 50; validation: reject values below a sane floor (e.g. < 10 connector-noise territory) and above `streamLoopTailWindow`.
- Wire through: config struct + yaml default → merge (`config_merge.go`) → validation (`config_validate.go`) → CLI `/config set` (`config_cli.go`) → agentmanager → agentic config → `streamLoopScan`/Detector A uses configured value instead of the const.
- Add row in `/config → Loop detection` menu with inline value prompt or picker; update `loopDetectionLabel` summary if it stays short.

**Plan:**
1. Add `StreamLoopMinPeriod int` to `config.ExecutionConfig` + `default.yaml` (50, commented).
2. Merge + validation (range check) + `config_cli.go` setInt entry.
3. Agentic SDK: add field to the relevant config struct, thread through agentmanager wiring (`core/agentmanager_lifecycle.go`), replace `streamLoopExactMinPeriod` usage (keep const as the default fallback).
4. Menu row + prompt in `settingLoopDetection`; ensure it applies and redraws.
5. Tests: config default/validation/merge round-trip; CLI set; stream-loop detection fires at configured floor (test at 50 catches a 50–59-char repeat that old default missed; lowering/raising shifts detection); menu row actionable (no dead row).
6. Validate per bug guidelines (go vet, staticcheck, gocognit, gocyclo, go test -race -cover) + PTY check of the /config menu row.
