<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Archived: compact summarize idle timeout

Closed 2026-11-27. Moved from `bugs.md`; fixed in the associated commit.

## Observed and expected behavior

After an agent turn stopped, the provider cache could expire while the
conversation was idle. The next user exchange then paid to process the full,
uncompressed context again.

The requested behavior was one explicit `/compact:summarize` pass after a
configurable period of post-stop inactivity, provided projected context
occupancy met a configurable minimum. The summarize request uses the existing
append-only conversation prefix. A successful compaction installs a checkpoint,
rotates cache identity, and lets the next user exchange continue from the
compressed context instead of the original full transcript. This does not probe
or predict provider cache state.

## Configuration and timer contract

- `context_compression.idle_summarize.trigger_percent` is the minimum projected
  context occupancy, default `10`. Explicit `0` permits any nonempty history.
- `context_compression.idle_summarize.delay_seconds` is the inactivity delay,
  default `240` (4 × 60 seconds). Explicit `0` disables idle summarization.
- The countdown starts after turn processing and cleanup have fully stopped.
  Starting another turn, clearing/restoring history, manual compaction, stopping
  the agent, or replacing the session invalidates pending work.
- Threshold and idle state are checked again when the timer fires. Generation
  checks reject stale callbacks.
- At most one idle summarize attempt is made per completed stop. Completion does
  not rearm; a later exchange and stop may arm one again.
- A new exchange cancels an in-flight idle summarize and waits for its provider
  call to unwind before starting, preventing concurrent history replacement.
  Cancellation is not reported as a compaction failure.
- `/compact` aliases `/compress`, so `/compact:summarize` and the idle pass use
  the same explicit summarize strategy. Fresh-window substitution remains
  disabled for this explicit path; remote compaction remains available when its
  own capability and gate allow it.

## Implementation

- Added pointer-backed application settings, embedded defaults, field-wise
  cascade merging, validation including duration overflow, `/config:set`
  setters, interactive Compression → Advanced rows and pickers, and command /
  configuration documentation.
- Added an agent-owned, generation-scoped one-shot scheduler with an injectable
  timer factory. Turns hold an activity read lock; idle compaction uses an
  exclusive non-blocking lock attempt. Manual compaction and history replacement
  use the exclusive lock and invalidate the scheduler before and after acquiring
  it.
- The processing owner arms after cleanup while still holding the turn-activity
  read lock. Queued `Run` callers release their read lock without arming.
  Generation-scoped run cleanup prevents stale post-`Stop` work from rearming a
  timer or clearing a newer run's processing state.
- Idle compaction rechecks processing, nonempty history, effective context
  window, and projected occupancy. Non-cancellation failures append a system
  notification but never install a checkpoint or replace conversation history.
- Successful compaction follows the existing checkpoint and cache-generation
  machinery and invalidates provider-usage anchors.
- Application configuration maps the resolved settings into the SDK, refreshes
  the scheduler on live changes, and disables idle summarization when global or
  per-model compression is disabled. `StopSession` stops the active agent before
  dropping its session reference so an orphaned timer cannot fire.
- Filmstrip validation exposed a separate persistence defect: generic scalar
  inference parsed numeric `0` and `1` as booleans before integers, causing the
  writer to refuse its own YAML. Config persistence now writes the committed
  field's typed value, including nested map and provider/model slice paths, and
  retains string inference only for synthetic paths.

## Test and validation record

RED was observed before implementation for the missing configuration/scheduler
APIs, application mapping, alias/setters/menu, and numeric persistence. The
numeric persistence regression specifically failed with `cannot unmarshal
!!bool 'false' into int` for delay `0` and the equivalent boolean `true` failure
for trigger `1`.

Coverage includes defaults, cascade overrides, explicit zeroes, deep-copy
independence, validation and setters, exact threshold boundaries, exact delay,
reset/stale-callback behavior, one-shot execution, in-flight cancellation,
active-turn and history-operation exclusion, failure reporting, live
configuration, session stopping, and shutdown cleanup.

Two end-to-end paths passed:

- `TestAgentIdleSummarizeEndToEnd` drives real SDK `Run`, provider streams,
  compaction, checkpoint installation, and cache identity with a controlled
  clock. It proves exactly one summarize request, no self-rearm, removal of the
  original full transcript, and a rotated next request containing both the
  summary and new user input.
- The manager-level E2E uses real `StartSession` and a configured one-second
  timer to drive conversation → summarize → next conversation through the
  application wiring.

`TestConfigCompressionIdleSummarize_Filmstrip` drove the production `/config →
Compression → Advanced` selectors and rendered the actual terminal frames. It
showed defaults `10%` and `240s`, both pickers, and updated values `25%` and
`off`, then verified the saved home YAML reloads with those values. The final
run emitted no persistence warning.

Focused race suites for `internal/agentic`, `core`, and `core/commands` passed,
as did both compression filmstrips. Required quality commands were run
separately:

- `go vet ./...` — pass.
- `staticcheck ./...` — pass.
- `gocyclo -over 12 .` — pass.
- `go test -count=1 -race -cover -timeout 180s ./...` — pass in 60.41s.
  Coverage included `config` 84.3%, `core` 75.5%, `internal/agentic` 86.9%, and
  `internal/app` 67.4%.
- `gocognit -over 15 .` — only unrelated pre-existing warnings:
  `TestResolveStartPlanAPIKey` (23),
  `TestAgentMCPCloseRemovesExtractedArtifacts` (23),
  `TestMakeToolFactory_BuildsEveryConfigurableTool` (19),
  `TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig` (16),
  `AgentManager.executeRunner` (16), and `initGoalSystem` (16). No changed
  function was reported after test fixture extraction.

No live LLM, server, browser, or throwaway Goa process was started for the
filmstrip validation, so no process cleanup was required.
