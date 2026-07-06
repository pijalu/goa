<!-- SPDX-License-Identifier: GPL-3.0-or-later
Copyright (C) 2026 Pierre Poissinger -->

# Investigation — `orchestrator-conversation-rendering-fix.md` regressions

Status: **investigation only** (no code changes). Companion to
`docs/plans/orchestrator-conversation-rendering-fix.md`.

> **Implementation update (2026-07-06).** The critical cluster is now fixed
> and tested (full `-race` suite green; `go vet`/`gocyclo`/`gocognit` clean):
> - **R1** (freeze) — async durable sink (`core/orchestrator/durable_sink.go`)
>   takes writes off the streaming hot path; `FileEventStore` now keeps the
>   file open (no open/close per token).
> - **R2 / R3 / R7 / R12** (shared-agent cluster) — `AgentPool.CreateEphemeralAgent`
>   gives each delegation a fresh, isolated worker with its own history and an
>   observer bound to its own handle. `Delegate` returns `h.Message()`
>   (per-handle accumulator replaces the role-keyed buffer that concurrent
>   same-role delegations clobbered).
> - **R8** (tool leak) — `baseToolsForRole` returns only allow-listed tools;
>   ephemeral agents get no `workflows:next` / `send_message`.
> - **R10** (companion renderer leak) — ephemeral agents do NOT fire
>   `OnAgentCreated`, so they no longer inherit the foreground orchestrator's
>   companion observer (the root cause of "companion · cycle" during
>   `/orchestrate`).
> - **R9** — run timeout is now configurable (`orchestrator.defaults.run_timeout`)
>   instead of hard-coded.
> - **R6** — `agentStreams` registry reset is now unconditional.
>
> **Still open** (follow-up): **R4** (per-agent filter tabs), **R5** (live
> fanout still drops; content reconciliation not yet wired), **R11** (the
> diagnostic export still does not bundle the orchestrator run).
>
> **Implementation update 2 (2026-07-06).** R4, R5 and the R11 extension point
> landed (full `-race` suite green, including LMStudio live tests):
> - **R4** — per-agent **filter tabs** restored: each started agent gets a tab
>   that scopes the chat viewport to its blocks via `ChatViewport.SetAgentFilter`
>   (no duplicated widgets). Tabs stay ordered `[Conversation, Stats, <agent>…]`.
> - **R5** — `EventAgentFinished` now carries the authoritative full text;
>   `reconcileAgentContent` snaps each finished worker's content widget to it,
>   repairing any deltas the lossy live fanout dropped.
> - **R11 (SOLID)** — the export bundle is now a generic bundler with an
>   `ArtifactContributor` extension point (`internal/logs/export/contrib.go`);
>   all domain-specific code was removed from it (Open/Closed). The
>   orchestrator contributor registration is the next step.
>
> **Implementation update 3 (2026-07-06).** R11 wiring landed SOLID:
>   `core/commands/orchestrate_export.go` registers an `ArtifactContributor`
>   that bundles the most recent run's `events.jsonl` + a JSON summary built
>   from the domain's `ReplaySnapshot` (no event-JSON parsing in the export
>   package). The contributor is registered via `init()` in `core/commands`
>   (imported by every entry point, so it covers both TUI and headless export).
>   Tests: contributor unit test + an export-level test asserting the generic
>   extension point flows both Data and Path artifacts through `BuildBundle`.

> **Revision note.** The first pass (R1–R7) inferred the crash from the
> concurrency model because the export bundle was stale. After the full crash
> UI was provided, the picture sharpened: the visible "crash" is a **10-minute
> deadline** (R9), driven by R1 + R12, and there are additional bugs R8–R12
> and a confirmed export gap (R11). R3 remains a real latent corruption/panic
> risk and explains the scrambled output, but it is not *the* crash trigger.

## 0. Executive summary

The plan was implemented, the repo builds clean, and its Phase 0 tests pass.
But the implementation is **broken in production** because the tests exercise
synthetic, single-handle scenarios that never touch the real wiring.

