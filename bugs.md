# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

## W2 — Escape debounce blocks readLoop on Windows; next keypress after Esc is swallowed/corrupted (menu freezing)

**Observed:** `tui/terminal.go:252` calls `os.Stdin.SetReadDeadline` which returns `os.ErrNoDeadline` on Windows console handles (silently ignored). `pollEscapeDebounce`'s `os.Stdin.Read` (:255) then blocks until ANY next byte. The 40ms fallback timer dispatches the Escape, but the blocked Read later returns the user's next keypress and forwards it merged as `"\x1b"+data` (:276) even though Escape was already dispatched: printable keys become Alt+key (swallowed, `tui/escape.go`), arrows produce a ghost extra Escape. Every Esc press poisons the following keystroke — menus appear to freeze / ignore input.

**Expected:** Esc dispatches exactly once; the following keypress arrives unmodified.

**Fix direction:** platform-specific debounce — on Windows do not block-read for continuation bytes (VT input delivers whole sequences per Read; `StdinBuffer` already buffers incomplete sequences), and never re-merge `\x1b` after the timer already dispatched it.

**Test approach:** unit test with `stdinRead` indirection simulating `ErrNoDeadline` + slow input: Esc then 'j' must produce exactly `["\x1b", "j"]`. Validate: `go test -count=1 -race ./tui/ -run Escape`.

## W3 — Missing UTF-8 output code page on Windows: mascot/header art renders garbled ("partially blocked")

**Observed:** no `SetConsoleOutputCP(CP_UTF8)` anywhere. `enableWindowsVTOutput` (`tui/terminal_windows.go:38`) sets only `ENABLE_VIRTUAL_TERMINAL_OUTPUT`. On consoles whose output code page is not 65001 (legacy conhost defaults CP437/850), the UTF-8 mascot art (`tui/header.go:184`, `▄` block glyphs + 256-color SGR) is decoded per the OEM code page → mojibake, wrong glyph widths, broken side-by-side layout.

**Expected:** UTF-8 output code page set while the TUI runs and restored on Stop; mascot renders identically on conhost and Windows Terminal.

**Test approach:** unit test behind a syscall indirection asserting set-on-start / restore-on-stop. Manual validation note: cannot be fully verified without a Windows host — flag for manual QA on Windows.

## W4 — winResizeWatcher.readOne can block indefinitely; reinjection reorders keys

**Observed:** `tui/resize_windows.go:203,274-290` — after peeking a resize record, `ReadConsoleInputW` is called; if the stdin byte reader dequeues that record first, the queue is empty and `ReadConsoleInputW` BLOCKS until the next record arrives (the `!got` branch at :207 is unreachable). The watcher then dequeues a foreign key record and re-injects it at the queue END → delayed, reordered keys. Reverse race: `ReadFileW` silently discards `WINDOW_BUFFER_SIZE_EVENT` records it wins, so resize detection degrades to the 1s safety net even when healthy.

**Expected:** watcher never dequeues foreign records and never blocks on an empty queue.

**Fix direction:** peek-only design — on `WAIT_OBJECT_0`, just re-check size via `notifyIfResized` (no `ReadConsoleInputW`, no reinjection); shorten the safety net (~100–250ms) to keep resize responsive.

**Test approach:** unit tests with synthetic record streams via existing indirections (`consoleSizeFunc`); race-free drain test. Validate: `go test -count=1 -race ./tui/ -run Resize`.

## W5 — Shutdown drain leaks a blocked stdin reader on Windows

**Observed:** `tui/terminal_drain_windows.go:13-15` spawns `drainInputFallback` → `startStdinPoller` goroutine blocks in `stdinFile.Read` forever when no input arrives before `Stop` returns. On an in-process TUI restart (anticipated by `screenOwnerCount`), the leaked poller competes with the new session's readLoop and eats its keys.

**Expected:** drain goroutine terminates when the drain window ends (or process exit), never outliving the session.

**Test approach:** unit test asserting the poller is released after the drain timeout without input. Validate: `go test -count=1 -race ./tui/ -run Drain`.
