<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B19 — `-race` failure in the supervisor reaper test (test-helper race)

Closed 2026-10-10. Archived from `bugs.md`.

## Reported (found while validating B18)

`go test -race ./...` failed intermittently on `internal/webui/supervisor` — clean
on the tree as it stood (reproduced with `git stash`, so not introduced by the
B18 change), but flaky, which made the `-race` gate untrustworthy:

```
$ go test -race ./internal/webui/supervisor/ -run TestReapLoopReapsAndStops -count=1
==================
WARNING: DATA RACE
Write at 0x00c000454298 by goroutine 9:
  supervisor.(*fakeChild).Stop()   supervisor_test.go:66
  supervisor.(*Supervisor).reapOnce()   supervisor.go:359
  supervisor.(*Supervisor).reapLoop()   supervisor.go:333
Previous read at 0x00c000454298 by goroutine 8:
  supervisor.TestReapLoopReapsAndStops.func3()   child_test.go:657
--- FAIL: TestReapLoopReapsAndStops (0.02s)
```

## Root cause

The race is entirely in the test scaffolding, not in `Supervisor`:
`fakeChild.Stop()` incremented a plain `int` field (`stopped`) from the **reaper
goroutine**, while the test goroutine read the same field in its
`waitForCond` predicate. The read happened to hold `sup.mu`, but that mutex
guards the supervisor's child map — not the fake's counter — so it provided no
synchronisation for this field.

## Fix

`fakeChild.stopped` is an `atomic.Int64`, incremented in `Stop()` and read through
a new `stops()` accessor; the four read sites use it. No production code changed.

## Tests / validation

* `go test -race ./internal/webui/supervisor/ -count=3` → ok (was: DATA RACE).
* `go test -race ./internal/webui/... -count=2` → ok.
* `go test -count=1 -race ./...` → green, twice in a row.

## Residual risk

None identified: the counter is the only field the reaper goroutine touches on the
fake (`session`, `path`, `lastUse`, `retired` are written by the test before the
child is registered and only read after). A future field mutated from a background
goroutine would need the same treatment — the type now documents why.