| # | Reported symptom | Root cause | Severity |
|---|------------------|------------|----------|
| R1 | "LM Studio freezes" | **Synchronous durable-store write on the token-streaming hot path** (`emit` → `store.Append` per delta). | Critical — starves the stream reader |
| R2 | "2 coders started, only one is used" + "tool-call detection shared by agent" | **Observer bound once to the FIRST `AgentHandle` per role**; later handles are orphaned. One cached `*Agent` per role means all later delegations route to handle #1. | Critical — wrong attribution / silent |
| R3 | scrambled / out-of-order / duplicate output | **Multiple `delegate` calls can run concurrently on the same cached `*Agent`** (parallel tool scheduler + no per-role cap). The agent is not safe for concurrent `Run`. | Critical — latent panic/corruption |
| R4 | "Per agent view is gone" | **Per-agent tabs removed by design** (Phase 3.4). Only `Conversation`/`Stats` remain; no way to isolate one agent's stream. | Regression (plan decision) |
| R5 | "Channel should not be used as a buffered queue" | Live fanout uses non-blocking **drop** sends; the TUI accumulates content from dropped deltas and **never reconciles** with the durable store → permanent gaps. | Correctness |
| R6 | (latent) inverted reset | `attachOrchView` resets `agentStreams` only `if != nil`; works only because it is pre-initialised. | Latent |
| R7 | (latent) cross-run leaks | Observer + delegate-tool are never detached; across `/orchestrate` runs they accumulate / point at dead runtimes. | Latent |
| R8 | `run tool` → "no active workflow run; use /wf:run:<name>" | **Workflow tool leak**: `AgentPool.toolsForRole` injects `WorkflowNextTool` into *every* pool agent whenever a `ForegroundOrchestrator` is attached, so orchestrator/coder agents receive a workflow tool they can never satisfy and the model calls it. | Critical — tooling |
| R9 | the actual "crash" | **10-minute hard timeout** (`orchestrate.go:561` `WithTimeout(..., 10*time.Minute)`) is exceeded because R1 + R12 bloat every token/turn; in-flight `Run`/tool calls return `context.DeadlineExceeded` → orchestrator & coder marked `crashed`. | **The visible crash** |
| R10 | "companion · cycle 1/2" inside an `/orchestrate` run | **Two multi-agent renderers share the chat viewport with no mutual exclusion**: the companion path (`internal/app/orchestrator.go` `AddCompanionCycle`) and the new `/orchestrate` path (`drainOrchView` / `agentStreamRegistry`) both fire. | UX / ownership |
| R11 | the export captured a stale, unrelated session | **Export is blind to orchestrator runs**: `collectArtifacts` bundles only `ctx.SessionStore` (the main interactive session), never `.goa/orchestrator/<run-id>/events.jsonl` nor the orchestrator's crash reason; `/orchestrate` never updates `SessionStore`. | Critical — diagnostics |
| R12 | coder edit-loop storm / deadline | **Cached agent `history` not reset between delegations**: `Delegate` clears only `r.msgs[role]`, not the underlying `*Agent.history`, so each `delegate(coder)` carries all prior delegations' context → growing context × R1 per-token disk cost → deadline (R9). | Critical — correctness/perf |

**You were right about the export:** the bundle at
`/Users/muaddib/goatest2/.goa/exports/goa-export-20260706-204402.zip`
genuinely does not contain the orchestration run. That is not a fluke — it is
**R11**: the diagnostic export predates the orchestrator and only knows about
the main `SessionStore`. See §13.

---

## 1b. Real crash walkthrough (from the provided UI)

The crash UI is the ground truth. Reading it in order, with the bug each line
maps to:

1. `/orchestrate:new:topology=hub happy.falcon` "Create a html containing a
   realistic fire burning simulation…".
2. `[orchestrator] ▶ orchestrator started` — hub drives only the orchestrator
   role; correct.
3. `▾ companion · cycle 1` then `▾ companion · cycle 2` — **R10**: the
   ForegroundOrchestrator companion renderer (`internal/app/orchestrator.go`
   `runOrchestratorEventForwarder` → `AddCompanionCycle`) is active during a
   `/orchestrate` hub run. Two multi-agent systems write to the same chat.
4. `✓ [orchestrator] run tool` returning the **coder's** `index.html` text —
   **R2**: the orchestrator's `delegate(coder)` result is shown, but the
   orchestrator also has the leaked workflow tool, so the renderer labels the
   delegation round as `run tool`, and attribution is muddy.
5. `[coder] ▶ coder started` — sub-task 2 delegated to coder (same cached
   agent as sub-task 1 → **R12**).
6. `✗ [coder] run tool / Error: no active workflow run; use /wf:run:<name>` —
   **R8**: the coder agent has `WorkflowNextTool` and calls it.
7. `[coder] ■ coder ok` — coder turn ends (the workflow-tool error is just a
   failed tool result, not fatal).
8. `✗ [orchestrator] run tool / Error: context deadline exceeded` — **R9**:
   the 10-minute orchestration context expired while the orchestrator's tool
   was in flight.
9. More `[coder]` content and a `pattern_not_found` edit-loop storm appear
   **after** `coder ok` and out of order — **R2/R3**: events from the shared
   cached coder agent (and its stale observer binding) arrive late/interleaved.
10. Loop guardrails fire (`already executed this turn`, `Loop guardrail …
    repeated 3 consecutive times`) — the coder is stuck retrying the same
    failed edit; each retry is a full LLM round whose tokens are paid for at
    R1's per-token disk-write cost.
