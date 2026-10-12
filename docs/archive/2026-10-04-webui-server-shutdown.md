# B1 — `goa server` could not be stopped from its own console

Closed 2026-10-04. Report and fix plan were `bugs.md` B1; this file is the
closure record: root cause, the fix, the RED/GREEN measurements and the residual
risks.

## Symptom (as reported)

`goa server` could not be stopped from its own console. `Ctrl+C` (SIGINT) and
`SIGTERM` were both ignored: the HTTP listener stopped answering but the process
kept running, and `--cpuprofile`/`--memprofile` never wrote their files. Cleaning
up after the web-UI frame-cost work needed `kill -9` on eight leaked servers that
a `pkill` (SIGTERM) had left behind.

## Root cause

`internal/app/webui.go` `runWebServer` built a context with
`signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` and
then blocked in `New(subs).Run()`, which never observed that context. Two
consequences, one cause:

1. the signal was consumed by the handler, so the default die-on-Ctrl+C
   behaviour was gone, and nothing acted on it instead;
2. the web UI session has **no TTY** — `subs.terminal` is a `VirtualTerminal`, so
   Ctrl+C is never read as a keystroke the way the interactive TUI reads it. The
   server console's only path to stopping the session was the signal, and that
   path dead-ended.

The session's own exit path was fine: `App.Run` blocks on the `done` channel
returned by `setupEventHandlers`, which closes when the TUI engine stops, and
`/quit` already worked (a browser's `/quit` sends a `StopRequest` control event,
which stops the engine). The server simply had no way to *reach* that path.

## Fix

* `internal/app/app.go` — `App.Run` is now `App.RunContext(ctx)` (`Run()` is the
  `context.Background()` case, so every existing caller and test is unchanged).
  After the event readers are live, `watchStopContext(ctx, engine)` ties the
  external context to the session's own shutdown: on `ctx.Done` it calls
  `requestStop`.
* `requestStop` sends the **same** `event.ControlEvent{StopRequest: true}` that
  `/quit` sends, handled on the commandLoop, so `TUI.Stop` runs on the single
  owning goroutine and its documented restore ordering is untouched (compositor
  reset → `Terminal.Stop` → `close(done)`). The send is bounded (2 s) and falls
  back to `engine.Stop()` — `stopOnce`-guarded and safe from any goroutine — so a
  full bus or a dead reader can never leave an unstoppable process again.
* `internal/app/webui.go` — `runWebServer` is split so the wiring is testable:
  `serveWebUI(subs, opts, session, ctx)` binds, serves, runs the session, and
  returns **after** the session has ended and the listener is closed. The HTTP
  server gets its own context, cancelled only at that point, so a console Ctrl+C
  can no longer shut the listener down underneath a live session (the old code
  closed it in `Serve`'s own `ctx.Done` goroutine).
* `internal/app/webui.go` — `shutdownSignals()` replaces `signal.NotifyContext`:
  the first SIGINT/SIGTERM cancels the shutdown context (graceful stop), a second
  one exits immediately with the conventional `128+signum` status. NotifyContext
  consumed Ctrl+C without observing it *and* left a second Ctrl+C just as
  ignored, so a graceful stop that failed to finish would have been unstoppable.

Because `runApp` defers `prof.stopProfiling()` and `serveWebUI` now returns
instead of blocking forever, `--cpuprofile`/`--memprofile` are flushed and the
process exits 0.

## Measurements

### RED — the tests against the pre-fix code

`cmd/goa/e2e_server_signal_test.go` run with `internal/app/app.go` and
`internal/app/webui.go` restored to `8c42fc73`'s wiring (the new app-level test
file moved aside so the package still built):

```
=== RUN   TestGoaE2E_ServerCtrlCStopsProcessWithStatusZero
    goa server did not exit within 15s (output goa web UI ready: http://127.0.0.1:65301/s/
        ^C)
--- FAIL: TestGoaE2E_ServerCtrlCStopsProcessWithStatusZero (17.88s)
=== RUN   TestGoaE2E_ServerSigtermStopsProcessWithStatusZero
    goa server did not exit within 15s (output goa web UI ready: http://127.0.0.1:65306/s/)
--- FAIL: TestGoaE2E_ServerSigtermStopsProcessWithStatusZero (17.73s)
=== RUN   TestGoaE2E_ServerBrowserQuitStopsProcessWithStatusZero
--- PASS: TestGoaE2E_ServerBrowserQuitStopsProcessWithStatusZero (2.88s)
```

