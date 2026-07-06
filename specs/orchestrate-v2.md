<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Orchestrator v2 Specification

This document defines the complete design for the `/orchestrate` command refresh. It consolidates all clarifications from the planning discussion and is intended to be the implementation reference.

## 1. Scope and Goals

- Unify `/orchestrate` on the standard Goa colon syntax.
- Make the command interactive when required arguments are missing.
- Expose the full orchestrator configuration through `/config`.
- Replace cryptic run IDs with memorable `adjective.noun` friendly names (e.g., `happy.hare`).
- Add `delete` (single run and bulk `*`) with confirmation and safe active-run handling.
- Integrate ephemeral orchestrator-managed goals that are protected from normal goal operations.
- Add retention/housekeeping for completed runs and normal goals.
- Wire TUI tab context into the main input line so steering is discoverable.
- Keep all documentation and tests in sync with the changes.

## 2. Terminology

| Term | Meaning |
|------|---------|
| **Run** | A single orchestrator execution, persisted under `.goa/orchestrator/<internal-id>/`. |
| **Internal Run ID** | Stable, machine-oriented identifier (e.g., `run-<unix-nano>-<hex>`). It is the directory name and the `RunID` in events. |
| **Friendly Name** | Human-readable alias stored in the run event log (e.g., `happy.hare`). It is **not** the directory name. |
| **Orchestrator-Managed Goal** | A goal created and owned by a run. It is ephemeral, protected from normal goal mutations, and deleted when the run completes or is deleted. |
| **Normal Goal** | A goal created through `/goal`. Subject to its own retention policy. |
| **Active Run** | A run currently held by `orchestrator.ActiveRuntime`. It may still be executing. |

## 3. Configuration Schema

### 3.1 `config.OrchestratorConfig`

Extend the existing struct with a `Retention` field. The existing `Roles`, `Pool`, and `Defaults` fields remain and are also exposed through `/config`.

```go
type OrchestratorConfig struct {
    Roles     map[string]OrchestratorRole     `yaml:"roles,omitempty"`
    Pool      OrchestratorPoolConfig          `yaml:"pool,omitempty"`
    Defaults  OrchestratorDefaultsConfig       `yaml:"defaults,omitempty"`
    Retention OrchestratorRetentionConfig      `yaml:"retention,omitempty"`
}

type OrchestratorRetentionConfig struct {
    Enabled bool `yaml:"enabled"` // default: true
    Days    int  `yaml:"days"`    // default: 7, 0 treated like "never"
}
```

Validation rules:
- `Retention.Days` must be `>= 0`. Negative values are rejected at load time.
- `Retention.Enabled == false` means "never delete".
- `Retention.Enabled == true && Retention.Days == 0` also means "never delete" (explicit safety).

### 3.2 Normal Goal Retention

Add a parallel retention section to the goal configuration:

```go
type GoalsConfig struct {
    // existing fields...
    Retention GoalsRetentionConfig `yaml:"retention,omitempty"`
}

type GoalsRetentionConfig struct {
    Enabled bool `yaml:"enabled"` // default: true
    Days    int  `yaml:"days"`    // default: 7, 0 means never
}
```

Validation rules are the same as for orchestrator retention.

### 3.3 Defaults

- `orchestrator.defaults.topology` default: `"hub"`.
- `orchestrator.retention.enabled` default: `true`.
- `orchestrator.retention.days` default: `7`.
- `goals.retention.enabled` default: `true`.
- `goals.retention.days` default: `7`.

## 4. `/config` Integration

Add a dedicated top-level menu item in `/config` called **Orchestrator**. It must support full CRUD via interactive lists and main-input prompts.

Sub-pages:

1. **Roles** – list/add/edit/remove orchestrator roles.
   - Each role shows: name, model, provider, allowed tools count.
   - Add/edit flows prompt for: name, model, provider, allowed tools (comma-separated).
   - Remove shows a confirmation.
2. **Pool** – edit `max_total_agents` and `max_agents_per_model`.
3. **Defaults** – select default topology (hub/fanout/pipeline) from a list.
4. **Retention** – toggle enabled and edit days via main input.

Add a **Goals** menu item (or extend an existing one) to expose `goals.retention.enabled` and `goals.retention.days`.

All changes are saved to the home configuration file via `ConfigSaver`. Validation errors surface as flash messages.

## 5. Run Identity and Naming

### 5.1 Internal ID

The internal run ID remains the directory name under `.goa/orchestrator/`. It must be generated with cryptographic randomness (reuse `internal.PrefixedHexID("run", 4)` instead of `time.Now().UnixNano()` to avoid collisions and predictable IDs).

### 5.2 Friendly Name

On every new run, generate a friendly name using the existing `internal.FriendlyNameUnique` helper, scoped to names currently in use among existing runs.