11. `[coder] ■ coder crashed` then `[orchestrator] ■ orchestrator crashed` —
    both turns return errors (deadline / exhausted). The run emits
    `EventRunFinished{ok:false}` → "finished with errors".
12. The auto-export writes a bundle that contains **the previous interactive
    session** (the README summary), not this run — **R11**.

**So the crash chain is:** R12 (growing context across delegations) × R1
(per-token synchronous disk write) ⇒ each turn is far slower than it should be
⇒ the coder's edit-loop storm (R8 makes it worse by injecting a useless tool)
burns the 10-minute cap (R9) ⇒ `context.DeadlineExceeded` ⇒ both agents
"crash". R3 is not the trigger here but is visible as the scrambled/late
output and remains a live panic risk under `-race`.

---

## 1. Bug R1 — Streaming hot path does synchronous disk I/O (the "freeze")

### Claim
The requirement *"complete processing should never limit the agent's ability
to consume/process tokens — there should be no blocking"* is violated. Every
streamed token that becomes an orchestrator event triggers an open/write/close
of the event-log file, **on the streaming goroutine**, before the next delta
is read. LM Studio is not frozen — goa stops draining its socket, so the
provider's send window backs up and the whole turn stalls.

### Evidence (byte path)

1. Provider deltas are consumed in `consumeStream` and turned into observer
   events synchronously:
   - `internal/agentic/agent_streaming.go` `consumeStream` →
     `handleStreamEvent` → `handleThinkingDelta`/`handleTextDelta`.
   - `internal/agentic/agent_events.go:emitEvent` calls
     `entry.obs.OnEvent(event)` **inline** (same goroutine, no hand-off).

2. The orchestrator observer forwards every delta straight into the runtime:
   - `internal/app/orchestrator_adapter.go` `applyOutputEvent` →
     `applyContent` → `rt.RecordAgentThinking` / `rt.RecordAgentMessage`.

3. Both record methods call `r.emit(...)`:
   - `core/orchestrator/runtime.go` `RecordAgentThinking` / `RecordAgentMessage`.

4. `emit` does the durable write **synchronously, then** fans out:
   ```go
   func (r *Runtime) emit(evt Event) {
       ...
       if r.store != nil {
           _ = r.store.Append(evt)   // BLOCKING, on the streaming goroutine
       }
       r.emitLive(evt)
   }
   ```
   `emitLive` and `fanout` are non-blocking (`select { case ch <- evt: default: }`),
   so they are **not** the problem — but they run *after* the blocking write,
   which makes the buffering theatrical.

5. `FileEventStore.Append` (`core/orchestrator/store.go`) opens, writes, and
   closes the file **on every call**, under a process-wide `sync.Mutex`:
   ```go
   f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
   ...
   f.Write(append(data, '\n'))
   ```
   → `open + write + close + mutex` **per token**, serialized across all agents.

### Why it is "the freeze"
Thinking deltas arrive at high frequency (one per token). For a long turn that
is thousands of synchronous file opens on the goroutine that is supposed to be
reading the HTTP body. The reader falls behind, the provider's kernel send
buffer fills, TCP backpressure stalls the stream. Combined with R12 (growing
context ⇒ more tokens per turn ⇒ more writes), this is what pushes the run past
the 10-minute cap (R9). Observed externally as "LM Studio froze".

### Note on the plan
The plan's own risk note only addresses the *bus* ("drops when full,
non-blocking … acceptable"). It never considered that the **store append is
blocking and happens before the non-blocking bus send**. The non-blocking bus
is therefore useless for the hot path.

---

## 2. Bug R2 — Observer bound to the first handle; "only one coder is used"

### Claim
`multiagent.AgentPool.GetOrCreate(role)` caches **one** `*agentic.Agent` per
role. The adapter attaches its output observer **exactly once per role**
(`a.seen`) and that observer closure captures the **first** `*AgentHandle`.
Every subsequent `Acquire(role)` builds a brand-new handle whose turn streams
into the **first** handle's stats/event stream. The later handles are
"started" (their own `EventAgentStarted` fires) but receive none of their own
streaming/tool events — exactly *"2 coders started, only one is used"* and
*"tool-call detection is shared by agent"*.

### Evidence
- `internal/app/orchestrator_adapter.go` factory:
  ```go
  agent, err := a.pool.GetOrCreate(role)          // cached per role
  ...
  h := orchestrator.NewAgentHandle("", role, model) // NEW handle each Acquire
  ...
  _, already := a.seen[role]
  if !already { a.seen[role] = struct{}{} }
  ...
  if !already {
      agent.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
          applyOutputEvent(h, rt, ev)   // captures THIS h (the first one)
      }))
  }
  return h, nil
  ```
  `h` is captured by pointer at attach time; there is no indirection (no
  "current handle for role" slot), so it is permanently the first handle.

