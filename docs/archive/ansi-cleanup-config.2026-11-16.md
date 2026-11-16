# ANSI escapes leak into goa output

**Closed** 2026-11-16. Fix: configurable ANSI cleanup for untrusted tool
output, enabled by default (`tools.strip_ansi: true`).

## Observed

Tool output was only sanitized: raw ESC bytes became visible `\e[...]` text
before reaching the model context and TUI. Child-process output therefore
produced noisy, hard-to-read transcripts such as:

```text
\e[32m✓\e[39m web/src/ui/menuModel.test.ts \e[2m(\e[22m\e[2m17 tests\e[22m\e[2m)\e[22m\e[32m 3\e[2mms\e[22m\e[39m
\e[2m Test Files \e[22m \e[1m\e[32m1 passed\e[39m\e[22m\e[90m (1)\e[39m
```

## Expected

ANSI escape sequences are removed from untrusted tool output before it is
displayed, logged, or sent to the model. Users can opt back into the legacy
visible-escape rendering with configuration.

## Fix

* Added `ansi.Clean`, a process-wide cleanup gate that strips ANSI escape
  sequences and then sanitizes remaining control bytes. Default: on.
* Added `tools.strip_ansi` (tri-state pointer, embedded default `true`) with
  cascade merge, `/config:set`, completion, and a Tools-menu toggle.
* Applied the gate to untrusted-output producers: bash, python, run_code,
  ssh_bash, search, smartsearch, read_file, verify, and goal verification.
* Wired the gate into config load, config reload, hot reload, and live
  `/config:set tools.strip_ansi` updates.
* Documented the option in `docs/CONFIGURATION.md`.

## Tests

* `internal/ansi`: `Clean` strips SGR/erase sequences, preserves literal
  `\e[...]` text, sanitizes other controls, and honours the off switch.
* `tools`: bash tool strips ANSI by default and retains visible escapes when
  disabled; existing sanitization tests for python/read/search/verify and the
  smartsearch byte-golden test pin the opt-out path.
* `config`: embedded default is `true`; a higher cascade layer can set `false`.
* `core/commands`: `/config:set tools.strip_ansi false` persists and flips the
  runtime gate immediately.

## Validation

* `go build ./...` — pass
* `go vet ./...` — pass
* `staticcheck ./...` — pass
* `gocyclo -over 12 .` — pass; pre-existing `TestResolveStartPlanAPIKey`
  and `alignOneMessage` warnings are in untouched files
* `gocognit -over 15 .` — pass for changed files; pre-existing warnings in
  untouched tests and `initGoalSystem` remain
* `go test -count=1 -race -cover ./...` — pass
