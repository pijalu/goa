<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Development Guide

## Prerequisites

```bash
# Go 1.22+
go version

# Complexity checking tools
go install github.com/uudashr/gocognit/cmd/gocognit@latest
go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
```

## Project Layout

```
goa/
├── main.go                  # Entry point
├── cmd/goa/root.go          # Cobra CLI root
├── config/                  # Configuration cascade
├── core/                    # Core engine
│   ├── command.go           # Command interface + registry
│   ├── router.go            # Command routing
│   ├── agentmanager.go      # Agent lifecycle
│   ├── execution.go         # Execution modes
│   ├── loopdetector.go      # Loop detection
│   ├── sessionstore.go      # Session persistence
│   ├── docengine.go         # Documentation engine
│   ├── context.go           # Command context
│   └── commands/            # Command implementations
├── internal/                # Shared types, errors, git worktree,
│   └── agentic/             # merged Agent SDK
├── tui/                     # ANSI TUI (inspired by pi/OpenCode/kimi-code)
├── tools/                   # Tool implementations
├── memory/                  # Memory store
├── prompts/mode/            # Mode definitions (coder, planner, reviewer)
├── provider/                # Provider management
├── skills/                  # Skill registry + runner
├── multiagent/              # Multi-agent orchestrators
├── plugins/                 # JS plugin system
├── chunks/                  # Milestone briefs
├── docs/                    # Documentation
├── Makefile                 # Build automation
└── bugs.md                  # Known issues and roadmap
```

## Common Tasks

### Build

```bash
make build        # Build binary
make install      # Install to GOPATH/bin
make clean        # Remove artifacts
```

### Test

```bash
make test           # Full test suite with race detection + coverage
make test-short     # Fast tests (no race)
make test-race      # Race detection only
make test-cover     # HTML coverage report → coverage.html
```

### Quality

```bash
make vet       # go vet
make fmt       # go fmt
make lint      # gocognit + gocyclo complexity checks
```

### Run

```bash
make run        # Build and run
go run .        # Run without building
go run . --model gpt-4o --debug   # With flags
```

## Code Style

### Formatting

Goa follows standard Go formatting (`gofmt`). Run `make fmt` before committing.

### Complexity Budget

| Scope | Cognitive Limit | Cyclomatic Limit |
|-------|----------------|-----------------|
| Config parsing | 20 | — |
| TUI rendering | 18 | — |
| All other logic | 15 | 12 |

Run complexity checks with `make lint` or:

```bash
gocognit -over 15 ./...
gocyclo -over 12 ./...
```

Any function exceeding thresholds must be refactored.

### Error Format

All tool errors follow the standard format:

```
[tool_name error: error_type]
Detail message
Hint: Actionable suggestion for recovery
```

Defined in `internal/errors.go`:

```go
type ToolError struct {
    Tool     string // tool name
    Type     string // error category (file_not_found, permission_denied, etc.)
    Detail   string // human-readable detail
    HintText string // actionable recovery hint
}
```

## Testing Standards

### Coverage Targets

| Package | Target |
|---------|--------|
| `internal/` | ≥90% |
| `config/` | ≥85% |
| `core/` | ≥80% |
| `tools/` | ≥80% |
| `memory/`, `prompts/`, `provider/` | ≥75% |
| `tui/` | ≥70% (state logic) |

### Test Patterns

- **Table-driven tests** for validation logic
- **`t.TempDir()`** for filesystem tests
- **Independent tests** — no shared mutable state
- **Explicit error cases** — test missing params, invalid input, permission errors
- **Naming**: `Test<FunctionName>_<Scenario>`

### Running Tests

```bash
# All tests
go test -count=1 -race -cover ./...

# Single package
go test -count=1 -race -cover ./config/...

# Single test
go test -count=1 -race -run TestLoader_Load ./config/...

# All packages must pass before commit
make test
```

## Working with the Agent SDK

The Agent SDK lives in `internal/agentic/`. It was originally a standalone module and has been merged into Goa, so it carries no external dependency. Its key types are used throughout Goa:

- `agentic.Agent` — Created by `AgentManager`
- `agentic.Tool` — Implemented by all tools in `tools/`
- `agentic.LLMProvider` — Created by `provider.Manager`
- `agentic.OutputObserver` — Consumed by TUI panes
- `agentic.AgentBus` — Multi-agent messaging
- `agentic/skillrunner.Runner` — Skill execution

## Adding a New Tool

1. Create a file in `tools/` implementing `agentic.Tool`
2. Optionally implement `tools.Documentable` for self-documentation
3. Register in `main.go`'s `registerTools()` function
4. Write tests in `tools/*_test.go`
5. Run complexity checks

## Adding a New Command

1. Create a file in `core/commands/` implementing `core.Command`
2. Self-register via `init()`: `core.RegisterCommand(&MyCommand{})`
3. Add command routing if needed in `core/router.go`
4. Write tests in `core/commands/*_test.go`

## Adding a New Profile

1. Add a built-in in `profiles/defaults.go`
2. Or define custom in `~/.goa/config.yaml` with `extends`

## Commit Guidelines

- **Atomic commits** — one logical change per commit
- **Test first** — write tests before implementation
- **All tests pass** — `make test` before commit
- **Complexity check** — `make lint` passes
- **Descriptive messages** — explain what and why

## Continuous Integration

(CI to be set up — currently local builds only.)