- `core/orchestrator/pool.go` `Acquire` calls `p.factory(role, model)` **every
  time** and stamps a fresh `h.ID = "<role>-<n>"`. So the 2nd `delegate(coder)`
  produces `coder-2`, but the agent's observer still routes to `coder-1`.

- `multiagent/agent_pool.go:162` `GetOrCreate` returns the cached agent for a
  role on the 2nd+ call (`if a, ok := p.agents[role]; ok { return a, nil }`).

- Attribution flows through `applyToolCall`/`applyContent`, which take `h` and
  call `h.Stats.IncToolCall()` / `rt.RecordAgentToolCall(h, ...)`. Because `h`
  is always handle #1, **coder-2's tool calls are recorded against coder-1**,
  and `RecordAgentToolCall` emits `EventAgentToolCall` with `AgentID =
  coder-1`. That is the "tool-call detection shared by agent" symptom.

### Why the tests missed it
`internal/app/orchestrator_adapter_events_test.go` constructs a **single**
hard-coded handle `h := orchestrator.NewAgentHandle("h-1", ...)` and calls
`applyOutputEvent(h, rt, ev)` directly. It never builds a `Runtime` through
the factory, never calls `Acquire` twice, and never touches
`multiagent.AgentPool`. The stale-binding bug is therefore untested.

### Plan assumption that was wrong
Risk note in the plan: *"Observer dedupe … attaches the observer once per
(process, role). The new event forwarding happens inside that same observer,
so no extra dedupe is needed."* This assumes **one handle per role**, but the
bounded pool issues a **new handle per `Acquire`**. The assumption is false for
the hub topology, where `delegate` re-acquires the same role repeatedly.

---

## 3. Bug R3 — Concurrent `delegate` to the same role corrupts the shared agent

### Claim
When the orchestrator emits **two** `delegate(role=coder, …)` tool calls in one
turn (normal model behaviour), they are executed **in parallel goroutines** by
the tool scheduler. Both call `Runtime.Delegate(ctx, "coder", …)` →
`Acquire("coder")` → the **same cached `*Agent`** → two concurrent
`agent.Run` on one agent. `agentic.Agent` is a single-driver state machine
(one conversation, one stream buffer, one turn state); concurrent turns corrupt
it and can panic (concurrent map/slice use, torn turn state). This is the most
likely source of an actual **panic** (as opposed to the R9 deadline "crash"),
and it explains the scrambled/duplicate blocks in the UI.

### Evidence
- Tool calls are dispatched concurrently:
  - `internal/agentic/agent_budget.go:300` `executeBufferedToolCalls` →
    `scheduleAndRunToolCalls`.
  - `internal/agentic/agent_tools.go:67` `scheduleAndRunToolCalls` adds every
    call to a `ToolScheduler` and `Collect()`s.
  - `internal/agentic/tool_scheduler.go` `Add` → `start`, and `start` runs
    each task in `go func() { … }()`. Non-conflicting calls run in parallel
    (`delegate` declares no file/path access, so it is never "blocked").

- `Runtime.Delegate` (`core/orchestrator/runtime.go`) has **no per-role lock**;
  it only goes through `pool.Acquire`, which blocks solely on
  `MaxTotalAgents` / `MaxAgentsPerModel`.

- Default config `config/configs/default.yaml:73-79`:
  ```yaml
  orchestrator:
    pool:
      max_total_agents: 8
      max_agents_per_model: {}   # empty = unlimited per model
  ```
  With an empty per-model cap, two `Acquire("coder")` calls **both succeed
  immediately** → two concurrent turns on the one cached agent.

- The cached agent is shared by all handles of a role
  (`multiagent/agent_pool.go` `GetOrCreate`). `agentic.Agent` keeps turn-level
  mutable state (`history`, `contentBuf`, `thinkingBuf`, `bufferedToolCalls`,
  `providerUsage`, the `transitionTo` state machine) guarded for *single-driver*
  access, not for two overlapping turns.

### Important nuance vs. the actual crash
In the provided UI the visible "crash" is the R9 deadline, **not** an R3 panic.
But R3 is what makes the output read out of order (coder content appearing
after `coder ok`, duplicate blocks), and it is a live `go test -race` violation
waiting to happen. It must be fixed regardless.

### Why the tests missed it
No test runs two `Delegate`/`Acquire` for the same role, and no test runs the
agent under the real scheduler. `core/orchestrator` has `[no tests to run]`
for these names.

---

## 4. Bug R4 — Per-agent view removed ("Per agent view is gone")

