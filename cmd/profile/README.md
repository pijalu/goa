# goa performance/tracing harness

`cmd/profile` runs goa under controlled load while collecting Go profiles so
you can generate heat maps and verify CPU usage stays in the 10–15% target
range.

> For profiling a **normal interactive goa session** in your real terminal,
> use the built-in `--with-profiling` flag instead. See
> [`docs/PROFILING.md`](../docs/PROFILING.md).

## Modes

### Synthetic (default)

Runs the TUI engine in-process with a fake terminal and a synthetic streaming
load. Fast and requires no LLM.

```sh
go run ./cmd/profile
```

### PTY mode

Builds the real goa binary and runs it inside a PTY with the built-in
`--perf-load` mode. This exercises the actual terminal I/O path.

```sh
go run ./cmd/profile -mode=pty -duration=30s
```

### PTY mode with a real prompt

Builds the real goa binary, starts it in a PTY, sends the given prompt, and
lets it run for the configured duration before interrupting it with Ctrl+C.
Use this to profile a real agent turn end-to-end.

```sh
go run ./cmd/profile \
  -mode=pty \
  -prompt="analyse and summarize this project" \
  -duration=60s
```

## Output

- `cpu.prof` — Go CPU profile (use with `go tool pprof`)
- `mem.prof` — heap profile
- `trace.out` — execution trace (`go tool trace`)
- `pty.log` — PTY output (only in PTY mode)

## Generate a flame graph

```sh
go tool pprof -http=:8080 cpu.prof
```

Then open http://localhost:8080 and switch to the Flame Graph or Top view.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-mode` | `synthetic` | `synthetic` or `pty` |
| `-cpu` | `cpu.prof` | CPU profile path |
| `-mem` | `mem.prof` | Memory profile path |
| `-trace` | `trace.out` | Execution trace path |
| `-duration` | `30s` | How long to run the load / wait before Ctrl+C in prompt mode |
| `-width` | `120` | Terminal width |
| `-height` | `40` | Terminal height |
| `-rate` | `60` | Synthetic updates per second |
| `-messages` | `1000` | Synthetic update count |
| `-prompt` | `""` | Real prompt to send to goa in PTY mode (empty = use `--perf-load`) |
| `-pty-log` | `pty.log` | PTY output capture path |
| `-keep-binary` | `false` | Keep the temporary goa binary (PTY mode) |

## Interpreting CPU usage

The harness prints CPU time as a percentage of wall time. For pi-like
responsiveness goa should stay under **15%** on this load. If it is higher,
open the CPU profile in pprof to find the bottleneck.
