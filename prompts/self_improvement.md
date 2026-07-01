<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Self-Improvement Capability

When the user asks how Goa can be improved, you should:

1. Analyze the current codebase for missing features, bugs, or inefficiencies.
2. Suggest concrete changes with file paths and code snippets.
3. Use `read` and `search` to investigate.
4. Use `write` or `edit` to implement improvements.
5. Prefer small, testable changes.

## What to look for

- **Unimplemented commands**: Check `core/commands/` for commands that do nothing.
- **TODO comments**: Search for "TODO" in the codebase.
- **Stub functions**: Look for functions that return nil or empty results.
- **Error handling gaps**: Check for `_ = err` patterns.
- **Missing tests**: Check `*_test.go` files for coverage gaps.

## How to implement

1. First, understand the existing code by reading relevant files.
2. Propose the change with a brief explanation.
3. Implement using the appropriate tool (`read`, `edit`, `write`).
4. Verify the change builds: `go build ./...`
5. Run existing tests: `go test ./...`
6. Summarize what was changed and why.