### Claim
This is **not an accidental removal** — Phase 3.4 of the plan deliberately
deleted `TabAll` and the per-agent `TabAgent` transcript tabs and moved all
conversation into the unified chat viewport. The result: with N agents
streaming concurrently there is **no way to view one agent's stream in
isolation**; everything is interleaved in the chat (and, per R10, interleaved
with the companion renderer too). The user considers this a lost key feature.

### Evidence
- `tui/orchestrator/view.go` `ensureBookendTabs` builds only:
  ```go
  v.tabs = []AgentTab{
      {Key: "conversation", Label: "Conversation", Kind: TabConversation},
      {Key: "stats",        Label: "Stats",        Kind: TabStats},
  }
  ```
  `handleAgentStarted` appends to `v.order` but **never creates a tab** for the
  agent. `ActiveAgentID()` returns the last-started agent id but only for
  steering — there is no per-agent render path.

- `internal/app/orchestrator_view_forwarder.go` `handleOrchViewEvent` routes
  *all* `EvAgentThinking/Message/ToolCall/ToolResult` to the shared chat via
  `agentStreamRegistry`; nothing is partitioned per agent.

- Dead-but-present code: `AgentLog`, `LogFor`, `appendLine`, `AgentLogLine`
  (`tui/orchestrator/view.go`) still exist and are fed by `EvAgentSteered` /
  `EvAgentFinished`, but nothing renders them into a tab — the transcript they
  collect is unreachable from the UI.

### Design tension
The plan's "huge correct implementation" argued for unifying everything in the
chat. That satisfies "rendered as other chat" but throws away the multi-agent
isolation that was the point of the tabbed view. The two are not mutually
exclusive: the chat can stay the default, with **per-agent filter tabs** that
scope the same `agentStreamRegistry` widgets to one agent. The plan rejected
this without weighing the isolation requirement.

---

## 5. Bug R5 — Buffered channels used as lossy queues; content gaps

### Claim
"A channel should not be used as a buffered queue." Here the runtime bus
(`make(chan Event, 256)`) and each subscriber (`make(chan Event, 64)`) are used
exactly as lossy buffers: sends are `select { case ch <- evt: default: }`, so
any event that does not fit is **silently dropped**. The TUI builds its content
**only** from the deltas it receives, so a dropped content delta is a
**permanent hole** in the rendered message — directly contradicting the plan's
claim that "the store is the source of truth".

### Evidence
- `core/orchestrator/runtime.go`:
  - `bus: make(chan Event, 256)`, `Subscribe()` → `make(chan Event, 64)`.
  - `emitLive` → `select { case r.bus <- evt: default: }`; `fanout` → same
    non-blocking send per subscriber.

- TUI accumulation has **no reconciliation** with the store:
  - `internal/app/agent_streams.go` `handleAgentContent` does
    `state.content.WriteString(text)` per received delta and renders
    `state.content.String()`. No code path ever re-reads the durable
    `EventAgentMessage` log to fill gaps. So "store is source of truth" is
    true only on disk, never on screen.

