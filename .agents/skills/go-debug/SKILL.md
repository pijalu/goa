---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: go-debug
description: Debug Go code using the Delve (dlv) debugger to inspect runtime behavior, trace execution flow, and diagnose issues by stepping through code rather than reading source. Use when investigating reproducible bugs, unexpected state, goroutine behavior, or when source code analysis is insufficient.
---

# Go Debug with Delve

Use `dlv` to inspect running Go programs. When a bug is reproducible, debugging is faster than source-code analysis: set breakpoints, reproduce the failure, and inspect actual runtime state.

## When to Use This Skill

| Situation | Action |
|-----------|--------|
| Bug is reproducible but root cause unclear from source | **Use dlv** |
| Need to inspect variable values at a specific point | **Use dlv** |
| Tests fail and you need the exact failure point | **Use dlv** |
| Concurrency issue (deadlock, race, goroutine leak) | **Use dlv** |
| Logic error in a long chain of function calls | **Use dlv** |
| Simple off-by-one or obvious typo | Read source instead |
| API shape or interface mismatch | Read source instead |

## Prerequisites

```bash
go install github.com/go-delve/delve/cmd/dlv@latest
dlv version  # verify — older versions may panic on certain binaries
```

## Core Workflow

1. **Start** — launch dlv against the entry point
2. **Break** — set breakpoint(s) at suspect location(s)
3. **Run** — `continue` until breakpoint hit
4. **Inspect** — `locals`, `args`, `print expr`, `stack`
5. **Step** — `next`/`step`/`stepout` to trace exact flow
6. **Restart** — `restart` after code changes
7. **Quit** — `exit` when done

## Quick Command Reference

### Starting Modes

```bash
# Debug a package (builds and runs)
dlv debug ./cmd/myapp --headless=false --allow-non-terminal-interactive=true

# Debug pre-built binary
dlv exec ./bin/myapp --headless=false --allow-non-terminal-interactive=true

# Debug tests
dlv test ./mypackage --headless=false --allow-non-terminal-interactive=true -- -test.run TestName

# Attach to running process
dlv attach <pid>
```

For best debugging experience, build the binary first with optimizations disabled:
```bash
go build -gcflags=all="-N -l" -o myapp-debug ./cmd/myapp
dlv exec ./myapp-debug --headless=false --allow-non-terminal-interactive=true
```

### Navigation

| Command | Alias | Description |
|---------|-------|-------------|
| `break <loc>` | `b` | Set breakpoint |
| `breakpoints` | `bp` | List breakpoints |
| `clear <id>` | | Delete breakpoint |
| `continue` | `c` | Run until next breakpoint |
| `next` | `n` | Step over line |
| `step` | `s` | Step into function |
| `stepout` | `so` | Step out of function |
| `restart` | `r` | Restart program |
| `exit` / `quit` | | Exit debugger |

### Inspection

| Command | Description |
|---------|-------------|
| `locals` | Local variables |
| `args` | Function arguments |
| `print <expr>` | Evaluate expression |
| `whatis <expr>` | Show type |
| `stack` | Call stack |
| `goroutines` | List goroutines |
| `goroutine <id>` | Switch to goroutine |
| `threads` | List OS threads |
| `list` | Show source at current line |

### Location Formats

- `main.main` — function name
- `./internal/server/server.go:42` — relative file:line
- `/absolute/path/file.go:42` — absolute file:line

## Interactive Session via interactive_shell

**Start:**
```bash
interactive_shell({
  command: "dlv debug ./cmd/myapp --headless=false --allow-non-terminal-interactive=true",
  mode: "hands-free",
  reason: "Debug server crash on startup"
})
```

**Send commands:**
```bash
input: "break main.main"
submit: true

input: "continue"
submit: true

input: "locals"
submit: true

input: "print cfg"
submit: true

input: "quit"
submit: true
```

**Poll output:**
```bash
interactive_shell({
  sessionId: "xxx",
  outputLines: 30
})
```

## Investigation Patterns

### Pattern 1: Trace a Function

When you know where the bug is but not why:

```
break mypkg.HandleRequest
continue
locals
args
print req.Header
next
next
print result
```

### Pattern 2: Debug a Failing Test

```
dlv test ./internal/db --headless=false --allow-non-terminal-interactive=true -- -test.run TestMigrate
break TestMigrate
continue
locals
print err
stack
```

### Pattern 3: Inspect Goroutines

For concurrency issues:

```
break mypkg.worker
continue
goroutines
goroutine 5
stack
locals
```

### Pattern 4: Conditional Breakpoint

When the bug only happens with specific data:

```
break ./handler.go:85
condition 1 len(items) > 100
continue
print items
```

### Pattern 5: Attach to Running Process

For bugs that only appear in long-running processes:

```bash
# Find PID
pgrep -f "myapp"

# Attach and debug
dlv attach <pid>
break main.handleWS
continue
locals
```

## Tips

- **Build for debug**: Use `-gcflags=all="-N -l"` when building binaries for debugging
- **Restart quickly**: After fixing code, use `restart` instead of quitting and relaunching dlv
- **Source context**: Use `list` to see source context without leaving the debugger
- **Expression evaluation**: `print` accepts Go expressions: `len(slice)`, `obj.Field.Method()`
- **Goroutine switching**: After `goroutines`, use `goroutine <id>` to inspect another goroutine
- **Absolute paths**: Prefer absolute paths for breakpoints if relative paths fail
- **Test flags**: Pass test flags after `--` separator: `dlv test ./pkg -- -test.run TestName -v`

## Troubleshooting

| Issue | Fix |
|-------|-----|
| "Stdin is not a terminal" | Add `--allow-non-terminal-interactive=true` |
| "could not find symbol value for x" | Variable out of scope; step past declaration |
| Breakpoint not hit | Verify path; try absolute path; check if function was inlined |
| Program exits immediately | Set breakpoints before `continue` |
| dlv panics on binary | Update dlv: `go install github.com/go-delve/delve/cmd/dlv@latest`; build with `-gcflags=all="-N -l"` |
| dlv not found | `go install github.com/go-delve/delve/cmd/dlv@latest` |
