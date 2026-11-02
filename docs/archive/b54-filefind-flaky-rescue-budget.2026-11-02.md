# B54 — internal/filefind completion test flaky under full-suite load

Archived 2026-11-02. The report and plan lived in `bugs.md` (# To fix).

## Observed behavior

Under the full `go test -count=1 -race -cover ./...` run,
`TestComplete_AbsoluteOutsideRoot/fd_absolute_hits_outside_root_stay_absolute`
(internal/filefind/filefind_test.go:266) failed once with `fd path = []`
(the fake-fd rescue returned nothing); standalone, the package passed
repeatedly with and without -race. The subtest drives the bounded `fd`
rescue (250 ms timeout per B39+B49), so under heavy parallel load the
rescue can exceed its budget and legitimately return no candidates — the
assertion depended on machine load, not behavior.

## Root cause (proven, not suspected)

`Search.fdCandidates` bounds the rescue with a hard-coded
`context.WithTimeout(context.Background(), fdTimeout)` (250 ms). The fake
fd is a `#!/bin/sh` script: under the full suite's parallel load, even
process spawn can exceed 250 ms, the context deadline kills it, and
`fdCandidates` — per its own contract — reports "no usable output", so
`Complete` returned `[]` and the assertion failed. RED on demand: a fake
fd sleeping 350 ms reproduced the exact reported failure
(`fd path = []`, elapsed ~500 ms) with the pre-fix code.

## Fix

Production timeout untouched. The rescue budget became instance-scoped
and overridable, following the existing builder pattern:

- `internal/filefind/filefind.go`: `Search` gained a `budget` field and
  `WithTimeout(d)`; `fdCandidates` uses `s.budget`, falling back to the
  `fdTimeout` default when unset. The default remains the interactive
  keystroke policy (B49): a slow fd on a huge tree must never hold a
  keystroke-triggered result hostage.
- `internal/filefind/filefind_test.go`: the flaky subtest extends the
  budget (`WithTimeout(5*time.Second)`) so its assertion depends on the
  normalization contract (fd hits outside root stay absolute), never on
  spawn latency.
- `internal/filefind/b54_test.go` (new): `TestComplete_FDSlowButWithinBudget`
  pins the budget contract deterministically — a fake fd that needs
  300 ms (slower than the 250 ms interactive budget, faster than the
  load-proof test budget) is capped under the default budget and rescued
  when the search extends it. Slow-machine behavior without ambient load.

## Validation

- RED observed: slow-fd stub (350 ms) against the pre-fix subtest →
  `fd path = []` (the reported symptom, on demand).
- GREEN: package suite race-enabled, including the new budget tests.
- Gates run separately: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .` (pre-existing hotspots only, all unrelated —
  app/b53 filmstrip test, provider startplan test, core GoalDriver.Drive,
  app tool-factory coverage test, config mirror test, app initGoalSystem,
  core executeRunner), `gocyclo -over 12 .` (same unrelated set).
- Full `go test -count=1 -race -cover ./...` three consecutive times
  under artificial load (13 busy loops on 12 cores): exit 0, zero FAIL
  lines each run (83 s / 71 s / 70 s), filefind 91.9% coverage. Load
  processes stopped after each run; none left running.

Residual risk: none identified — production behavior (250 ms default)
is unchanged and still pinned by the capping half of the new test.
