# Windows console bugs — 2026-11-28

Archived per bugs.md guideline 4. Source: Windows port report (mascot
partially blocked, resize issues, menu freezing) → review of the Windows
console code (`tui/*_windows.go`, `tui/terminal.go`, `tui/tui.go`).

## W1 — listenResize respawned the resize watcher on every event — FIXED

**Observed:** `tui/tui.go` `listenResize()` evaluated `resizeEvents(t.done)`
inside the `select` in the `for` loop, so every resize event created a new
channel AND a new watcher goroutine; old watchers only exited at process end.
On Windows each stale `winResizeWatcher` kept running `WaitForSingleObject` +
`Peek/ReadConsoleInputW` on the shared console input queue: races multiplied,
stolen key records were re-injected at the queue END (reordering), resize
events were consumed by dead channels (missed/delayed resizes), and watchers
busy-spun while any foreign record sat queued. On Unix it leaked a SIGWINCH
registration + goroutine per resize and fired duplicate `RequestRender`.

**Fix:** hoisted `resizeEvents(t.done)` above the loop — one watcher per
session (`tui/tui.go`).

**Test:** `TestListenResizeCreatesOneWatcherPerSession`
(`tui/listen_resize_test.go`) — fails on the old code (RED verified by stash),
passes with the fix; asserts exactly one watcher across 5 synthetic events and
one `RequestRender` per event. Gates: vet/staticcheck clean, gocognit/gocyclo
clean for changed files, `go test -count=1 -race -cover ./...` green,
`GOOS=windows go build ./...` green.
