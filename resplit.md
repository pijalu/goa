# Plan: functional resplit of hard-limit files

## Objective

Reduce every Go file above the 1,000-line hard limit to semantic files below the limit, without reverting the prior semantic naming work, restoring deleted originals, changing behavior, or introducing numbered/generated filenames. The current hard-limit inventory contains 27 files and is recorded below.

## Non-negotiable rules

1. Split only at complete declarations (types, vars, consts, funcs, methods, and their comments). Never split a function body.
2. Name files after one functional responsibility. Never use `_01`, `_02`, `_split_`, `_features_NN`, or `_behavior_NN`.
3. Keep production and test code separate. Test fixtures shared by several files go in an explicitly named `<feature>_test_helpers_test.go` file.
4. Preserve package-private symbols, init behavior, declaration text, imports, test names, and file-level build constraints.
5. Do not restore deleted originals or change checker thresholds/suppressions.
6. Target 300–800 lines per file where practical; absolute maximum 999 lines.
7. Before deleting a source file, compare declarations and references to prove every symbol has a destination.

## Baseline and inventory procedure

Before each batch:

```sh
cp /dev/null /tmp/resplit-baseline  # replace with captured command output
bash .agents/skills/golang-check/go-file-size-check.sh
find . -name '*.go' -type f | sort
```

Use `go/parser`/`go/ast` to inventory each hard file: declaration name, kind, receiver, start/end lines, and referenced package-private identifiers. Record the mapping in the change description. Use `goimports` only after declarations are moved; inspect imports manually.

After each batch:

```sh
gofmt -w <changed files>
go test <focused package>
go test <focused package> -run <affected behavior>  # where useful
git diff --check
```

## Hard-file mapping and exact functional destinations

### `config`

- `config/config_model.go` (1,555): split into:
  - `config_core_types.go`: `Config`, registry/container types, common shared fields.
  - `config_provider_types.go`: provider, model, pricing, cache, completion, execution, retry types.
  - `config_goal_types.go`: goals, retention, plans, memory, dreams, skills, tools.
  - `config_ui_types.go`: transparency, plugins, thinking, time-context, compression, and related UI/runtime settings.
  - `config_model_accessors.go`: methods and normalization/default helpers, if present.
- `config/config_behavior_test.go` (1,839): classify tests by name and move into:
  - `config_validation_test.go`
  - `config_merge_test.go`
  - `config_defaults_test.go`
  - `config_provider_test.go`
  - `config_test_helpers_test.go`
- `config/loader_behavior_test.go` (1,016): split into `loader_load_test.go`, `loader_save_test.go`, and `loader_migration_test.go`; shared fixtures into `loader_test_helpers_test.go`.

### `core`

- `core/agentmanager_behavior_test.go` (2,531): group by test subject into `agentmanager_lifecycle_test.go`, `agentmanager_config_test.go`, `agentmanager_tools_test.go`, `agentmanager_loop_test.go`, and `agentmanager_test_helpers_test.go`.
- Each destination is based on test names and fixture dependencies, not current line ranges.

### `core/commands`

- `goal_command.go` (1,457): split into:
  - `goal_command.go`: registration, metadata, top-level dispatch only.
  - `goal_args.go`: argument/token/objective parsing.
  - `goal_status.go`: status/current/list formatting and todo/budget rendering.
  - `goal_pause_resume.go`: pause, resume, promotion notes.
  - `goal_queue.go`: next/last/reorder/cancel queue mutations.
  - `goal_manager.go`: interactive queue manager, edit/move/delete/create prompts.
  - `goal_lifecycle.go`: create/replace/start and autonomy permission flows.
  - `goal_verify.go`: verification and event-log/settings concerns.
- `goal_command_test.go` (2,663): split by behavior into `goal_args_test.go`, `goal_status_test.go`, `goal_pause_resume_test.go`, `goal_queue_test.go`, `goal_manager_test.go`, `goal_lifecycle_test.go`, and `goal_verify_test.go`; shared fakes/fixtures into `goal_test_helpers_test.go`.
- `config_menu_behavior_test.go` (1,298): split into `config_menu_navigation_test.go`, `config_menu_models_test.go`, `config_menu_providers_test.go`, and `config_menu_test_helpers_test.go`.

### `core/orchestrator`

- `runtime_runtime.go` (1,130): split into `runtime_lifecycle.go` (construction, IDs, close), `runtime_events.go` (bus/emission/subscription), and `runtime_execution.go` (delegation, agent actions, result handling).

### `internal/agentic`

