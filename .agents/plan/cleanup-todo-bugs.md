# Plan: Cleanup TODO.md & bugs.md

## Objective
Clean up the TODO and bugs tracker files, add the Zero gap analysis as a new track.

## Files to modify

### 1. `TODO.md` — cleanup + add Track 3

**Current state:** Two fully-completed tracks (Bugs+Reviews, Orchestration) with
detailed per-step checkboxes. All items checked. ~190 lines.

**Target state:** Compact archival entries for Tracks 1-2. New Track 3 with the
Zero gap analysis items, un-checked. Gates section at bottom.

**New structure:**
```markdown
<!-- SPDX-License-Identifier + Copyright header -->

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
- [ ] Add pattern-based secret detection (API keys, tokens, private keys, JWTs)
- [ ] Redaction engine to strip from tool outputs before sending to model
- [ ] Tests for detection + redaction
- [ ] Gates + commit

#### Z2 — LSP integration
- [ ] LSP client (JSON-RPC 2.0, Content-Length framing)
- [ ] gopls lifecycle management
- [ ] Diagnostics gathering on file writes
- [ ] Navigation tools (go to definition, find references, hover)
- [ ] Tests for each component
- [ ] Gates + commit

#### Z3 — Self-verify loop
- [ ] Test framework discovery (Go test, pytest, jest, etc.)
- [ ] Run tests → capture structured output → feed to model
- [ ] Remediation loop: fix attempt → re-run → repeat
- [ ] Configurable max attempts, stop-on-pass
- [ ] Gates + commit

#### Z4 — Sandbox shell analysis
- [ ] Add `mvdan.cc/sh/v3` AST-based command analysis to existing sandbox
- [ ] Classify commands: destructive, network, interactive, safe
- [ ] Permission-gated execution in `bash` tool
- [ ] Tests for analysis + enforcement
- [ ] Gates + commit

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
```

### 2. `bugs.md` — keep guidelines, keep open bug

**Current state:** Guidelines + one open bug about `#30363d` color codes in
`ask_user_question` tool output.

**Target state:** Same as current — guidelines are correct, open bug stays
until fixed. No content changes needed. Just verify it's clean.

## Verification
- [x] `TODO.md` has clear Track 3 with Zero gap analysis items
- [x] `bugs.md` still has the open bug entry
- [x] Both files pass `go vet` (they're not Go files, but no syntax errors)
