<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Profiling Goa

Goa can capture Go CPU, heap, and execution-trace profiles during normal use.
The profiles are written when the process exits, so you can analyze them
afterwards with the standard Go toolchain or hand them to another agent.

## Quick start: profile a normal session

Start goa with the `--with-profiling` flag and use it normally. When you exit
(`/quit` or Ctrl+C), the profiles are saved next to the binary:

```sh
./goa --with-profiling
# ... do normal goa work ...
# ... exit goa ...

ls cpu.prof mem.prof trace.out
```

You can then inspect the results:

```sh
# Interactive flame graph / top view
go tool pprof -http=:8080 cpu.prof

# Text summary
go tool pprof -top cpu.prof

# Execution trace (opens a browser UI)
go tool trace trace.out
```

## Individual profile flags

`--with-profiling` is a shortcut that enables all three profiles with default
file names. You can also enable them individually and override the paths:

```sh
./goa \
  --cpuprofile=cpu.prof \
  --memprofile=mem.prof \
  --trace=trace.out
```

| Flag | Output | Analysis tool |
|------|--------|---------------|
| `--cpuprofile` | `cpu.prof` | `go tool pprof` |
| `--memprofile` | `mem.prof` | `go tool pprof` |
| `--trace` | `trace.out` | `go tool trace` |

## Profiling a headless task

For a non-interactive prompt, use `--prompt` together with profiling flags.
The task runs to completion and the profiles are written on exit:

```sh
./goa --with-profiling --prompt="analyse and summarize this project" --yes
```

`--yes` auto-approves tool calls so the run can finish without user input.

## Automated harness

For repeatable, synthetic load testing you can also use the harness in
`cmd/profile/`. It runs goa in a controlled PTY with either the built-in
performance load or a real prompt and reports CPU usage. See
[`cmd/profile/README.md`](../cmd/profile/README.md) for details.

## What to look for

The goal is to keep goa's CPU usage under **15%** during normal streaming. In
the CPU profile, the TUI path should be a small fraction of the total; most
time should be spent waiting on I/O or the LLM. If `tui.(*Compositor).Render`,
`ChatViewport` rendering, or unicode width functions dominate the samples, the
rendering pipeline is the bottleneck.

## Notes

- Profiles are only written when goa exits cleanly. If you kill the process
  with `kill -9`, the CPU/trace profiles may be incomplete.
- The `--with-profiling` flag is intentionally orthogonal to `--perf-load`;
  `--with-profiling` just enables collection, while `--perf-load` triggers a
  synthetic TUI workload.
