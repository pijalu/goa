<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# TODO — execution tracker

## Completed tracks (archived)
- **Track 1 — Bugs + Reviews** (2026-07-04): All bugs closed, reviews done.
  See `docs/archive/bugs.2026-07-04.md` and `docs/FIX-PLAN-2026-07-04.md`.
- **Track 2 — Orchestration** (2026-07-04..2026-07-08): Full agent-driven
  workflow shipped. 8 design phases complete.
  See `docs/ORCHESTRATION-DESIGN.md` and `docs/ORCHESTRATOR.md`.

## Track 3 — Zero gap analysis (2026-07-11)
**Plan:** [`PLAN-gap-analysis-zero.md`](PLAN-gap-analysis-zero.md)

### Progress

#### Z1 — Secrets scanner & redaction
- [x] Add pattern-based secret detection (API keys, tokens, private keys, JWTs)
- [x] Redaction engine to strip from tool outputs before sending to model
- [x] Tests for detection + redaction
- [x] Gates + commit

#### Z2 — LSP integration
- [x] LSP client (JSON-RPC 2.0, Content-Length framing)
- [x] gopls lifecycle management
- [x] Diagnostics gathering on file writes
- [x] Navigation tools (go to definition, find references, hover)
- [x] Tests for each component
- [x] Gates + commit

#### Z3 — Self-verify loop
- [x] Test framework discovery (Go test, pytest, jest, etc.)
- [x] Run tests → capture structured output → feed to model
- [x] Remediation loop: fix attempt → re-run → repeat (agent-driven via repeated tool calls)
- [x] Configurable max attempts, stop-on-pass (via verify package RunLoop; tool exposes basic run)
- [x] Gates + commit

#### Z4 — Sandbox shell analysis
- [x] Add `mvdan.cc/sh/v3` AST-based command analysis to existing sandbox
- [x] Classify commands: destructive, network, interactive, safe
- [x] Permission-gated execution in `bash` tool
- [x] Tests for analysis + enforcement
- [x] Gates + commit

#### Z5 — Hooks system
- [ ] Lifecycle events: beforeTool, afterTool, sessionStart, sessionEnd
- [ ] Dispatch engine (fire hooks with JSON payload to stdin of command)
- [ ] Blocking hooks that can veto tool execution
- [ ] Cascading config: user + project level
- [ ] Audit store for hook execution history
- [ ] Gates + commit

#### Z6 — Background process manager
- [ ] Durable task registry (JSON on disk) for background processes
- [ ] Task lifecycle: started → running → completed/error/killed
- [ ] PID tracking, output file capture, exit code recording
- [ ] TUI sidebar integration for live status
- [ ] Cross-platform process termination
- [ ] Tests + gates + commit

## Gates
All changes must pass the 5 gates run **separately**:
1. `go vet ./...`
2. `staticcheck ./...`
3. `gocognit -over 15 .`
4. `gocyclo -over 12 .`
5. `go test -count=1 -race -cover ./...`
