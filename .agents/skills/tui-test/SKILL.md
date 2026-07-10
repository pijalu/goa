---
name: tui-test
description: Test Goa's TUI behavior without a real terminal by driving an agentic event sequence through the app layer and inspecting a Filmstrip of structured, ANSI-free UI states. Use when debugging or regressing anything in the status spinner, tool widgets, chat viewport, streaming/stream-state wiring, footer activity, or any event→UI behavior. Do NOT attempt to assert on raw ANSI/escape sequences or spin up a live model.
---

# Testing TUI behavior without a terminal

The TUI is fully testable as **data**. Never debug a UI bug by running goa
against a live model in a real terminal, and never assert on escape sequences.
Both are blind alleys that have cost multi-hour agent loops.

Instead, drive an `agentic.OutputEvent` sequence through the real app event
handler and inspect the resulting UI as a structured `Filmstrip`.

## When to Use

- Status spinner / activity indicator lifecycle (e.g. "spinner disappears")
- Tool widget rendering or running/success/error state
- Chat viewport content, streaming, thinking blocks, scrollback
- Footer activity/model-busy indicators
- Any change to `internal/app/stats.go` event→status/streaming wiring
- Any change to `tui/` components that respond to agent events
- Regressions in `internal/agentic` event ordering (e.g. mid-turn `EventEnd`)

## The two layers

1. **`tui.AgentFrame`** (`engine.AgentFrame()`) — one frame: terminal size,
   z-ordered layers, a widget DOM (`AgentNode`), the cursor, and the visible
   viewport as plain text. `AgentFrame.Dump()` for readable failure output.

2. **`tui.Filmstrip`** — the *series* of `AgentFrame`s as the UI evolves, each
   with a compact `FrameDiff` (added/removed visible lines, status text,
   cursor movement). `Filmstrip.StatusTrace()` = the spinner lifecycle as a
   `[]string` across all steps — the primary artifact for activity assertions.
   `Filmstrip.Render()` = ANSI-free transcript an agent can read directly.

## The harness: `uiScenario` (`internal/app/ui_scenario_test.go`)

Wires the **full production component tree** to a fake terminal and feeds real
events through `App.handleAgentOutputEvent`, recording a Filmstrip snapshot
per event. Everything runs on the engine command loop (`ApplySync`) to match
production actor-model semantics.

### Minimal example

```go
func TestMyUIBehavior(t *testing.T) {
    sc := newUIScenario(t, 100, 24) // width, height

    sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
    sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
        ToolName: "read", ToolInput: `{"path":"x"}`, ToolCallID: "c1"})
    sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
        ToolName: "read", ToolCallID: "c1", Text: "..."})
    sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

    // Activity lifecycle as data:
    trace := sc.filmstrip().StatusTrace()
    // Current widget DOM / visible text:
    frame := sc.engine.AgentFrame()
    _ = frame.FindNode("Editor")
}
```

### Asserting the spinner never goes dark mid-turn

This is the canonical invariant for any turn-spanning behavior:

```go
frames := sc.filmstrip().Frames()
for i, s := range frames {
    if i == len(frames)-1 {
        // final true turn end: spinner must be cleared
        continue
    }
    if s.Diff.StatusText == "" {
        t.Errorf("step %d (%s): spinner went dark mid-turn; trace=%v", i, s.Label, trace)
    }
}
```

### Direct status helpers (concise single-step assertions)

```go
sc.statusVisible() // bool — spinner showing any text
sc.statusText()    // string — current spinner text
```

## Event types & states cheat sheet

| `agentic.EventType` | Meaning | Typical status label set |
|---------------------|---------|--------------------------|
| `EventStateChange` | agent state transition | Thinking... / Answering... / Tool calling / Sending request... |
| `EventContent` (`Role=Assistant`, `State=StateThinking`) | reasoning stream | Thinking... |
| `EventContent` (`Role=Assistant`, `State=StateContent`) | answer stream | Answering... |
| `EventToolCall` | tool invocation | Tool calling (X/Y) |
| `EventToolResult` | tool output | Sending request... (re-query) |
| `EventProgress` | prompt-processing / reconnect | Processing... N% / Sending request... |
| `EventEnd` | **end of a conversation turn** (NOT a round) | clears spinner (`SessionEnd`) |
| `EventTokenStats` / `EventContextStats` | usage | (no status change) |

> `EventEnd` means the **whole turn** is over. The agent SDK must NOT emit it
> mid-turn (e.g. after a tool batch while another round will follow). A mid-turn
> `EventEnd` arms the spinner's session-ended guard and silently drops every
> later `Show()` — the "spinner disappears after the first tool call" bug.
> Regression guard: `TestAgent_SingleEventEndAcrossToolCallTurn`.

## TUI-layer-only tests (no app events)

For component behavior that does not involve agent events (input editing,
cursor navigation, overlays), drive the engine directly and read
`engine.AgentFrame()`:

```go
term := &fakeTerminal{w: 80, h: 24}      // tui package: testTerminal analog
engine := tui.NewTUI(term)
ed := tui.NewEditor(); ed.SetTUI(engine); ed.SetFocused(true)
engine.AddChild(ed); engine.SetFocus(ed)
engine.Start(); defer engine.Stop()
engine.SendKey("abc"); engine.SendKey("up")
frame := engine.AgentFrame()
// frame.Cursor, frame.FindNode("Editor"), frame.CursorNode() ...
```

See `tui/agentic_dom_test.go` for canonical examples.

## Workflow for a UI bug report

1. **Reproduce as an event sequence.** Read the export's `session/events.jsonl`
   to get the exact `Type`/`State`/`Role` sequence the user hit.
2. **Write a failing filmstrip test** in `internal/app/` that replays that
   sequence and asserts the invariant (e.g. spinner visible mid-turn).
3. **Localize.** Is it the agent SDK emitting a wrong event (e.g. mid-turn
   `EventEnd`)? Or the app handler mapping an event to the wrong status? Or a
   pure component rendering bug?
4. **Fix the root cause**, not the symptom.
5. **Verify** the filmstrip test now passes and `go test -race ./internal/app/ ./internal/agentic/ ./tui/` is green.

## What NOT to do

- ❌ Assert on ANSI escape bytes (`\x1b[...`) — protocol/layout dependent.
- ❌ Run goa against a live LLM to "see" the bug — non-deterministic, blind.
- ❌ Test `StatusMsg.Show/Clear` in isolation only — the bug is usually in the
  integration (event → handler → component). Always add a filmstrip scenario.
- ❌ Re-introduce a mid-turn `EventEnd`. One `EventEnd` per turn, at the true end.

## Reference files

- `tui/filmstrip.go` / `tui/filmstrip_test.go` — the recorder
- `tui/compositor.go` — `Scene`, `AgentFrame`, `AgentNode`, `AgentFrame.Dump()`
- `internal/app/ui_scenario_test.go` — the `uiScenario` harness
- `internal/app/ui_scenario_regression_test.go` — canonical spinner regression
- `internal/app/stats_status_test.go` — many component-tree wiring examples
- `internal/app/stats.go` — the event→status/streaming handlers under test
- `docs/TUI.md` — "Agent-testable UI (Filmstrip)" section
