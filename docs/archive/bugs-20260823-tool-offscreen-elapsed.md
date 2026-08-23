# Archived fix — off-screen running tool keeps live status; final duration truthful

Source: `bugs.md` §2 (2026-08-23). Closed 2026-08-23.

## Problem

A running tool widget that scrolled above the viewport stopped receiving visible repaints. Its elapsed status froze in terminal scrollback, and completion could not rewrite the stale historical row with the true final duration.

## Fix

- Added pinned `ToolLiveStrip`, shown while the oldest running tool is fully off-screen. It recomputes elapsed at render time and displays current progress in the live chrome.
- Added one-time compositor scrollback resync requests at the off-screen running-update and off-screen completion boundaries. This avoids per-tick full transcript resets while ensuring the final `Took Xs` row is truthful.
- Wired the viewport callback to the compositor and the live strip into the assembled application engine.

## Tests and validation

Added compositor/viewport boundary tests, live-strip tests, and fake-terminal integration coverage. Validation passed:

- `go test ./tui/ ./internal/app/ -count=1`
- `go vet ./...`
- `staticcheck ./...` (only pre-existing unrelated SA1019)
- `gocognit -over 15 .` and `gocyclo -over 12 .` (only pre-existing unrelated findings)
- `go test -count=1 -race -cover ./...`

Interactive PTY filmstrip validation used a 30×100 terminal, real Goa binary, and an OpenAI-compatible mock streaming one `sleep 12` tool plus six quick tools. Captured output contained a live off-screen strip (`sleep 12`, `elapsed`) and final `Took 12.0s`; result: `VALIDATION PASS`.

## Acceptance

The off-screen running status remains current in pinned chrome, no per-tick scrollback reset occurs, and completion rewrites scrollback with the true final duration.
