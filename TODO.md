<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# TODO — execution tracker

Two-track plan, decided 2026-07-04: concrete bugs + reviews ship first
(single session), Orchestration is a separate dedicated session.

## Track 1 — Bugs + Reviews (execute now)
**Plan:** [`docs/FIX-PLAN-2026-07-04.md`](docs/FIX-PLAN-2026-07-04.md)

97 numbered microsteps, in execution order:

- [x] **B1** — Thinking-loop discoverability (completion + config menu)
- [x] **B2** — Up-arrow on empty line / cannot navigate to empty line (follow-up: also recall history when cursor is at the start of the first visual line of a non-empty buffer, not just when the buffer is empty)
- [x] **B3** — `goal` tool: `.goa/goals` location + disable flag (default off) + cache collapse (kimi-code append-on-top model)
- [x] **B4** — Spinner disappears after 1st tool call
- [x] **B5** — Steering messages enqueued (`prompt.steered`-style injection)
- [x] **B6** — Config selection list cursor at `search>` marker
- [x] **B7** — smartsearch review (fix everything found)
- [x] **R1** — Stability review of all TUI code (drainInput goroutine leak fixed; panic recovery added to commandLoop/renderLoop; callback entry points already covered by dispatchInput recover; edge paths handled by existing shutdown/resize paths)
- [x] **R2** — Perf review of all TUI code (profiled via `-perf-load`; top user hotspots are compositor differential rendering and terminal write syscalls; no user-code regressions found; drainInput goroutine removal also reduces allocation churn)
- [x] **R3** — Functional review: workflow/swarm/multi-agent/goal (full test suite passes under `-race`; no new regressions found; steering queue already aligned with multi-agent orchestrator)
- [x] **Close** — gates re-run separately, bugs archived to `docs/archive/bugs.2026-07-04.md`, `bugs.md` reduced to guidelines only

## Track 2 — Orchestration (separate session)
**Plan:** [`docs/ORCHESTRATION-DESIGN.md`](docs/ORCHESTRATION-DESIGN.md)

43 numbered microsteps across 8 phases. Confirmed decisions baked in:
per-run topology selector (hub/fanout/pipeline), config-only role→model map
with bounded pool (`max_agents_per_model` + `max_total_agents`), tabs per
agent + orchestrator + Summary, tab-driven steering (orchestrator may post
to agents), fully event-sourced & resumable under `.goa/orchestrator/<run-id>/`,
layered above swarm, optional goal binding.

- [ ] Phase 0 — Config schema (`OrchestratorConfig`)
- [ ] Phase 1 — Bounded agent pool with caps
- [ ] Phase 2 — AgentHandle & live stats
- [ ] Phase 3 — Orchestrator runtime (topology selector)
- [ ] Phase 4 — Event sourcing & resumability
- [ ] Phase 5 — TUI: orchestrator view + tabs + summary
- [ ] Phase 6 — Goal binding integration
- [ ] Phase 7 — Wiring, slash commands, headless flag
- [ ] Phase 8 — Validation & gates

## Notes
- The previous TODO content (agentic optimization pass) is fully resolved
  and lives in git history; closed bugs are under `docs/archive/`.
- All changes must pass the 5 gates run **separately**: `go vet ./...`,
  `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...`.
