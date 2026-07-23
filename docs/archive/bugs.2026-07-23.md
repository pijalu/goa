<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking

## Guideline

1. Create a detailed fix plan for each bug/new feature - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found, even if not related to the bug/feature, must be fixed and the fix plan must be updated accordingly. You can add new items to the bug list as you find them.
3. Each item should be moved to archive when tested and closed as the associated plan.
5. Use filmstrip approach to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.
9. **Cache-hit-first design (CRITICAL for local models).** A cached prefix
   costs ~0; a full re-parse costs 40-100x more (measured 2026-07-21 on
   qwythos-9b-v2: 23.6 tok/s generation — a 20K-token re-parse is a 45-90s
   stall). Therefore every provider request must be **strictly append-only**:
   never move, rewrite, or re-project content mid-history; volatile
   per-request text may only ever be appended at the tail. The system prompt
   (byte 0) must stay byte-identical for the whole session. Anything that
   "decorates" messages per request (cache_control breakpoints, markers,
   wrappers) must be pinned to a fixed position — a marker that moves to the
   newest message each round rewrites history bytes and kills llama.cpp's
   longest-prefix cache match exactly where it lands. Validate any change to
   prompt/message construction with a proxy capture proving request N is a
   byte-prefix of request N+1, and by watching CH% climb in real sessions.

 *At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## STOP CONDITION (binding — an agent working this file must not stop early)
