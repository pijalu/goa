<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-07-16

Archived after fixing in the 2026-07-16 session.

## Swarm tool showed no sub-agent activity and appeared to stop the main conversation

**Original:** While running `agent_swarm`, the TUI only showed the tool header
(`🐝 <task> (N items)`) and a `Tool calling` spinner. No sub-agent progress
or conversation was visible, so the user could not follow what the parallel
sub-agents were doing. The main conversation also appeared frozen.

**Root cause:** The swarm tool spawned sub-agents via `AgentPool.CreateTaskAgent`,
which fired the foreground orchestrator's `OnAgentCreated` hook. That hook
wired every sub-agent's output into the companion-renderer event stream,
so swarm sub-agent output was misrouted as companion content (or lost if no
companion section was active). In addition, the swarm tool had no channel to
surface its own sub-agent lifecycle events, so the chat history contained no
record of sub-agent starts, failures, or results.

**Fix:**

- `tools/swarm/agent_swarm.go` now uses `AgentPool.CreateEphemeralAgent` for
  each sub-agent. Ephemeral agents are isolated, do not trigger
  `OnAgentCreated`, and therefore do not leak into the companion renderer.
- Added a `ChatEmitter` interface and `Emitter` field to `AgentSwarmTool`.
  Each sub-agent emits a start message when it begins and a completion/failure
  message with its full output when it finishes. These messages are delivered
  as `event.ChatEvent{InterAgent}` so they appear as agent messages in the chat
  history.
- Added a `ProgressReporter` field and a live `swarmProgress` tracker to
  `AgentSwarmTool`. Each sub-agent status change (pending → running →
  completed/failed) updates the running `agent_swarm` tool widget so the
  user sees a live per-item progress summary inside the tool block itself.
- `internal/app/swarm.go` introduced `swarmEmitter` (forwards chat messages
  to the typed event bus) and `swarmProgressUpdater` (refreshes the running
  `agent_swarm` tool widget on the TUI command loop).
- `internal/app/subsystems.go` wires the `Emitter` when the swarm tool is
  registered; `internal/app/app.go` calls `wireSwarmTool` during `App`
  construction to attach the progress updater once the app exists.

**Tests:**

- `tools/swarm/agent_swarm_emitter_test.go` (new):
  `TestAgentSwarmTool_EmitsSubAgentActivity` verifies that swarm sub-agents
  produce start/completion emits, do not trigger `OnAgentCreated`, report live
  progress snapshots, and still return their output in the XML result.
- `internal/app/swarm_filmstrip_test.go` (new):
  `TestSwarmActivityShowsInChatHistory` uses the filmstrip harness to verify
  that emitted `InterAgent` chat events render as agent messages in the chat
  history, including the start/completion text and sub-agent output.
  `TestSwarmProgressUpdater_UpdatesRunningToolWidget` verifies that the live
  progress updater writes sub-agent status into the running `agent_swarm`
  tool widget.

**Validation:** `go vet ./...` clean; `gocognit -over 15`, `gocyclo -over 12`,
`staticcheck` show only pre-existing warnings unrelated to this change;
`go test -count=1 -race -cover ./...` passes.
