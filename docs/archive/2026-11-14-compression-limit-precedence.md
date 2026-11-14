<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: Compression limit precedence and hard-tier/status mismatch

Closed (fix landed before 2026-11-14). Moved from `bugs.md` (entry: "Compression limit precedence and hard-tier/status mismatch — fixed").

## Root cause

Compression limit resolver preferred configured `MaxTokens` over known model capacity; status-bar independently displayed model capacity. Hard-tier gates correctly used conservative estimated usage, but status-bar percentage and some logs used projected occupancy, obscuring the hard-tier decision.

## Fix

Known model context window (runtime-refreshed first, then model metadata) is now authoritative across compression and status-bar. Explicit compression `MaxTokens` is fallback only when model capacity is unknown. Status-bar percentage and hard-tier checks/logs report conservative estimated occupancy; the soft-tier gate remains provider-projected to preserve its cache behavior.

## Regression coverage

Model-window precedence over larger/smaller configured limits; hard-tier triggers when estimate reaches hard ceiling despite lower projected usage; soft tier remains projection-based.

## Validation

Focused agentic tests passed; `go test ./internal/agentic -count=1 -race -cover -timeout 120s` passed (86.3%); `go test ./... -count=1 -timeout 120s`, `go vet ./...`, and `staticcheck ./...` passed. Complexity checks report pre-existing unrelated warnings in app/provider/core/config.
