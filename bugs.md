# Bugs found by qa-e2e skill

## B1: Empty `--prompt ""` launches TUI instead of headless mode

**Scenarios:** 3 (Error handling)

**Observed:** `goa --yes --timeout 30s --prompt ""` opens the full TUI instead of
running in headless mode. The command hangs waiting for TUI input.

**Root cause:** In `internal/app/bootstrap.go`, `promptImpliesHeadless()` checks
`o.prompt != ""` — when the prompt is explicitly set to empty string, it returns
false, so goa falls back to TUI mode. The intent of `--prompt ""` is to run
headless with an empty/invalid prompt; the tool should produce an error about
empty input rather than opening a TUI.

**Fix:** Change `promptImpliesHeadless()` to also check whether the `--prompt` flag
was explicitly set (not just whether the value is non-empty).

## B2: `goa build -o` target must be `./cmd/goa/` not `.`

**Scenarios:** Setup

**Observed:** Running `go build -o /tmp/goa ./goa` builds a library archive
(`current ar archive`) instead of an executable binary.

**Root cause:** The module root package (`github.com/pijalu/goa`) is a library,
not a main package. The main entry point is in `cmd/goa/`.

**Note:** This is a documentation/build knowledge issue, not a code bug. The
qa-e2e skill build instructions should use `./cmd/goa/`.

## B3: Headless mode output format uses structured markers

**Observed:** The headless renderer outputs lines like `-- user`, `-- thinking start`,
`-- assistant`, `-- stats`, `-- summary`. While this is useful for parsing, the
qa-e2e skill's validation instructions assume simpler output. Validation should
account for the actual output format.

**Mitigation:** The skill validation is flexible enough (checks for substrings),
so this is not a blocker. It may cause false negatives if validation expects
exact output matching.