The `^C` echo with no exit is the reported defect exactly. The browser `/quit`
test passing on the pre-fix code is expected and useful: that path was never
broken, so it is a regression guard for this change, not a fix.

### GREEN — the fixed code

`go test -tags e2e -count=1 -run TestGoaE2E_Server -v ./cmd/goa/`, real binary on
a real PTY, three consecutive full runs (9/9 tests):

```
--- PASS: TestGoaE2E_ServerCtrlCStopsProcessWithStatusZero (3.05s)
    e2e_server_signal_test.go:183: profile cpu.prof: 1660 bytes
    e2e_server_signal_test.go:184: profile mem.prof: 9976 bytes
--- PASS: TestGoaE2E_ServerSigtermStopsProcessWithStatusZero (2.83s)
--- PASS: TestGoaE2E_ServerBrowserQuitStopsProcessWithStatusZero (2.98s)
```

Each test asserts, on the real process: exit within 15 s (measured ~2.8-3.1 s of
wall clock including startup), exit status **0**, the bound port no longer
accepts connections, and — for the Ctrl+C case — both profile files exist and are
non-empty. Ctrl+C is a real `0x03` written into the console's PTY (the line
discipline turns it into SIGINT: the server never puts that TTY in raw mode);
SIGTERM is sent to the process; `/quit` is typed over a websocket exactly as the
page types it (one `{t:"key"}` per keydown, encoded by the server).

App-level tests (`internal/app/server_shutdown_test.go`, covered by the recorded
verify command):

| Test | Proves |
|---|---|
| `TestRunContext_ContextCancelEndsSessionAndRestoresTerminal` | the REAL `RunContext` is still running before the cancel, returns after it, and restores the terminal exactly once (TUI.Stop ordering) |
| `TestRunContext_AlreadyCancelledContextStopsSession` | Ctrl+C during startup is not missed |
| `TestRequestStop_SendsTheQuitControlEvent` | the console's stop is the same control event `/quit` sends |
| `TestRequestStop_FallsBackToStoppingTheEngine` | an unreachable control reader still stops the session |
| `TestServeWebUI_ContextCancelEndsSessionThenClosesListener` | listener answers while the session runs, closes only after it ends, `serveWebUI` returns nil |
| `TestShutdownSignals_FirstSignalCancelsContext`, `TestSignalExitCode` | SIGTERM reaches the session as a cancelled context; escalation status mapping |
| `TestStartProfiling_StopWritesBothFiles` | the deferred flush writes `--cpuprofile`/`--memprofile`, non-empty |

### Gate

All run on the final tree:

* `go vet ./...` — clean.
* `staticcheck ./internal/app/... ./internal/webui/... ./cmd/goa/...` and
  `staticcheck -tags e2e ./cmd/goa/...` — clean.
* `gocognit -over 15` / `gocyclo -over 12` on every touched file — clean.
* `go test -count=1 -timeout 600s -race -cover ./...` — **88 packages ok, 0 FAIL**.
* `go test -count=1 -timeout 180s ./internal/app/ ./internal/webui/` (the recorded
  verify command) — ok, 30.1 s / 4.1 s.
* No stray `goa server` / `goa-e2e-test` process after any run (`ps`, verified
  after every e2e pass).

## Residual risks and follow-ups

* **Second-signal escalation is not measured end-to-end.** The graceful stop
  finishes in ~3 s of wall clock (well under the 15 s budget) and in tens of
  milliseconds once the session ends, so a second Ctrl+C is hard to land inside
  the window and the path calls `os.Exit` (unassertable in-process). Covered by
  the `signalExitCode` unit test and left as-is; it is a safety net, not the
  primary path.
* **New defect found and recorded as `bugs.md` B7**: a page that connects and
  types while the session is still starting can lose the *effect* of its first
  keystrokes. The terminal half is fixed here — `webui.VirtualTerminal` now holds
  pre-`Start` bytes (bounded, 4 KiB) and replays them in `Start` instead of
  dropping them silently, with unit tests for replay, post-Stop dropping and the
  bound. The remaining half is that `inp.SetOnSubmit` is wired in
  `setupEventHandlers`, after `buildTUI` already started the engine, so an Enter
  replayed at `Start` finds no handler (the text stays in the editor). That is a
  session-startup ordering question, out of scope for a shutdown fix, and it is
  why the browser `/quit` e2e keeps a documented bounded re-send instead of
  asserting a single send.
* `docs/webui-perf-assessment.md` §5.1 recorded this defect while measuring frame
  cost; it now points here.
