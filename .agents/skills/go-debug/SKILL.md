---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: go-debug
description: Debug Go code using the Delve (dlv) debugger to inspect runtime behavior, trace execution flow, and diagnose issues by stepping through code rather than reading source. Use when investigating reproducible bugs, unexpected state, goroutine behavior, or when source code analysis is insufficient.
---

# Go Debug with Delve

Use `dlv` to inspect running Go programs. When a bug is reproducible, debugging is faster than source-code analysis.

## When to Use

- Reproducible bug with unclear root cause
- Need exact variable values or failure point
- Concurrency issue (deadlock, race, goroutine leak)
- Logic error in a long call chain

For simple typos or API mismatches, read source instead.

## Prerequisites

```bash
go install github.com/go-delve/delve/cmd/dlv@latest
```

## Core Workflow

1. **Start** — `dlv debug ./cmd/myapp`, `dlv test ./pkg`, or `dlv exec ./bin`
2. **Break** — `break <loc>` (function, file:line, or absolute path)
3. **Run** — `continue`
4. **Inspect** — `locals`, `args`, `print <expr>`, `stack`
5. **Step** — `next`, `step`, `stepout`
6. **Restart** — `restart` after code changes

## Key Commands

| Command | Description |
|---------|-------------|
| `break <loc>` | Set breakpoint |
| `continue` | Run until next breakpoint |
| `next` | Step over |
| `step` | Step into |
| `stepout` | Step out |
| `locals` | Local variables |
| `args` | Function arguments |
| `print <expr>` | Evaluate expression |
| `stack` | Call stack |
| `goroutines` | List goroutines |
| `goroutine <id>` | Switch to goroutine |
| `list` | Source at current line |

## Common Patterns

### Trace a Function
```
break mypkg.HandleRequest
continue
locals
args
print req.Header
next
print result
```

### Debug a Failing Test
```
dlv test ./internal/db -- -test.run TestMigrate
break TestMigrate
continue
locals
print err
stack
```

### Inspect Goroutines
```
break mypkg.worker
continue
goroutines
goroutine 5
stack
locals
```

### Conditional Breakpoint
```
break ./handler.go:85
condition 1 len(items) > 100
continue
print items
```

## Interactive Session

Use `interactive_shell` with `mode: "hands-free"` for long-running dlv sessions. Send commands with `input` + `submit: true`, poll with `outputLines`.

## Tips

- Build with optimizations disabled: `go build -gcflags=all="-N -l" -o myapp-debug ./cmd/myapp`
- Use `restart` instead of relaunching after code changes
- `print` accepts Go expressions: `len(slice)`, `obj.Field.Method()`
- Prefer absolute paths for breakpoints if relative paths fail
- Pass test flags after `--`: `dlv test ./pkg -- -test.run TestName -v`

## Troubleshooting

| Issue | Fix |
|-------|-----|
| "Stdin is not a terminal" | Add `--allow-non-terminal-interactive=true` |
| "could not find symbol value for x" | Variable out of scope; step past declaration |
| Breakpoint not hit | Verify path; try absolute path; check if function was inlined |
| dlv panics on binary | Update dlv; build with `-gcflags=all="-N -l"` |