The first event in the run log records the name:

```go
Event{
    Type: EventRunStarted,
    Payload: map[string]any{
        "objective": objective,
        "topology":  string(topology),
        "name":      friendlyName,
    },
}
```

`RunSummary` gains a `Name` field populated from this event.

### 5.3 Custom Names

`/orchestrate:new` accepts an optional `name=<value>` argument. Rules:
- Lowercase only.
- Permitted characters: `a-z`, `0-9`, `.`, `-`, `_`.
- Must be unique among existing run names and must not collide with an existing internal ID.
- Empty or invalid names fall back to auto-generation with a flash warning.
- Custom names do not need to follow `adjective.noun`.

### 5.4 Name Resolution

When a command accepts an `id` argument, resolution order is:
1. If the input matches an existing friendly name, use that run.
2. If the input matches an existing internal run ID, use that run.
3. Otherwise, error: `run %q not found`.

## 6. Command Surface

All commands use colon-only syntax. The legacy positional syntax is removed.

### 6.1 Bare `/orchestrate`

Opens an interactive action list with items: **new**, **resume**, **delete**.

### 6.2 `/orchestrate:new`

Required argument: `objective`. Optional arguments: `topology`, `name`, `goal` (ignored, kept for forward compatibility if present, see §8).

Examples:

```
/orchestrate:new
/orchestrate:new:objective=Build auth system
/orchestrate:new:topology=fanout,objective=Build auth system
/orchestrate:new:name=custom.id,objective=Build auth system
```

When `objective` is missing, the TUI prompts for it via the main input line. Topology defaults to the configured default. The run is automatically bound to an ephemeral orchestrator-managed goal (§8).

### 6.3 `/orchestrate:resume`

Required argument: `id` (friendly name or internal ID). Resumes an unfinished run.

```
/orchestrate:resume:id=happy.hare
```

If `id` is missing, show a filterable list of unfinished runs. If no unfinished runs exist, flash a message and return to the menu.

### 6.4 `/orchestrate:delete`

Required argument: `id` (friendly name, internal ID, or `*`). Optional argument: `confirm=true`.

```
/orchestrate:delete:id=happy.hare
/orchestrate:delete:id=*
/orchestrate:delete:id=happy.hare,confirm=true
```

- If `id` is missing, show a filterable list of runs.
- If `id=*`, all runs are selected.
- If `confirm=true`, proceed immediately.
- Otherwise, show a confirmation dialog in the TUI.
- In non-interactive contexts with no confirmation, return an error: `delete requires confirmation; add confirm=true`.

Active runs are stopped before deletion by clearing `ActiveRuntime` and canceling the run context.

### 6.5 `/orchestrate:steer`

Required arguments: `id` (agent ID, `all`, or `orchestrator`) and `message`.

```
/orchestrate:steer:id=coder-1,message=fix the bug
/orchestrate:steer:id=all,message=sync up
```

If arguments are missing, the TUI prompts for them. The `message` is provided through the main input line.

### 6.6 `/orchestrate:list`

Opens an interactive filterable list of all runs. Each item shows friendly name, status, topology, objective preview, and updated time. Selecting a run shows details or offers resume/delete actions (implementation may choose one consistent behavior; recommend details with sub-actions).

### 6.7 Help Suffixes

`/orchestrate?` and `/orchestrate??` continue to return short and long help.

## 7. Interactive Flow Reference

### 7.1 Bare `/orchestrate` → new

1. Main input prompt: `Objective:`
2. Create run with configured default topology and auto-generated friendly name.
3. Bind ephemeral goal.
4. Start run.

### 7.2 Bare `/orchestrate` → resume

1. Filterable list of runs.
2. User selects a run.
3. If the run is finished, flash "run already finished" and return.
4. Otherwise, resume with the stored objective.

### 7.3 Bare `/orchestrate` → delete

1. Filterable list of runs.
2. User selects a run (or a `— delete all —` item at the bottom).
3. Confirmation dialog: "Delete happy.hare and its goal?" / "Delete all N runs?"
4. On confirm, stop active runs, delete directories, delete associated goals.
5. Return to the action list.

### 7.4 Escape and Ctrl-C Behavior

- **Escape** in any selector or input cancels the current step and returns to the previous screen (or closes the menu if at the top).
- **Ctrl-C** cancels the entire interactive flow and returns to the main chat input.

## 8. Goal Integration

### 8.1 Ephemeral Orchestrator Goals

Every new orchestration automatically creates a goal:
- Goal ID: generated by the goal system (stable internal ID).
- Goal name: the run's friendly name.
- Goal objective: the run's objective.
- Marker: a `managed_by: orchestrator` tag stored in the goal event log.

The runtime's `GoalBinder` is used for token accounting, exactly as today.

### 8.2 Lifecycle