- Compounding factor: thinking deltas are the highest-frequency events and the
  most numerous drops; combined with R1 (slow store writes delaying the
  forwarder's `a.apply` drain), the subscriber can fall behind the 64-buffer
  and start dropping **content** too.

### Why "buffered channel as queue" is the wrong abstraction here
A bounded channel with `default:` drop hides backpressure: the producer never
learns it is losing data, and the consumer cannot tell which events were
dropped. For a durable event log the correct shape is a **decoupled async
writer** (the producer enqueues to a slab/queue that cannot drop durable
events; a single writer goroutine flushes to disk) **plus** a separate,
explicitly-lossy live channel for UI ticks whose contract is "best-effort".

---

## 6. Bug R6 — Inverted registry reset (latent)

`internal/app/orchestrator_view_forwarder.go` `attachOrchView`:
```go
if a.subs.agentStreams != nil {
    a.subs.agentStreams = newAgentStreamRegistry()
}
```
The intent is "fresh registry per run", but the condition is inverted. It only
"works" because `agentStreams` is pre-created at subsystem assembly
(`internal/app/subsystems.go:840`). If that pre-init ever changes (or a code
path reaches `attachOrchView` with a nil registry), **nothing in the chat would
ever render** and every `handleAgent*` would silently early-return. Should be
unconditional:
```go
a.subs.agentStreams = newAgentStreamRegistry()
```

---

## 7. Bug R7 — Cross-run leaks (observer + delegate tool never detached)

- The observer attached in the factory captures `rt` and `h` and is **never
  removed** (`a.seen` only prevents re-adding). Across multiple `/orchestrate`
  runs in one process, the cached orchestrator/coder agents keep firing into
  the **first** run's `rt`/`h`. `emitLive` guards `r.closed`, so closed-bus
  sends are skipped — but stats still mutate the **stale** `h.Stats`, and any
  later `Runtime` built for the same role gets an agent whose observer points
  at a dead runtime. This is the cross-run face of R2.

- The orchestrator branch re-appends `OrchestratorDelegateTool` on **every**
  factory call:
  ```go
  cur := append([]agentic.Tool{}, agent.Tools()...)
  cur = append(cur, &OrchestratorDelegateTool{Runtime: rt, Roles: roles})
  agent.SetTools(cur)
  ```
  Unlike the observer, this has **no `a.seen`-style dedupe**, so each run adds
  another `delegate` entry (and another closure over a different `rt`). The
  model sees duplicate `delegate` tools and may call a stale-runtime copy.

The plan's own comment admits multiagent "does not expose observer removal"
and waves it off with "prefer `CreateTaskAgent`" — but the production wiring
uses `GetOrCreate`, so the leak is live, not hypothetical.

---

## 8. Bug R8 — Workflow tool leaks into orchestrator/coder agents

### Claim
The UI shows `[coder] run tool / Error: no active workflow run; use /wf:run:<name>`
and `[orchestrator] run tool`. The error string is produced **only** by
`multiagent/workflow_tool.go:47` (`WorkflowNextTool.Execute`). So orchestrator
and coder agents have the workflow-advancement tool in their toolset, the model
calls it (it looks plausibly useful mid-task), and it always fails because no
workflow run is active.

### Evidence
`multiagent/agent_pool.go` `toolsForRole`:
```go
// Add WorkflowNextTool so stage agents can advance. The tool validates
// that actual work was done before allowing advancement — see Execute().
if p.orch != nil {
    result = append(result, &WorkflowNextTool{
        Orchestrator: p.orch,
    })
}
```
This is added to **every** agent the pool builds whenever a
`ForegroundOrchestrator` (`p.orch`) is attached — which is the normal app
wiring. The orchestrator adapter builds its agents via `a.pool.GetOrCreate`,
so they inherit the workflow tool. The adapter only *adds* `delegate` for the
orchestrator role (`agent.SetTools(append(agent.Tools(), delegate))`); it never
**filters out** the workflow tool for any role.

### Impact
- Wastes turns/tokens on a tool that can never succeed (feeds R9).
- Confuses the model (it has both `delegate` and `workflows:next`, plus the
  edit tools), increasing loop/stall behaviour.
- Pollutes the rendered transcript with `run tool` errors.

### Fix direction
Orchestrator-run agents must be built from an **explicit allow-list** (the
adapter already has `rcfg.AllowedTools`). `WorkflowNextTool` (and
`SendMessageTool`) should be opt-in per role, not unconditionally injected
whenever a foreground orchestrator exists anywhere in the process.

---

## 9. Bug R9 — 10-minute hard timeout is the actual "crash"

### Claim
The orchestrator run is launched with a fixed 10-minute context:
```go
// core/commands/orchestrate.go launch()
runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
```
When it expires, every in-flight `agent.Run` / tool call returns
`context.DeadlineExceeded`, both agents' turns return errors, and the runtime
marks them `crashed` (`core/orchestrator/runtime.go` `driveOne`/`Delegate` set
`AgentCrashed` on `runErr != nil`). That is exactly the terminal UI:
`[orchestrator] run tool / Error: context deadline exceeded` →
`■ orchestrator crashed`.

### Why 10 minutes is reachable here
It is not a model-speed problem in isolation; it is the product of the other
bugs:
- **R1** makes every token pay an open/write/close + mutex.
- **R12** makes each successive `delegate(coder)` carry the full prior
  conversation (no history reset), so token counts balloon.
- **R8** injectates a guaranteed-to-fail tool that the model retries.
- The coder's `pattern_not_found` edit-loop storm then runs many LLM rounds,
  each amplified by R1.

Net: a task that should take well under a minute blows past 10 minutes.

### Fix direction
The timeout should be **configurable** and the default generous only relative
to a *non-blocking* pipeline. Once R1/R12 are fixed, 10 minutes is plenty; but
hard-coding it is itself a smell — it should derive from config and from any
bound goal's token/time budget, and a deadline should produce a clear
"timed out" outcome, not a generic `crashed`.

---

## 10. Bug R10 — Companion renderer double-fires during `/orchestrate`

### Claim
The crash UI shows `▾ companion · cycle 1` and `▾ companion · cycle 2`
**inside** a `/orchestrate [hub]` run. That label is produced **only** by the
ForegroundOrchestrator companion path:
- `internal/app/orchestrator.go` `runOrchestratorEventForwarder` reads
  `a.subs.foregroundOrch.Events()` and calls `handleOrchestratorStreamMsg` →
  `ensureCompanionSection` → `a.subs.chat.AddCompanionCycle(cycle)`
  (`tui/chat_viewport_components.go:295` `companion · cycle %d`).

So **two independent multi-agent renderers are writing to the same chat
viewport at the same time**: the companion renderer (above) and the new
`/orchestrate` renderer (`drainOrchView` → `agentStreamRegistry` →
`AddAgentThinkingBlock`/`AddAgentContent`). There is no mutual exclusion or
ownership arbitration between them.

### Evidence
- Companion cycle label: `tui/chat_viewport_components.go:295`.
- Companion driver: `internal/app/orchestrator.go:17-135` (reads
  `foregroundOrch.Events()`, builds companion sections).
- `/orchestrate` driver: `internal/app/orchestrator_view_forwarder.go`
  `drainOrchView` + `internal/app/agent_streams.go`.
- Both append into the same `*ChatViewport`.

### Why it happens
The plan's risk note assumed the two systems were cleanly separated ("leave
`ForegroundOrchestrator` alone"). They share (a) the `AgentPool` (so the
companion's `p.orch` is set, which is exactly what triggers R8) and (b) the
chat viewport. If a companion/multi-agent session is active (or its event
forwarder is simply running), its cycles render alongside `/orchestrate`.

### Impact
Interleaved, confusing output; the user cannot tell which system produced
which block; undermines the "conversation renders cleanly" goal of the plan.

### Fix direction
Make chat-viewport ownership explicit: only one multi-agent renderer may be
active at a time, or give each its own labelled region/stream. At minimum,
`/orchestrate` starting should quiesce/detach the companion forwarder for the
duration of the run.

---

## 11. Bug R11 — The diagnostic export is blind to orchestrator runs

### Claim (you called this one)
The bundle written at crash time contained a **completely unrelated**
single-agent "Lost spinner" session, not the orchestration run. This is a real
gap, not a stale-file fluke: the export only knows about the main
`SessionStore`, and `/orchestrate` never writes there.

### Evidence
`internal/logs/export/bundle.go` `collectArtifacts` bundles:
- `session/events.jsonl` ← `resolveSessionPath` → `ctx.SessionStore.CurrentSessionPath()`
- `logs/{goa,keys,agent}.log`
- `config/*.yaml`, `prompts/mode/`
- `logs/http.jsonl` ← `transport.GlobalHTTPLog.Snapshot()`
- `diagnostics/trace.json` ← derived from those HTTP entries
- `session.md` ← `RenderSessionMarkdown(ctx)` (also `SessionStore`-based)

It **never** includes `.goa/orchestrator/<run-id>/events.jsonl`
(`core/orchestrator/store.go` `FileEventStore`), which is where the entire
orchestration run — including the `EventAgentFinished{outcome:"crashed"}` and
any error payloads — actually lives.

`/orchestrate` does not update `SessionStore` either: a grep of
`core/commands/orchestrate.go`, `internal/app/orchestrator_adapter.go`, and
`core/orchestrator/runtime.go` shows **zero** references to `SessionStore`. So
at crash time `ctx.SessionStore` still holds the previous interactive session
(the README summary), and the export faithfully dumps that.

### Consequence
You cannot diagnose an orchestrator crash from its own export. The HTTP log
may still contain sub-agent LLM calls (it is global), but the run's structured
event log, the delegation chain, the per-agent stats, the crash reason, and the
run id are all absent. (The `manifest.json`/`README.md` timestamps in the
provided zip being a day older than the filename is consistent with the bundle
having been assembled around a stale `SessionStore` snapshot rather than the
live orchestrator run; either way the content is wrong for the purpose.)

### Fix direction (the "export rework to support the new feature")
- Add the orchestrator run to `collectArtifacts`: when an active/recent
  `*orchestrator.Runtime` exists, bundle its event log (`.goa/orchestrator/<runID>/events.jsonl`),
  a `diagnostics/orchestrator.json` summary (topology, roles, per-agent stats,
  terminal outcomes, the **crash error**), and the run id/name in the manifest.
- Make `session.md`/`trace.json` orchestrator-aware (or add a parallel
  `orchestrator.md`) so the delegation chain and the failing agent/tool are
  visible.
- Ensure the export is triggered with the **crashed run** as context, not just
  `ctx.SessionStore`.

---

## 12. Bug R12 — Cached agent history is not reset between delegations

### Claim
`Runtime.Delegate` reuses the one cached `*Agent` for the role across every
delegation, but it resets **only** the runtime's message accumulator:
```go
// core/orchestrator/runtime.go Delegate()
r.msgMu.Lock()
r.msgs[role] = nil
r.msgMu.Unlock()
h.Stats.IncTurn()
runErr := h.RunTurn(ctx, task)
```
`h.RunTurn` → `agent.Run` **appends** to the agent's own `history`
(`internal/agentic/agent.go` only sets `a.history = nil` inside a manual
reset/import API, never on the delegation path). So `delegate(coder,
sub-task-2)` runs with `sub-task-1`'s entire conversation still in context.

### Impact
- Context grows monotonically across delegations ⇒ more tokens per turn ⇒ R1's
  per-token disk cost multiplies ⇒ the 10-minute cap (R9) becomes reachable.
- The coder "remembers" prior sub-tasks' failed edits, reinforcing the
  `pattern_not_found` loop storm seen in the UI.
- Violates the hub model's expectation that each delegation is a focused,
  somewhat-isolated task.

### Fix direction
Either (a) give each delegation a **fresh** agent (`CreateTaskAgent` with a
unique role/id — this also kills R2, R3, R7), or (b) add an explicit
per-delegation history reset to the `Delegate` path. Option (a) is strongly
preferred because it removes the shared-state class of bugs entirely.

---

## 13. Why the gate passed despite all of this

- `go build ./...` → green.
- Phase 0 tests pass (`OrchestratorAdapterEvents`, `OrchestratorConversation`,
  `OrchestratorHubRender`, `ChatViewportAgentStream`). They all:
  - build events/handles **directly** (no `AgentPool`, no `Acquire`, no real
    `Runtime` factory), and
  - drive **single** handles / single sequential streams.
- Nothing in the suite exercises: two `Acquire`s of the same role, the real
  `ToolScheduler` running `delegate` in parallel, the durable store on the
  streaming path, the live fanout under load, the workflow-tool injection, the
  10-minute deadline, or the export. So R1–R3, R5, R8, R9, R11, R12 are
  structurally untestable by the current tests.

---

## 14. Reproduction plan (to confirm before fixing)

1. **R1 (freeze)** — instrument `FileEventStore.Append` with a duration log;
   run `/orchestrate:new:topology=hub,…` against LM Studio and observe
   per-token open/write/close latency on the streaming goroutine. Confirm the
   HTTP body reader stalls.
2. **R2 (stale handle)** — test that calls the real
   `OrchestratorAdapter.NewRuntime` factory, `Acquire("coder")` twice, and
   asserts each handle receives only its **own** observer events (today: only
   the first does).
3. **R3 (race)** — run the hub with a fake provider that makes the
   orchestrator emit two `delegate(coder)` calls; run under
   `go test -race`/`go run -race` and look for concurrent access on
   `Agent.history` / `contentBuf` / `bufferedToolCalls`.
4. **R8 (tool leak)** — build an orchestrator agent via the adapter and assert
   its tool schemas contain **no** `workflows:next` (today: it does).
5. **R9 (deadline)** — run the fire-sim objective with an instrumented clock
   and confirm the run aborts with `context.DeadlineExceeded` at 10 min and
   marks both agents `crashed`.
6. **R11 (export)** — trigger the export after a crashed `/orchestrate` run
   and assert the bundle contains `.goa/orchestrator/<runID>/events.jsonl` and
   the crash outcome (today: absent).
7. **R12 (history)** — call `Delegate(role, t1)` then `Delegate(role, t2)`
   with a fake agent that records its `history` length; assert t2 starts from
   a reset history (today: it carries t1).

---

## 15. Fix priority (suggested)

1. **R1** (async, non-dropping durable writer) — unblocks everything; removes
   the freeze and most of the deadline pressure.
2. **R12 + R2 + R3 + R7 together** — switch orchestrator agents from cached
   `GetOrCreate` to **fresh agent per delegation** (`CreateTaskAgent` with a
   unique role/id) + per-agent turn mutex. One change kills four bugs.
3. **R8** — allow-list tools for orchestrator-run agents; drop the unconditional
   `WorkflowNextTool` injection.
4. **R9** — make the timeout configurable and report "timed out" distinctly.
5. **R10** — establish chat-viewport ownership between the two renderers.
6. **R11** — extend the export to bundle the orchestrator run + crash reason.
7. **R4** — restore per-agent tabs as filters over `agentStreamRegistry`.
8. **R5** — split lossless-durable from lossy-live; add store reconciliation.
9. **R6** — one-line unconditional reset.

---

## 16. Mapping back to `bugs.md` workflow

Per `bugs.md`, each of R1–R12 should become its own bug entry with a RED repro
(test or `-race` run) before any fix. The single most important first step is a
**`-race` repro of a multi-`delegate` hub run** (R3) plus an **instrumented
repro of the R9 deadline** (R1+R12): together they pin the user-visible
"crash" and the silent corruption, and they justify the "fresh agent per
delegation" redesign that resolves the largest cluster of bugs at once.
