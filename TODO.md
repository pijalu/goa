<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# TODO — execution tracker

Tracks 1 (Bugs + Reviews) and 2 (Orchestration) are complete and archived at
[`docs/archive/tracks-1-2-complete.2026-07-04.md`](docs/archive/tracks-1-2-complete.2026-07-04.md).

---

## Track 3 — Zero feature gaps

**Analysis:** [`docs/research/goa-gap.md`](docs/research/goa-gap.md) (gap analysis)
**Plan:** [`docs/research/goa-gap-plan.md`](docs/research/goa-gap-plan.md) (implementation plan)

Eight gaps identified from Zero's feature set, scoped into 3 phases:

- **Phase 1 (P0, parallel):** MCP HTTP client transport + lifecycle hooks engine
- **Phase 2 (P1/P2, sequential):** Hooks JSON config → audit log → specialist CLI → plugin skills → MCP server CLI
- **Phase 3 (P1):** Plugin bundling (JSON manifest with tools + hooks + skills)

See the plan document for detailed types, integration points, and step-by-step instructions.

## Notes

- All changes must pass the 5 gates run **separately**: `go vet ./...`,
  `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...`.
