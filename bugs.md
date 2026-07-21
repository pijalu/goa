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

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

# TODO
## Start-up
Regression: At startup, Goa inputline is not responsive for couple seconds - likely related to load up items - run a details review of startup sequence.

The inputline should not be impacted by the startup sequence / plugin loading / ...

**Hard requirement:** there must not be *any* HTTP/API calls blocking the startup path — every network call (provider probe, context-window refresh, quota prime, update check, model list, ...) must be fully async, with results applied when they land. goa *MUST* feel fast: first frame + responsive inputline come before any I/O.

## Goal
Regression: currently a goal execution does not show any status line details:
```
Let me start by understanding the current state of the project - what tests exist, what's failing, and what features are implemented.


● $ cd /Users/muaddib/dev/frigolite && go test ./... 2>&1 | tail -100 (timeout 120s)
elapsed 12.8s


● $ cd /Users/muaddib/dev/frigolite && go test -count=1 -v ./... 2>&1 | grep -E '^(=== RUN|--- FAIL|--- PASS|ok |FAIL)' |... (timeout 120s)
elapsed 12.8s


● $ cd /Users/muaddib/dev/frigolite && go test -count=1 -v ./... 2>&1 | grep -E 'FAIL' | head -100 (timeout 120s)
elapsed 12.8s

⬣ Tool calling
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
⟐ [warm.viper] create a detailled plan to implement *all* missing features to allow all test to pass - be complete then execute the plan - size of the
task should not matter, only matter the completion
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/frigolite (✱ main)                                                                                                             coding-posture │ YOLO
0.8%/1.0M (auto)                                                                                                    (opencode-go) deepseek-v4-flash • high
```

Status line update are critical to allow the user to see the progress of the goal execution / the size of the context / the cache.

## Multi-tool calling and timeout:
Multiple tool calls does not seems to respect timeout - check 3rd tool call
```

 ● $ cd /Users/muaddib/dev/frigolite && go test ./... 2>&1 | tail -100 (timeout 60s)
 elapsed 37.2s


 ● $ cd /Users/muaddib/dev/frigolite && go test ./... -v 2>&1 | grep -E '(FAIL|--- FAIL|PASS|ok)' | tail -80 (timeout 60s)
 elapsed 37.2s


 ● $ cd /Users/muaddib/dev/frigolite && find . -name "*_test.go" | sort (timeout 10s)
 elapsed 37.2s
```

## ESC: hard stop for ALL ongoing activities (global)
ESC is a hard stop — globally, in every mode, with no exceptions. This is currently not the case.

Pressing ESC must immediately stop, in normal chat, goal mode, orchestrator/swarm runs, and any other mode:
- every ongoing tool call (bash/pty/exec — including multiple concurrent tool calls from a single turn, sub-agent runs, background exec started by the turn),
- the in-flight provider stream,
- the goal driver loop (no further continuation turns launched),
- orchestrator/swarm runs and any queued continuations/steering,

returning control to the input line at once. Today ESC interrupts the agent turn (agentMgr.Interrupt) + pty cleanup + bg stop, but ongoing tool calls from the current batch and other concurrent activities keep running to completion, and the goal driver launches the next turn.

## Mascot/logo redraw:
The mascot/logo is sometimes redrawn mid-session — run a review of the TUI render path to identify what triggers these redraws. The header/mascot should render once and stay stable; any re-emit of those rows points to a differential-rendering invalidation bug.

Recent regression — repro correlation: occurs with tool calls and after a terminal tab switch (macOS). Tab switch fires no SIGWINCH; suspect transient wrong values from the per-frame `terminal.Size()` re-query (renderNow) triggering the width-change scrollback-reset path, and/or tool-widget height shrink→regrow re-entering the scroll paths.

Additional detail (user, 21 jul): occurs after a tab switch DURING tool calls. The mascot should be much higher in the scrollback history — it seems the redraw does not draw the active view in the correct ORDER (stale/mis-ordered rows rather than a simple re-emit). This issue existed before but did not surface until the recent fixes (21 jul) — or something else changed that made it more obvious. Check interplay with the session-3 tool-widget elapsed/start-time fixes and any render-order change landed that day (git log around 2026-07-21 touching tui/ and tools/*_renderer.go).

## Terminal title animation does not work
The terminal window title animation (hexagon-black startup transition + working animation, see `internal/app/title.go` titleController) does not play. May be related to the startup delay (animation frames starved while the main goroutine is blocked on startup I/O — see Start-up item).

## Session: slow commands need an "executing xyz..." placeholder
Session feels slow — every command must immediately show an "executing xyz..." placeholder so the user knows something is happening, then replace it with the result when done. Applies to all /commands (e.g. /session, /quota, /config, ...): no silent gap between submit and first visible feedback.

## Session command: list ordering, filtering, and timestamps
The /session picker list is wrong on three axes:
- Ordering: most recent session must be on TOP and be the first/default selection (today it isn't).
- Filtering: the list must not contain sessions without any actual model turn (empty/no-turn sessions pollute the picker).
- Timestamps: each entry must show the date/time — date only when the session is NOT from today (today → time only); time format hh:mm; append seconds (:ss) ONLY when needed to disambiguate duplicate hh:mm entries.

## Stats: cache write is always 0
The cache-write figure in stats (/stats and/or the footer) is always 0. Review whether this is correct (provider never reports cache-write tokens) or a bug (we fail to parse/plumb it). If it is legitimately always 0, hide/remove the field when 0 instead of showing a permanent zero.

## Tool call loop detector: false positives
Again a tool call loop detected incorrectly — evidence: /Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260721-233247.zip. Review the cause and find a detection strategy that catches real runaway loops but limits false positives. Characterization of a TRUE loop: it goes endlessly and repeats the same couple of tool calls over and over (same tool + same arguments in a tight cycle). Legitimate work often repeats similar calls (e.g. go test runs with different grep filters, re-reading a file after an edit) and must NOT trip the detector.
