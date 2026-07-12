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
- [x] Lifecycle events: beforeTool, afterTool, sessionStart, sessionEnd
- [x] Dispatch engine (fire hooks with JSON payload to stdin of command)
- [x] Blocking hooks that can veto tool execution
- [x] Cascading config: user + project level
- [x] Audit store for hook execution history
- [x] Tests + gates + commit

#### Z6 — Background process manager
- [x] Durable task registry package (`internal/background`) with JSON persistence
- [x] Task lifecycle: started → running → completed/error/killed
- [x] PID tracking, output ring-buffer capture, exit code recording
- [x] Cross-platform process termination (SIGTERM → SIGKILL)
- [x] Tests for the manager package
- [x] Integrate manager into `bg_exec` tool for persistent task tracking
- [x] TUI sidebar integration for live status
- [x] Gates + commit

#### Z7 — Swarm mailbox (Tier 3)
- [x] Agent-to-agent messaging bus (`internal/agentic.AgentBus`)
- [x] `send_message` / `receive_message` tools
- [x] `CommConnector` for auto-receive wiring into `AgentPool`
- [x] Tests for bus, tools, and connector
- [x] Gates + commit

#### Z8 — OAuth flow (Tier 3)
- [x] Device-code / authorization-code flow for supported providers
- [x] Encrypted token storage
- [x] Auto-refresh support via oauth.TokenSource
- [x] Tests + gates + commit

#### Z9 — Plugin system (Tier 3)
- [x] Git-based plugin distribution (`plugins install <git-url>`)
- [x] Manifest validation
- [x] Lockfile with content-hash tracking
- [x] Permission-gated activation via trust manager
- [x] Plugin-scoped skills (skills_dir in manifest)
- [x] Runtime plugin loading for enabled plugins
- [x] Tests + gates + commit

## Gates
All changes must pass the 5 gates run **separately**:
1. `go vet ./...`
2. `staticcheck ./...`
3. `gocognit -over 15 .`
4. `gocyclo -over 12 .`
5. `go test -count=1 -race -cover ./...`
