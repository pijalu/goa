<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goa — Terminal-native AI coding agent

## Hard Rules

1. **Always favor good implementation** — Debug root causes, don't remove features.
2. **Always assume the code is wrong** — Trace every byte path before blaming the environment, terminal, or timing.
3. **Always test** — Every fix must include a test that would have caught it. Fix the code, not the test.
4. **A huge correct implementation is much better than a small incorrect one** — Don't lower scope to save effort; do the full clean design.
5. **Complex work done now is better than tomorrow** — Delay compounds: later changes are layered on top of the design you deferred, making the eventual rework harder and riskier. Do the hard part now.
6. **Follow SOLID design** — Methods should be generic and composable (small primitives + factories), not fat per-type APIs. Single Responsibility per type; Open/Closed for extension; depend on abstractions.

## Architecture

- **Module**: `github.com/pijalu/goa` · Go 1.25+
- **Main entry**: `cmd/goa/main.go` → `internal/app/app.go`
- **TUI engine** (`tui/`): Component-based ANSI TUI — `Component` interface, `TUI` engine with differential rendering, CSI 2026 synced output (inspired by pi/OpenCode/kimi-code).
- **Agent SDK**: Lives in `internal/agentic/`
- **Config cascade**: embedded → home (~/.goa/) → project (.goa/) → local (.goa/config.local.yaml) → env (GOA_) → flags
- **Tools**: `tools/` package — each implements `agentic.Tool` interface. Renderers in same package for TUI display.
- **Colors/ANSI**: `internal/ansi/` package for all escape sequence handling.

## Testing

- **Unit tests**: < 100ms per test, < 5s per package. Use `go test -timeout 30s`.
- **Coverage targets**: internal ≥90%, config ≥85%, core ≥80%, tools ≥80%, tui ≥70%
- **Test patterns**: table-driven for validation, `t.TempDir()` for filesystem, keep tests independent
- **Gate**: `go vet ./...`, `go test -count=1 -race -cover ./...`, `gocognit -over 15`, `gocyclo -over 12` before committing

## Complexity Budget

- Config parsing: max 20 (gocognit) / 12 (gocyclo)
- TUI rendering: max 18 / 12
- All other logic: max 15 / 12

## Key Conventions

- All prompts embedded via `//go:embed` Markdown files — no hardcoded prompt text
- Tool errors use `internal.ToolError` format
- Commands self-register via `init()` in `core/commands/`
- TUI renderers for tools in `tools/*_renderer.go`, registered via `tui/register_renderers.go`
