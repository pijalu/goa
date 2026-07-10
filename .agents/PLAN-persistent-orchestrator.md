# Spec: Persistent Orchestrator as User↔Solution Link

## Problem Statement

The current hub topology treats the orchestrator as a **two-turn planner**:
1. Planning turn: orchestrator delegates to specialists.
2. Synthesis turn: orchestrator summarizes specialist outputs.

This is not how the user wants to work. The orchestrator should be a **persistent, interactive manager** that:
- Is the single point of contact between the user and the solution.
- Drives specialists in a loop: delegate → review → request rework → re-delegate.
- Asks the user clarifying questions when needed.
- Continues until the solution is done, not until a fixed number of turns is exhausted.

## Goals

1. **Remove the fixed "turn" notion** for the orchestrator. The orchestrator is a long-running conversation that can span multiple interactions with both the user and specialists.
2. **Orchestrator as main contact**: the orchestrator is the moderator, but the user can see the full conversation between the orchestrator and the specialists.
3. **Back-and-forth with specialists**: the orchestrator can inspect a specialist's result and ask for clarification or rework before finalizing.
4. **Back-and-forth with the user**: the orchestrator can ask the user questions and wait for answers before continuing.
5. **No hard-coded workflow**: the orchestrator decides when to delegate, when to ask, and when to finish, based on its own reasoning.
6. **Transparency**: the user sees everything. Specialist outputs, orchestrator questions, and rework requests are all visible in the event stream / TUI, not hidden behind a private summary.

## Non-Goals (for this iteration)

- Adding new UI panels; reuse existing chat/steering/event mechanisms.
- Multi-user sessions or persistence across application restarts (beyond existing session store).

## Design

### Core Abstraction: Orchestrator Loop

Replace the fixed `runHub` (plan + synthesize) with a **loop** inside the runtime. Each loop iteration is one **orchestrator turn**.

```
User objective → Orchestrator turn → decide action
   ├─ ask_user(question) → end turn, pause run, wait for user answer → loop
   ├─ delegate_to(role, task) → end turn, start specialist async → wait → loop
   ├─ rework(role, feedback) → end turn, start specialist async → wait → loop
   └─ final answer / no tool → finish
```

Key rule: **after delegating to sub-agents, the orchestrator's turn ends immediately.** The `delegate_to` and `rework` tools return a placeholder so the orchestrator does not block. The runtime runs the specialists in the background, waits for them to finish, and then starts a new orchestrator turn with their results.

The orchestrator's output and every specialist output are visible in the event stream.

### Reuse Existing Multi-Agent

The core orchestrator already uses `multiagent.AgentPool` through `internal/app/orchestrator_adapter.go`. The new design extends this reuse:

- The **orchestrator role** is the main agent that can call multi-agent-style tools.
- Sub-agents are created from the same `multiagent.AgentPool` (reuse agent creation, model resolution, tool wiring).
- The `delegate_to` / `request_review` patterns from `multiagent/agent_driven_tools.go` are adapted for the orchestrator agent's tool set, with a dedicated `Enabled=true` instance (not gated by `/agent-driven:on`).
- The orchestrator's `delegate_to` and `rework` tools are **async**: they return a placeholder immediately and start the specialist in a background goroutine tied to the runtime's run context.
- The `ForegroundOrchestrator`'s `SteeringQueue` and `OutputHandler` abstractions are reused where possible.

### Tools

The orchestrator uses tools to express intent. Existing tools are extended; new tools are added.

1. **`delegate_to(role, task)`** — adapted from `multiagent.DelegateTool`. Starts the specialist asynchronously and returns a placeholder immediately so the orchestrator's turn can end. The specialist runs in the background and its output is visible in the event stream. When all pending specialists finish, the loop starts a new orchestrator turn with their results.
2. **`rework(role, feedback)`** — new tool. Starts a specialist revision asynchronously and returns a placeholder immediately. Reuses the same specialist agent (or `new_agent=true`).
3. **`ask_user(question)`** — new tool. Records the question, ends the orchestrator's turn, and pauses the loop. The runtime emits an event and waits for the user to answer via `SteerOrchestrator`.
4. **Final answer** — no tool required. If the orchestrator outputs text without calling any tool, the loop terminates and the text is shown to the user.

### Runtime State Machine

`Runtime` gains a `conversationLoop` state:
- `loopActive`: set when the loop is running.
- `pendingUser`: set when the orchestrator asked the user a question; the loop is paused.
- `pendingDelegates`: async specialist delegations currently in flight (for parallel delegation).
- `resumeCh`: channel signaled when new user input or steering arrives.

