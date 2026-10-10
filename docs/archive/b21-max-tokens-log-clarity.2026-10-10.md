<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B21 — `goa/protocol` logged an unclear, per-request `max_tokens` note

Closed 2026-10-10. Archived from `bugs.md`.

## Reported

In a live session, straight onto stderr:

```
goa/protocol: 2026/10/10 11:51:06 field max_tokens came from reasoning-ceiling default: 384000 (model=deepseek-v4.1-flash provider=opencode-go)
```

## Why it was wrong

1. **The wording named a thing that does not exist.** There is no "reasoning-ceiling
   default". `resolveRequestMaxTokens` returns source `reasoning-ceiling` when the
   request carries no cap *and* the provider catalog has no `default_max_tokens`, so
   the model's own **maximum output** is used. 384000 is the catalog's `limit.output`
   for `deepseek-v4.1-flash` (`internal/agentic/provider/models/api.json`) — a
   ceiling, not a default. A reader could not tell a safe fallback from a
   suspiciously large cap.
2. **The level and the frequency were wrong.** A `log.Printf` to stderr
   (`protocolLog`, `openai_completions.go`) repeated on *every* request that omitted
   `max_tokens`, writing into the terminal while the TUI owned the screen.

## Fix

* `noteMaxTokensDefault` replaces the inline `Printf`: one sentence per source that
  states the field, the value, **why** it applies and the identifiers —
  `max_tokens=384000 (no explicit value and no provider default, so the model's
  maximum output applies), model=deepseek-v4.1-flash provider=opencode-go`.
* The source strings became constants (`maxTokensSourceProvider`,
  `maxTokensSourceCeiling`, `maxTokensSourceFallback`) shared by the resolver and the
  diagnostic, so the reason and the wording cannot drift apart.
* `defaultNotes` dedupes the line per `(model, source)`: a streaming session explains
  its wire requests once instead of per request.
* An explicit request value still logs nothing (unchanged).

## Tests

* `TestDefaultMaxTokens_LogsDefaultedField` — the provider-default line names the
  field, the model, the provider and the reason, appears **once** for a repeated
  resolution, and stays silent for an explicit cap.
* `TestDefaultMaxTokens_ReasoningCeilingWording` — the exact case reported: value,
  model, provider, "the model's maximum output applies", and no `came from`.

## Validation

* `go test -count=1 -race ./internal/agentic/provider/...` — all packages ok.
* Manual check of the emitted line (printed by the test above).

## Residual risk

The dedupe map grows one entry per `(model, source)` pair seen in a process — bounded
by the catalog and the three sources, so it cannot grow with traffic. A test replaces
the whole `maxTokensNotes` value, so no test depends on ordering between cases.