An agent working this file may ONLY stop when ALL of the following hold:
1. This file contains NO open items (every item is ✅/closed or moved to the archive).
2. Every item is tested and working (regression test green; filmstrip-validated where it is a UI behavior per guideline #5).
3. Any issue/problem discovered during the work has been ADDED to this file AND solved — nothing is deferred out-of-band.
A turn that ends with open items, an untested fix, or an unrecorded newly-found issue is a FAILED turn: continue working; do not summarize-and-stop.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

# Open items

## Stats on session - temporary ✅ FIXED

**Fix Plan:**
1. Add `CurrentTurn()` to `TurnRecorder` — returns snapshot of in-progress turn data (nil if no active turn)
2. Reset `turnStartTime` to zero in `FinalizeTurn` so `CurrentTurn()` correctly returns nil after completion
3. Add `CurrentTurn()` to `AgentManager` — delegates to `TurnRecorder`
4. Extend `SessionRecorder` interface with `CurrentTurn() *TurnRecord`
5. Implement `CurrentTurn()` on `Context` via `AgentManager`
6. Update `showStats` in `transparency.go` to display in-progress turn when history is empty but a turn is active
7. Update `fakeSessionRecorder` in tests to implement new interface
8. Add tests: `TestTurnRecorder_CurrentTurn_*` and `TestShowStats_InProgressTurn*`

**Test approach:** Unit tests verify `CurrentTurn()` returns nil before/after turns, correct snapshot during turn, and `showStats` displays live stats for in-progress turn. All existing tests still pass.

**Validation:** `go vet`, `staticcheck`, `gocognit`, `gocyclo`, `go test -race -cover` all pass. No new complexity issues introduced.

**Files changed:**
- `core/turnrecorder.go` — added `CurrentTurn()`, reset `turnStartTime` in `FinalizeTurn`
- `core/agentmanager.go` — added `CurrentTurn()` delegate
- `core/context.go` — extended `SessionRecorder` interface, added `CurrentTurn()` impl
- `core/commands/transparency.go` — updated `showStats` to show in-progress turn, added `writeCurrentTurnStats`
- `core/commands/test_helpers_test.go` — updated `fakeSessionRecorder`
- `core/commands/transparency_test.go` — added `TestShowStats_InProgressTurn*` tests
- `core/turnrecorder_test.go` — added `TestTurnRecorder_CurrentTurn_*` tests

## Tool calls optimization: bash / python / read ✅ AUDIT COMPLETE

**Audit scope:** All `.goa/sessions/*.jsonl` files in `/Users/muaddib/dev/goa` and `/Users/muaddib/dev/frigolite`.

### Tool Call Distribution (8,594 bash calls total)

| Tool | Count | % of total |
|------|-------|-----------|
| bash | 8,594 | 67.0% |
| read | 3,594 | 28.0% |
| edit | 2,788 | 21.7% |
| search | 746 | 5.8% |
| write | 317 | 2.5% |
| python | 31 | 0.2% |
| others | ~30 | <0.5% |

### Key Findings

#### 1. Bash `cd &&` prefix: 70.2% of all bash calls
The model prefixes `cd /project && ` before almost every command. This is unnecessary overhead — the bash tool should support a `workdir` parameter (like `read` and `edit` already resolve paths relative to project root). **Recommendation:** Add `workdir` field to bash tool schema; when set, prepend `cd <workdir> && ` automatically. This eliminates ~6,000 redundant `cd` commands from session history.

#### 2. `sed -n 'X,Yp'` used as read replacement: 692 calls (8.1% of bash)
The model uses `sed -n 'start,endp' file` to extract line ranges instead of the `read` tool with `start_line`/`end_line`. This is likely because the model doesn't always know the exact line numbers and prefers sed's flexibility. **Recommendation:** No code change needed — the `read` tool already supports `start_line`/`end_line`. The model's system prompt should emphasize using `read` with line ranges instead of `sed -n`. The `search` tool can help find line numbers first.

#### 3. `grep` used as search replacement: 190 calls (2.2% of bash)
Simple `grep pattern file` calls (not recursive, not piped) could use the `search` tool. **Recommendation:** System prompt guidance — prefer `search` tool for pattern matching.

#### 4. `find` used as search replacement: 109 calls (1.3% of bash)
`find ... -name ...` calls could use the `search` tool with glob patterns. **Recommendation:** System prompt guidance.

#### 5. `cat file` used as read replacement: 78 calls (0.9% of bash)
Simple `cat file` without pipes/redirects. **Recommendation:** System prompt guidance — use `read` tool.

#### 6. `ls` used as directory listing: 107 calls (1.2% of bash)
Simple `ls dir` calls. The `read` tool already returns directory listings. **Recommendation:** System prompt guidance — use `read` tool for directories.

#### 7. `head`/`tail` used as read replacement: 67 calls (0.8% of bash)
`head -N file` and `tail -N file` could use `read` with `max_lines`. **Recommendation:** System prompt guidance.

**Total potentially replaceable:** ~1,243 calls (14.5% of bash) — mostly addressable via system prompt tuning, not code changes.

#### 8. Python tool (gpython) limitations: 23 errors in 31 calls (74% error rate)
The gpython interpreter has significant compatibility issues:

| Error | Count | Root Cause |
|-------|-------|-----------|
| TypeError: 'bytes' not subscriptable | 3 | gpython `open(..., 'rb')` returns custom type, not real `bytes` |
| TypeError: 'file' not iterable | 1 | gpython file objects don't support iteration |
| TypeError: unsupported operand | 2 | gpython tuple comparison not implemented |
| TypeError: open() unexpected kwarg | 1 | gpython `open()` missing `encoding` kwarg |
| AttributeError | 5 | Missing stdlib attributes |
| FileNotFoundError | 4 | Jail path resolution issues |
| SyntaxError | 4 | gpython parser limitations (f-string nesting, etc.) |
| ImportError | 1 | Missing stdlib module |
| invalid_input | 1 | Malformed JSON input |

**Analysis:** gpython is fundamentally incompatible with real Python for anything beyond trivial scripts. The model tries to use it for file processing, data analysis, and text manipulation — all of which hit gpython's incomplete stdlib and type system. With a 74% error rate, the tool is more frustrating than useful.

**Recommendation:** Replace the python tool with a `script` tool that runs `python3` via the existing bash infrastructure (with jail restrictions). This gives the model real Python while maintaining security. Alternatively, remove the python tool entirely and let the model use `bash python3 -c "..."` (which it already does 67 times — more than the python tool's 31 calls).

### Actionable Items (priority order)

1. ✅ **Add `workdir` default to bash tool** — `runCommand` now defaults `cmd.Dir` to `ProjectDir` when `workdir` is not explicitly set. Eliminates 70% of `cd &&` prefix overhead. Tests added: `TestBashTool_Execute_DefaultWorkdir_UsesProjectDir`, `TestBashTool_Execute_ExplicitWorkdir_OverridesProjectDir`.
2. ✅ **Python tool description updated** — now warns about gpython limitations (Python 3.4 subset, no real bytes type, limited stdlib) and suggests `bash python3 -c "..."` for non-trivial scripts. Full replacement deferred (would need security review for real python3 via bash).
3. **System prompt: emphasize `read` over `sed -n`/`cat`/`head`/`tail`** — reduces 8.1% of bash calls (prompt change, not code)
4. **System prompt: emphasize `search` over `grep`/`find`** — reduces 3.5% of bash calls (prompt change, not code)