- Created on `/orchestrate:new`.
- Deleted when the run finishes (success, failure, or crash).
- Deleted when the run is explicitly deleted.

Deletion uses the goal system's existing delete/clear mechanism, not a hard file removal, so the audit trail remains in the goal event log.

### 8.3 Protection in `/goal`

Orchestrator-managed goals:
- Appear in `/goal:list` with a marker (e.g., `[orch]` or `🎼`).
- Are read-only in `/goal:show`.
- Reject mutating operations: `next`, `clear`, `complete`, `block`, `reorder`, `replace`. Each returns an error like `goal happy.hare is managed by /orchestrate`.

The goal system must expose a way to query whether a goal is orchestrator-managed (e.g., a method on `GoalSnapshot` or `GoalMode`).

## 9. Retention and Housekeeping

### 9.1 Orchestrator Runs

After a run reaches a terminal state (`Finished == true`), it is eligible for deletion after `orchestrator.retention.days` days.

Trigger:
- On application startup.
- Periodically every 60 minutes (configurable constant, not exposed to users).
- After any `/orchestrate:delete` or run completion (optional opportunistic pass).

Behavior:
- Scan all run directories.
- For each run that is finished and whose `UpdatedAt` is older than retention days, delete the directory.
- Log the count of deleted runs via a flash message (only if > 0).
- If retention is disabled (`Enabled == false` or `Days == 0`), skip.

### 9.2 Normal Goals

After a normal goal reaches a terminal state (`complete` or `blocked`), it is eligible for deletion after `goals.retention.days` days.

Trigger and behavior mirror the orchestrator run cleanup, using the goal event log timestamps.

### 9.3 Orchestrator-Managed Goals

These are **not** subject to retention; they are deleted immediately on run completion or run deletion (§8.2).

## 10. TUI Tab Input Steering

When the orchestrator Summary tab is active:
- If a specific agent row is selected, the main input line label changes to `steer <agent-id|role>:` and submitted text is routed to `/orchestrate:steer:id=<agent-id>,message=<text>`.
- If no agent is selected, the label changes to `steer orchestrator:` and submitted text is routed to the orchestrator role (or `all` if no orchestrator role is active).
- The user can still type explicit `/orchestrate:steer` commands; those take precedence over the contextual routing.
- A small footer hint indicates the current steering target while the tab is active.

## 11. Implementation Phases

Each phase produces runnable tests and keeps the build green.

### Phase 1: Configuration Schema and Defaults
- Add `OrchestratorRetentionConfig`, `GoalsRetentionConfig`, and validation.
- Update `config/defaults.go` and `config/config_validate.go`.
- Add unit tests for validation and defaults.

### Phase 2: Run Name and Identity
- Generate stable internal run IDs with `internal.PrefixedHexID`.
- Store `name` in `EventRunStarted` payload.
- Add `Name` to `RunSummary` and populate it from the replay.
- Add name resolution helper (`ResolveRunID(rootDir, id) (internalID, friendlyName, error)`).
- Add custom name validation.
- Add unit tests for name generation, collision, resolution, and custom names.

### Phase 3: Colon Syntax Parser for `/orchestrate`
- Introduce `OrchestrateInput` struct with typed fields.
- Implement parser that consumes the colon-split args and produces the struct.
- Reject legacy positional syntax.
- Add unit tests for all valid and invalid forms.

### Phase 4: Delete Implementation
- Implement `DeleteRun(rootDir, id, activeRuntime, confirm)` helper.
- Stop active runs before deleting.
- Support `id=*` bulk delete.
- Wire `confirm=true` and interactive confirmation.
- Add unit tests for single, bulk, active, and missing-confirm cases.

### Phase 5: Interactive Bare Menu and Flows
- Implement the action selector (`new`, `resume`, `delete`).
- Implement filterable run lists for resume/delete (reuse existing `tui.Selector` filtering).
- Implement objective prompt via `ShowInput`.
- Wire the flows to the parser and runtime.
- Add TUI tests using the `tui-test` skill.

### Phase 6: Goal Integration
- Tag goals created by orchestrator as managed.
- Delete ephemeral goals on run completion and run deletion.
- Block goal mutations in `/goal` commands.
- Add unit tests for create/delete/protection.

### Phase 7: Retention/Housekeeping
- Implement a cleanup function for runs and another for goals.
- Schedule cleanup on startup and periodically.
- Add unit tests with mocked clocks and fake file systems.

### Phase 8: `/config` Orchestrator Menu
- Add the top-level `/config → Orchestrator` menu.
- Implement CRUD sub-pages for roles, pool, defaults, and retention.
- Add the `/config → Goals → Retention` page.
- Add TUI tests.

### Phase 9: TUI Tab Input Steering
- Add contextual input labeling in the orchestrator Summary tab.
- Route submitted text to the appropriate steering target.
- Add TUI tests.

