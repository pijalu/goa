# B29 — Multi-tool `waiting` / `elapsed` investigation

Closed 2026-10-23.

## Disposition

Not a rendering defect. The reported `elapsed / waiting / elapsed / elapsed`
sequence accurately describes the exported batch's actual scheduler state:
three calls had started and one conflicting shell call was still queued.
Changing the later running calls to `waiting` would conceal real concurrency.

Regression coverage now distinguishes that valid mixed batch from a fully
serialized batch. No production code changed.

## Report and expected invariant

The report described the first tool as elapsed, the second as waiting, and all
later tools as elapsed even though the later tools were assumed to be queued.

The invariant is based on execution state, not position in the batch:

- pending with complete arguments and no execution start: `⧖ waiting Ns…`;
- running after the scheduler's execution-start event: `elapsed Ns`;
- pending with arguments still streaming: `elapsed Ns` for argument-stream age.

## Export evidence

The source archive was `.goa/exports/goa-export-20261023-210438.zip`, inspected
read-only through a scratch extraction. Its `session/events.jsonl` contains the
exact discriminating bash/bash/search/search batch:

```text
1425: tool_call   bash   tool_nzn22rLCkVxAD9pK6t6EkePf
1426: tool_call   bash   tool_FihZ99W3R8z0ZxYObRXI7cwe
1427: tool_call   search tool_hLdx0G7SEuliApDqKdGlFIcy
1428: tool_call   search tool_PRpiGc4vj3ci1J2Kv4Ejxxem
1429: tool_start  bash   tool_nzn22rLCkVxAD9pK6t6EkePf
1430: tool_start  search tool_hLdx0G7SEuliApDqKdGlFIcy
1431: tool_start  search tool_PRpiGc4vj3ci1J2Kv4Ejxxem
1440: tool_start  bash   tool_FihZ99W3R8z0ZxYObRXI7cwe
```

Bash declares the shared `shell` access category (`tools/bash.go`), so the two
shell calls serialize. Search declares no conflicting resource access, so both
searches can execute independently of the active shell call. Consequently the
second shell widget waits while the two search widgets legitimately show
elapsed execution time.

Source tracing agreed with those events:

- `internal/agentic/agent_tools.go:81` wires scheduler `OnStart` to
  `EventToolStart`;
- `internal/agentic/tool_scheduler.go:175` emits that callback when a task
  actually starts, including when a queued task becomes unblocked;
- `internal/tooltracker/tracker.go` finalizes arguments without starting a
  widget and transitions pending to running on start/progress;
- `tui/tool_execution.go:365` renders complete pending calls from `waitStart`
  and running calls from the execution `startTime`.

## Regression reproduction

`internal/app/tool_waiting_test.go` now drives four calls through the real
`agentic.ToolScheduler`, routes `OnStart` through the production app event
handler, and captures the production component tree as a `Filmstrip`.

1. `TestUI_QueuedToolsShowWaiting` uses four conflicting shell calls. The first
   remains blocked in execution, forcing c2/c3/c4 to stay queued. Every queued
   widget is asserted pending with complete arguments, an hourglass, `waiting`,
   an ellipsis, and no `elapsed` text.
2. `TestUI_MixedToolBatchShowsActualExecutionStates` replays the exported
   bash/bash/search/search access pattern. It asserts bash1 running, bash2
   waiting, and both searches running—the reported sequence, with the correct
   cause established by the actual start events.
3. Both tests assert counts on `AgentFrame.Visible`, not just component state,
   and log the ANSI-free visible terminal for inspection.

The focused command passed:

```text
go test -count=1 -run '^(TestUI_QueuedToolsShowWaiting|TestUI_MixedToolBatchShowsActualExecutionStates)$' -v -timeout 30s ./internal/app
--- PASS: TestUI_QueuedToolsShowWaiting
--- PASS: TestUI_MixedToolBatchShowsActualExecutionStates
PASS
```

Actual visible-terminal excerpts from that run:

```text
● $ go test ./...
elapsed 0.03s
⧖ $ ls -la
waiting 0.04s…
⧖ $ go vet
waiting 0.03s…
⧖ $ gofmt -l .
waiting 0.03s…
```

and the exported mixed-access reproduction:

```text
● $ go vet ./...
elapsed 0.03s
⧖ $ go test ./...
waiting 0.03s…
● search first (non-recursive)
elapsed 0.02s
● search second (non-recursive)
elapsed 0.02s
```

The targeted race run also passed:

```text
go test -count=1 -race -run '^(TestUI_QueuedToolsShowWaiting|TestUI_MixedToolBatchShowsActualExecutionStates)$' -timeout 60s ./internal/app ./internal/agentic ./tui
ok github.com/pijalu/goa/internal/app
ok github.com/pijalu/goa/internal/agentic [no tests to run]
ok github.com/pijalu/goa/tui          [no tests to run]
```

## Quality gates

Each required command was run separately after the final test refactor:

- `go vet ./...` — passed.
- `staticcheck ./...` — passed.
- `go test -count=1 -race -cover -timeout 180s ./...` — passed all packages in
  59.61s.
- `gocognit -over 15 .` — exit 1 for three unrelated pre-existing functions:
  `core.(*GoalDriver).Drive` 21, `core.(*AgentManager).executeRunner` 16, and
  `app.initGoalSystem` 16. The first version of the new tests also exceeded the
  budget (27 and 25); shared scenario/assertion helpers removed those new
  findings before closure.
- `gocyclo -over 12 .` — exit 1 only for unrelated pre-existing
  `core.(*GoalDriver).Drive` 13.

No server, browser, or live-model process was started, so no such cleanup was
required.

## Residual risk

The export does not retain a screenshot, so the historical pixels cannot be
compared directly. It does retain the authoritative call/start ordering, and
the new visible-terminal replay covers the exact access pattern and labels.
A different report in which a call renders `elapsed` while its arguments are
complete and no `tool_start` or `tool_progress` event exists would need a new
reproduction; this export contains no such state for the reported batch.
