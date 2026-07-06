<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Archived Bugs — 2026-07-06

## Questions are not rendered correctly

**Status:** Fixed

**Problem:** The `ask_user_question` tool rendered clarification cards with raw
hex color strings (e.g. `#30363d`, `#c9d1d9`) visible in the terminal instead of
proper ANSI color escapes. The cards looked like:

```
#30363d╭──────────────────────────────────────────────────────────
#30363d│ #c9d1d9❓ Topic for Summary
...
```

**Root cause:** `tui/clarify_card.go` obtained theme colors via
`TheTheme.ColorHex(...)`, which returns the raw `#RRGGBB` string. The renderer
concatenated those strings directly into the output as if they were ANSI escape
sequences.

**Fix plan:**
1. Reproduce the render locally with a ClarifyCard and inspect raw output.
2. Change `cardColors()` to wrap each `ColorHex` result with `ansi.Fg(...)` so
   it returns a true-colour ANSI foreground escape sequence.
3. Add a regression test asserting that no raw hex strings leak into the render
   and that ANSI escape sequences are present.
4. Run the existing TUI tests to ensure cards still display their title,
   summary, question, and options.

**Validation:**
- `go test -count=1 -race -cover ./tui/...` passes.
- New regression test `TestClarifyCard_RenderUsesAnsiNotHex` passes.
- `go vet ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .` pass.

**Files changed:**
- `tui/clarify_card.go`
- `tui/clarify_card_test.go`