### Phase 10: Documentation and Help
- Rewrite `core/commands/help/orchestrate.long.md` to match the colon syntax.
- Update `core/commands/help/help_colon_syntax_test.go` forbidden list if needed.
- Update config documentation and README sections.
- Update AGENTS.md or inline comments if needed.

### Phase 11: Integration and Acceptance
- Run `go vet ./...`, `go test -count=1 -race -cover ./...`, `gocognit -over 15`, `gocyclo -over 12`.
- Validate interactive flows with `interactive_shell` / `tui-test` skill.
- Manual smoke test: create, list, resume, delete, and configure via TUI.

## 12. Testing Strategy

### Unit Tests
- Config validation and defaults.
- Name generation, uniqueness, custom name validation, resolution.
- Colon syntax parser (table-driven).
- Delete logic (single, `*`, active, missing confirmation, non-existent run).
- Goal tagging and protection.
- Retention math with fake clock and `tui.TempDir()`.

### TUI Tests
- Use the `tui-test` skill: drive an event sequence through the app layer and assert on a filmstrip of ANSI-free states.
- Cover: bare menu, new flow prompting for objective, resume list filtering, delete list + confirmation, `/config → Orchestrator` menu, tab input label changes.

### Interactive Shell Tests
- Build the binary and exercise the commands in a real terminal overlay.
- Verify `/orchestrate` opens the menu, `/orchestrate:new:objective=...` runs, `/orchestrate:delete:id=*,confirm=true` cleans up, `/config` shows the new menu.

### Static Analysis Gate
Before any phase is considered complete:
- `go vet ./...`
- `go test -count=1 -race -cover ./...`
- `gocognit -over 15`
- `gocyclo -over 12`

If a function exceeds the budget, refactor it into smaller, composable helpers (factories + small primitives).

## 13. Files to Modify

- `config/config.go` – new retention structs.
- `config/defaults.go` – default retention values.
- `config/config_validate.go` – validation.
- `config/config_test.go` – tests for the above.
- `internal/idgen.go` – stable run ID generation, name validation helper.
- `core/orchestrator/runtime.go` – use stable internal ID, emit `name` in run-started event.
- `core/orchestrator/run_snapshot.go` – add `Name` to `RunSummary`, add name resolution, add delete helper.
- `core/orchestrator/store.go` – (if needed) directory deletion helper.
- `core/commands/orchestrate.go` – full rewrite for colon syntax, interactivity, delete, list.
- `core/commands/orchestrate_command_test.go` – parser and command tests.
- `core/commands/orchestrate_test.go` – integration tests.
- `core/commands/config.go` – new `/config → Orchestrator` menu pages.
- `core/commands/config_menu_test.go` – TUI tests.
- `core/commands/goal.go` and `core/goal/mode.go` – orchestrator-managed goal tagging and protection.
- `core/goal/model.go` – (if needed) expose managed flag.
- `core/commands/help/orchestrate.long.md` – rewrite.
- `core/commands/help/help_colon_syntax_test.go` – update forbidden list.
- `README.md` and any orchestrator docs in `docs/` – update.
- `internal/app/` – scheduling of retention cleanup, active runtime clearing on delete.
- `tui/` – contextual input label support for Summary tab.

## 14. Complexity and Design Constraints

- Keep parser, menu handlers, and delete logic under the complexity budgets defined in `AGENTS.md`:
  - Config parsing: max 20/12 (gocognit/gocyclo).
  - TUI rendering: max 18/12.
  - All other logic: max 15/12.
- Prefer small, composable functions:
  - A `parseOrchestrateInput` function.
  - A `deleteRunByID` helper.
  - A `runSelectorItems` builder.
  - A `confirmDelete` dialog builder.
- Depend on abstractions, not concrete types, where possible (e.g., accept `EventStore` interfaces, not `FileEventStore`).
- All prompt text must come from embedded help files, not hardcoded strings.
- Tool errors must use `internal.ToolError` format where applicable.

## 15. Risks and Open Items

- The existing `orchestrate.long.md` help describes a legacy task-pipeline syntax that does not match the current implementation. It will be fully replaced by this spec.
- Changing run IDs from timestamps to random IDs may affect tests that assert on ID format; those tests must be updated.
- The `tui.Selector` already supports filtering, but the filterable list UX for run pickers must be verified to compose with the main input line (especially for the tab steering feature).
- The active runtime holder is shared between the command and the app. Deleting an active run must coordinate cancellation carefully to avoid goroutine leaks.
- Retention cleanup must be idempotent and safe if multiple Goa instances run against the same project directory (use best-effort directory removal; ignore "not found" errors).

---

*This spec is ready for implementation. Each phase should be committed independently and must pass the static-analysis gate before moving to the next phase.*
