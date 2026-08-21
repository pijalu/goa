# Cache-miss status line

Status: closed.

The footer gate in `internal/app/stats_footer.go` already suppresses `CM` when full and partial counters are zero. Existing regression tests cover zero, full-only, partial-only, mixed output, reset behavior, and plain output. Baseline and focused tests passed.

Validation: `go vet ./...`, `staticcheck ./...`, and race tests passed during this fix session.