- `agent_runtime.go` (1,614): split into `agent_constructor.go`, `agent_configuration.go`, `agent_history.go`, `agent_observers.go`, and `agent_state.go` according to type/API responsibility.
- `agent_runtime_test.go` (2,943): split into `agent_run_test.go`, `agent_history_test.go`, `agent_observer_test.go`, `agent_state_test.go`, and `agent_test_helpers_test.go`.
- `compression_runtime.go` (1,433): split into `compression_summary.go`, `compression_pruning.go`, `compression_strategies.go`, and `compression_recovery.go`.
- `compression_behavior_test.go` (1,075): split into `compression_summary_test.go`, `compression_pruning_test.go`, `compression_strategy_test.go`, and `compression_test_helpers_test.go`.
- `stream_runtime.go` (2,288): split into `stream_rounds.go`, `stream_events.go`, `stream_tool_calls.go`, `stream_state.go`, `stream_context.go`, and `stream_retry.go`.
- `streamloop_behavior_test.go` (1,063): split into `stream_loop_detection_test.go` and `stream_loop_test_helpers_test.go`.

### `provider`

- `manager_runtime.go` (1,175): split into `manager_configuration.go`, `manager_models.go`, `manager_resolution.go`, `manager_stream_options.go`, and `manager_auth.go`.
- `manager_behavior_test.go` (1,357): split into `manager_models_test.go`, `manager_resolution_test.go`, `manager_stream_options_test.go`, `manager_auth_test.go`, and `manager_test_helpers_test.go`.

### `internal/app`

- `app_behavior_test.go` (1,363): split into `app_events_test.go`, `app_headless_test.go`, `app_stats_test.go`, `app_subsystems_test.go`, and `app_test_helpers_test.go`.
- `events_runtime.go` (1,247): split into `events_agent.go`, `events_control.go`, `events_chat.go`, `events_overlays.go`, and `events_goals.go`.
- `headless_runtime.go` (1,002): split into `headless_lifecycle.go`, `headless_readers.go`, and `headless_rendering.go`.
- `stats_runtime.go` (1,484): split into `stats_stream.go`, `stats_usage.go`, `stats_compaction.go`, and `stats_format.go`.
- `subsystems_runtime.go` (1,533): split into `subsystems_base.go`, `subsystems_agent.go`, `subsystems_goal.go`, `subsystems_commands.go`, `subsystems_workflows.go`, and `subsystems_assembly.go`.

### `tui`

- `tui_runtime.go` (1,524): split into `tui_lifecycle.go`, `tui_input.go`, `tui_render.go`, `tui_overlays.go`, and `tui_scene.go`.
- `compositor_runtime.go` (1,774): split into `compositor_scene.go`, `compositor_render.go`, `compositor_scrollback.go`, and `compositor_cursor.go`.
- `chat_viewport_runtime.go` (1,013): split into `chat_viewport_state.go`, `chat_viewport_messages.go`, and `chat_viewport_tools.go`.
- `tool_execution_runtime.go` (1,043): split into `tool_execution_state.go`, `tool_execution_args.go`, and `tool_execution_render.go`.
- `selector_behavior_test.go` (1,137): split into `selector_render_test.go`, `selector_input_test.go`, `selector_reorder_test.go`, and `selector_test_helpers_test.go`.
- `editor_behavior_test.go` (1,187): split into `editor_input_test.go`, `editor_render_test.go`, `editor_completion_test.go`, and `editor_test_helpers_test.go`.

## Execution order

1. Build the AST inventory and declaration-to-destination manifest for all 27 files.
2. Split `core/commands/goal` first and run `go test ./core/commands`.
3. Split config, agent manager, and orchestrator; run each focused package test.
4. Split provider and agentic production/test groups; run `go test ./provider` and `go test ./internal/agentic` after each family.
5. Split app groups; run `go test ./internal/app` after each family.
6. Split TUI groups, beginning with compositor regression tests; run `go test ./tui` after each family.
7. Run the size checker and inspect every remaining violation. Address soft violations only after all hard violations are gone, prioritizing files above 800 lines.
8. Run `gofmt`, `go vet ./...`, `bash .agents/skills/golang-check/golang-check.sh`, and `go test ./...`.

## Validation and rollback

For each batch, save the focused test output and declaration inventory. If a test fails, first compare the moved declarations/imports and package initialization behavior; fix the root cause rather than weakening tests. If a batch cannot be explained, revert only that batch while preserving earlier validated batches. Confirm no numbered split files, duplicate declarations, restored deleted originals, or unrelated modifications are introduced.

## Completion criteria

- `go-file-size-check.sh` reports zero hard violations (and ultimately zero file-size violations if the checker requires soft compliance).
- Every new file has a functional name and is below 1,000 lines.
- `gofmt -l` is empty, `go vet ./...` passes, and `go test ./...` passes.
- Complexity remediation remains behavior-preserving and is not hidden by suppressions or checker changes.