### Pause / Resume

When the orchestrator calls `ask_user`:
- The tool records the question and sets `pendingUser = true`.
- The loop blocks on `resumeCh`.
- `SteerOrchestrator` appends the user's answer to the orchestrator's steering queue and signals `resumeCh`.
- The loop continues; the next orchestrator turn sees the answer in its steering queue.

The app layer can surface the question to the user (via chat event) and must route the user's next message to `SteerOrchestrator` while the loop is active.

### Specialist Results as Steering

When a specialist finishes:
- Its result is stored in the runtime's `lastByRole` map (already exists) and emitted as events so the user can see it.
- The loop builds a "results so far" block.
- The next orchestrator turn is driven with this block appended to the prompt (or via the steering queue, if the agent supports it).

We prefer the **steering queue** because it is the existing mechanism for injecting follow-up messages into an agent's turn. The loop drains the orchestrator's steering queue before each turn, and it can append specialist results there.

### Prompt Updates

- `hub_orchestrator.md`: replace the plan/synthesis instructions with a persistent-manager persona. The orchestrator should know it can:
  - ask the user for clarification,
  - delegate to specialists,
  - request rework,
  - provide final answer when done.
- Remove or repurpose `hub_synthesis.md`. The synthesis is now just one of the loop's terminal turns, not a separate prompt.

### Configuration

No new config required; reuse existing `orchestrator` topology. We may add loop limits later (max iterations, max user questions) but not in this iteration.

### Events

- `EventAgentStarted` / `EventAgentFinished` / `EventAgentStats` for specialist runs (already emitted).
- `EventAgentMessage` from the orchestrator and specialists is visible in the event stream (already emitted by the adapter).
- New event type: `EventAskUser` with the question payload, so the app can render it as a chat message.
- New event type or payload field: `EventLoopState` (active, paused_for_user, finished) so the TUI knows the orchestrator is waiting.

### Edge Cases

1. **Orchestrator delegates but never finishes**: loop runs until a terminal condition. No explicit max-iterations guard; normal stop/loop detection is enough.
2. **Specialist crashes**: result is empty; the orchestrator receives the error and can decide to retry or ask the user.
3. **User never answers**: the loop remains paused; the run context's timeout still applies (activity-based + absolute max).
4. **Multiple ask_user calls in one turn**: take the last one, or concatenate.
5. **ask_user and delegate in the same turn**: ask_user takes precedence; pause before running specialists.
6. **No roles configured**: fall back to fanout (existing behavior).
7. **Orchestrator asks a question on first turn**: the run pauses and waits for user input.
8. **ask_user tool only for orchestrator**: specialists do not get this tool; they ask the orchestrator, who asks the user.

## Implementation Plan (completed)

1. **Add new event types** (`EventAskUser`, `EventLoopState`) in `core/orchestrator/store.go`.
2. **Implement orchestrator loop** in `core/orchestrator/runtime.go`:
   - Replace `runHub` with `runOrchestratorLoop`.
   - Add pause/resume state (`loopActive`, `pendingUser`, `resumeCh`, `orchSteer`).
   - Add `WaitForUserAnswer`, `AskUser`, `ReworkAsync`, `SetLastAction`, `buildSpecialistResultsPrompt`.
   - Feed specialist results back as the next prompt; drain buffered user steering into the orchestrator handle at turn start.
3. **Add new tools** (`ask_user`, `rework`) and update `delegate` tool description in `internal/app/orchestrator_adapter.go`.
4. **Update prompt** `prompts/orchestrate/hub_orchestrator.md` as persistent manager.
5. **Forward events** in `core/commands/orchestrate.go` for `EventAgentMessage`, `EventAskUser`, and `EventLoopState`.
6. **No submithandler change required**: existing `maybeSteerOrchestrator` routes user input to the active orchestrator runtime; `SteerOrchestrator` now buffers the answer and signals the resume channel.
7. **Tests**:
   - `TestRuntime_DelegateAsync_ReturnsImmediately`
   - `TestRuntime_WaitForDelegations_ResumesAfterPending`
   - `TestRuntime_HubConversationStyleRunsSynthesisEvenIfOrchestratorSpoke`
   - `TestRuntime_HubLoop_PauseForUserAnswer` (new)
   - `TestRuntime_HubLoop_Rework` (new)
   - Updated `TestRuntime_HubLoopsSpecialistOutputs` and live hub tests.