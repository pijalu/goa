# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
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

---

## python tool `re` module missing DOTALL / MULTILINE / VERBOSE flags — FIXED 2026-08-03 (pending archive)

- **Observed** (2026-08-02, parsing `internal/parse/sql_tables.go` in the
  `python` tool REPL):
  ```
  ✗ python
  >>> import re
  ...
  ... m = re.search(r'var yyRuleInfoNRhs = \[\]int\{(.*?)\n\}', src, re.DOTALL)
  ...
  Error: [python error: execution_error]
  Traceback (most recent call last):
    File "<python>", line 6, in <module>
  AttributeError: 'module' has no attribute 'DOTALL'
  ```
  `re.S`, `re.M`, `re.X` (and their long names) fail the same way; only
  `re.I` / `re.IGNORECASE` exist.
- **Localization**: `internal/python/stdlib/re.go` —
  - Flag bits reserved but never assigned: lines 16-21 (`reFlagIgnoreCase = 1
    << iota` followed by three reserved slots for M/S/X).
  - Module `Globals` registers only `"I"` / `"IGNORECASE"` (~line 116); no
    `S`/`DOTALL`, `M`/`MULTILINE`, `X`/`VERBOSE` → the AttributeError above.
  - `compileRegexp` (~line 513) maps only IGNORECASE to an inline `(?i)`
    prefix; no other flag handling.
  - The module doc already admits it: "Unsupported in the first pass: M
    (MULTILINE), S (DOTALL), X (VERBOSE)".
- **Fix plan**:
  1. Assign the reserved bits (MULTILINE `1<<1`, DOTALL `1<<2`, VERBOSE
     `1<<3`) and register both short and long names in the module `Globals`
     dict.
  2. `compileRegexp`: build one combined inline prefix from the flags —
     IGNORECASE→`i`, DOTALL→`s` (`.` matches `\n`), MULTILINE→`m` (`^`/`$`
     match at line boundaries). RE2 supports all three.
  3. VERBOSE has no RE2 equivalent (`(?x)` unsupported): either preprocess
     the pattern (strip unescaped whitespace and `#`-to-EOL comments outside
     character classes) or reject with a clear `ValueError` stating the
     limitation. Pick one, implement it, and document it in the module doc
     and the TOOLS.md python section.
  4. Flag combination via `|` (e.g. `re.S | re.I`) must work — bit tests
     already support it once the bits exist.
  5. Update the module doc string (drop "Unsupported in the first pass" for
     the implemented flags).
- **Test approach**: table-driven tests in `internal/python/stdlib/re_test.go`:
  - `re.DOTALL` / `re.S` attribute access returns the constant (regression
    test for this exact report).
  - `search(r'a.b', 'a\nb', re.DOTALL)` matches; without the flag it doesn't.
  - `findall(r'^b', 'a\nb', re.MULTILINE)` matches `b` after the newline.
  - Combined `re.S | re.I` semantics in one call.
  - VERBOSE per the chosen implementation (strip-and-match or documented
    error).
  - All existing `re` tests keep passing.
- **Validation steps**:
  1. `go test ./internal/python/... -count=1 -race` green.
  2. Interactive validation per guideline 5: run the `python` tool in the TUI
     and re-execute the reported snippet (`re.search(..., re.DOTALL)`); it
     must return a Match instead of AttributeError.
  3. Quality gates (run separately): `go vet ./...`, `staticcheck ./...`,
     `gocognit -over 15 .`, `gocyclo -over 12 .`,
     `go test -count=1 -race -cover ./...` — no new violations.
- **Workaround until fixed**: inline RE2 flags work today, e.g.
  `re.search(r'(?s)var yyRuleInfoNRhs = \[\]int\{(.*?)\n\}', src)`.
- **Resolution** (2026-08-03, goal kind.marten): implemented in
  `internal/python/stdlib/re.go` — bits assigned (M=1<<1, S=1<<2, X=1<<3),
  short+long names registered in Globals, `compileRegexp` emits one combined
  inline prefix `(?ism)`, VERBOSE implemented by preprocessing
  (`stripVerbosePattern`: strips unescaped whitespace + #-comments outside
  character classes; escaped chars verbatim; `Pattern.pattern` still returns
  the original text). Regression: `TestReFlagsDotallMultilineVerbose`
  (RED → GREEN); module doc updated. Full `internal/python/stdlib` + `tools`
  suites pass. Awaiting final quality gates + archive.

---

## Micro-compaction cache gate fails open during the entire first turn — OPEN

- **Observed** (2026-08-02, session export goa-export-20260802-104717.zip,
  kimi-code/k3-256k): a micro-compaction fired at round 58 of the session's
  FIRST turn (context at 50.04%, `min_context_ratio: 0.5` crossed — that part
  is configured behavior). The in-place tool-result truncation busted the
  demonstrably hot provider prefix cache: the next request reprocessed
  51,133 uncached tokens with cache_read collapsing from 113,408 (round 57)
  to 5,376 (round 58); rounds 59+ re-warmed at the shrunk size. This is the
  exact cache-churn the `cache_miss_threshold: 1h` gate exists to prevent
  (compaction.go:63-71).
- **Localization**: `internal/agentic/compaction.go` —
  - `microCompactForced` defers mutation only when `contextRatio <
    deferCeiling && !a.cacheAssumedCold()`.
  - `cacheAssumedCold()` (lines 106-116) returns true whenever
    `lastTurnEnd.IsZero()` — "no previous turn has completed yet (first turn
    / fresh resume)". `lastTurnEnd` is written only in `finishProcessing`
    (turn END), so the cache is presumed cold for the ENTIRE first turn:
    every per-round gate check in turn 1 (agent_streaming.go:165) sees it
    cold even though the cache has been hot since round 2.
  - The presumption is only sound for the session's first request; by round
    58 it is stale and the gate fails open.
- **Fix plan**: make the cold presumption expire once the session has
  evidence of a warm cache. Concretely: treat the cache as hot when any
  completed request in this agent observed `cache_read_tokens > 0` (track a
  `cacheWarmObserved` bool, set under a.mu where token_stats are recorded),
  OR expire the first-turn presumption after the first completed stream
  round (lastRoundEnd rather than lastTurnEnd). Prefer the observed-cache-hit
  signal: it also hardens fresh-resume cases where the provider cache is
  actually warm (short resume gap). Keep `lastTurnEnd` idle-gap logic for
  later turns unchanged.
- **Test approach** (`internal/agentic/compaction_test.go` /
  `agent_compression_cache_gate_test.go`):
  - Regression: first turn, round 2+, usage ≥ min_context_ratio, provider
    reports cache_read > 0 on round 1 → micro compaction must DEFER (no
    EventCompact, history untouched).
  - First turn with no cache hits reported (cache_read == 0) → compaction
    may still run (genuine cold cache).
  - Manual `/compress` (force) still bypasses the gate.
  - Idle ≥ threshold between turns → compaction runs (existing behavior
    preserved).
- **Validation steps**:
  1. `go test ./internal/agentic/ -run 'Compaction|Compression' -count=1
     -race` green, then full package suite.
  2. Re-run a long single-turn session against a cache-reporting provider
     and confirm no `compact` event fires while cache_read > 0 below the
     deferral ceiling.
  3. Quality gates (separately): `go vet ./...`, `staticcheck ./...`,
     `gocognit -over 15 .`, `gocyclo -over 12 .`,
     `go test -count=1 -race -cover ./...` — no new violations.
- **Note**: unrelated second cache miss in the same export (request at
  10:45:39, full cold) is expected provider TTL expiry after a 34-min idle
  gap — no action needed.

---

## Provider prefix-cache bust loop: tool_elision mutates history every round above the 85% deferral ceiling (status bar CM:13) — OPEN

- **Observed** (2026-08-03, session exports goa-export-20260803-094337.zip and
  goa-export-20260803-095438.zip, frigolite project, opencode/deepseek-v4-flash-free,
  200k window): status bar `↑553.6K ↓208.7K 59.2 tok/s CH98.9% CM:13 TC:428
  86.8%/200.0K`. Replaying the CM detector (stats.go:745-748) over
  `session/events.jsonl` reproduces exactly 13 misses, ALL partial-drop type
  (none zero-read): rounds 17, 141, 227, 296, 345, 362, 371, 381, 389, 395,
  403, 408, 410. Signatures:
  - Round 141: cache_read 169088 → 7552 (first elision pass; 7552 = position of
    the session's first big tool payload).
  - Rounds 227-410: a monotonically advancing frontier — drops to 65536, 98304,
    113792, 131072, 141312, 147072, 151552, 155520, 157056, 157824, 159488
    while the cache re-warms to ~164k between busts. Inter-miss gaps shrink
    86 → 2 rounds (accelerating). Each bust reprocesses the whole tail
    (prompt_n spikes 4.7k-45k). CH stayed 98.9% because the cache re-warms
    every round — CM was the only visible signal.
  - Follow-up export logs prove the mechanism: `Context compression triggered:
    94% usage` + `Applied tool_elision to messages before index 947` → `949` →
    `951` ~6s apart (i.e. EVERY round), then `Hard-layer context compression:
    95%` + `Applied selective compression: removed 1 messages`.
  - Contributing session shape: after round 17 the session was essentially ONE
    continuous turn (486 rounds, only 2 continuation Runs across both exports;
    no state_change/end events between rounds 17-415), so per-round compression
    checks (agent_streaming.go:162-165) fired repeatedly mid-turn as usage
    climbed 84% → 95%.
- **Localization**: `internal/agentic/agent_compression.go` —
  - `compressToolElision` → `computeElisionBoundary(histLen, preserve)` (line
    353) returns `histLen - preserve*3`: as history grows by ~2 messages per
    round, the boundary advances by ~2, so `elideToolMessages` rewrites 2 more
    messages in place per pass — busting the cached prefix at the frontier
    every single round while freeing almost nothing (usage stayed 94-95%
    across the logged window: the loop never converges).
  - The cache-hot gate (`cacheAssumedColdForProactive`, compaction.go:129-142)
    is intentionally overridden above `deferralCeiling()` = hard(95) − 10 =
    85% (compression_thresholds.go:84-93): near-full beats cache churn. But
    the per-pass yield is so small that the bust repeats EVERY round instead
    of once with hysteresis.
  - Latent companion defect (cross-ref first-turn gate entry above):
    `cacheAssumedCold` reads `lastTurnEnd`, written only in `finishProcessing`
    (agent.go:1115) — per TURN, not per round. During long single turns (this
    session: ~399 rounds / ~40 min in one turn) "idle" grows while the
    provider is hit every few seconds; past `cache_miss_threshold: 1h` the
    gate would flip "cold" mid-turn and fire BELOW the ceiling, busting a
    provably hot cache. Use last per-round provider-activity time instead.
- **Unresolved anomaly (round 17)**: cache_read 30592 → 7552 at ~8% usage
  (compression cannot fire below min_context_ratio 0.5; TTL expiry would give
  ~0, not a partial 7552). History verified append-only across rounds 15-18
  (prompt totals 30.8k → 34.5k → 36.9k consistent with the 3 appended
  goal-resume messages). Hypotheses: provider-side partial eviction at a block
  boundary (7552 = 29.5×256 blocks), or an unlogged request-shape change at
  the goal paused→active resume (tool set re-registration). Needs a repro:
  paused goal → resume while logging per-request cache_read and the tool-list
  hash.
- **Fix plan**:
  1. Give tool_elision hysteresis: when it must fire above the ceiling, elide
     by TOKEN BUDGET down to a target ratio (e.g. hard − 20 ≈ 75%) instead of
     "everything before len − preserve*3", so one bust buys many rounds of
     headroom. Alternatively escalate straight to selective/summary
     compression at ≥ ceiling and stop nibbling.
  2. Track last provider-request completion per round (e.g.
     `lastRoundActivity` set where token_stats are recorded) and use it in
     both cache gates; `lastTurnEnd` stays for inter-turn idle only.
  3. Round-17 anomaly: add a one-line debug log of prev/curr cache_read plus
     tool-list hash per request so the next export can discriminate
     eviction vs request-shape change; attempt paused→resume repro.
- **Test approach** (`internal/agentic/agent_compression_test.go`,
  `compaction_cache_test.go`, `internal/app/stats_cm_test.go`):
  - Elision convergence: scripted token_stats feed driving usage to 94%;
    assert elision brings estimated usage below the target in ONE pass and
    does not re-fire while usage < trigger (no per-round EventCompact spam).
  - Gate: fake clock advancing >1h WITHIN a single turn while rounds keep
    completing → cache must stay "hot" (no compression below the ceiling).
  - CM replay: fixture reproducing this session's cache_read series (drop
    169088→7552, then advancing floors) must still count exactly 13 misses
    (detector unchanged); after the fix, the same simulated workload yields
    ≤2 misses.
- **Validation steps**:
  1. `go test ./internal/agentic/ ./internal/app/ -count=1 -race` green.
  2. Long single-turn session against a cache-reporting provider at >90%
     usage: count `Applied tool_elision` lines per minute — must drop from
     ~10/min to ≤1 per several minutes; CM counter must not climb steadily.
  3. Quality gates (separately): `go vet ./...`, `staticcheck ./...`,
     `gocognit -over 15 .`, `gocyclo -over 12 .`,
     `go test -count=1 -race -cover ./...` — no new violations.

---

## Model imitates the `[elided]` tool-call placeholder: 10 invalid tool calls in one session — OPEN

- **Observed** (same export goa-export-20260803-094337.zip): 10 `tool_call`
  events carry the literal arguments `{"arguments": "[elided]"}`
  (evIdx 56785, 57372, 92340, 92982, 93974, 117555, 117561, 120442, …). Every
  one errors and burns a round: `bash` → `[bash error: missing_command] No
  command provided`, `edit` → `[edit error: missing_path] No 'path' provided`.
  The model saw elided assistant tool calls in history
  (`function.arguments == "[elided]"`) and pattern-copied the placeholder as
  its own call arguments — elision actively teaches an invalid call shape.
- **Localization**: `internal/agentic/agent_compression.go:361-377`
  `elideToolMessages` — `msg.ToolCalls[j].Arguments = "[elided]"` leaves a
  LIVE tool_call block in history whose arguments are a placeholder string.
  (Tool results become `"[tool result elided]"`, line 374 — inert, but the
  call side is the imitable one.)
- **Fix plan** (pick one, implement fully):
  1. Convert elided assistant tool_call blocks to a plain-text note inside the
     assistant message content (e.g. `[earlier call to bash elided]`) so no
     invocable-call exemplar remains; or
  2. Replace the call+result pair with a single compact user/system note; or
  3. Use a schema-valid inert stub per tool (e.g. bash `{"command": "true"}`)
     so imitation is harmless. Must round-trip through migrateMessage and the
     OpenAI/Anthropic serializations.
- **Test approach** (`internal/agentic/agent_compression_test.go`):
  - After elision, `buildProviderHistory` output contains NO tool_call whose
    arguments equal `"[elided]"` (or the chosen stub is schema-valid for the
    named tool — validated against the tool's JSON schema).
  - End-to-end-ish: history with elided calls → serialize via the OpenAI
    provider path → assert no `arguments:"[elided]"` in the payload.
  - Existing elision tests (token reduction, boundary) keep passing.
- **Validation steps**:
  1. `go test ./internal/agentic/ -run 'Elision|Compress' -count=1 -race`.
  2. Interactive: run a session past the trigger, then inspect the next
     request's debug log (`Provider context`) for placeholder-free calls.
  3. Quality gates as above, run separately.

---

## python tool (gpython) CPython-parity gaps: `enumerate(start=)` kwarg and `json.dumps(indent=int)` — FIXED 2026-08-03 (pending archive)

- **Observed** (export goa-export-20260803-095438.zip, issue "python errors";
  the model's script hit gpython):
  ```
  Error: [python error: execution_error]
  Traceback (most recent call last):
    File "<python>", line 4, in <module>
  TypeError: 'enumerate() does not take keyword arguments'
  ```
  Trigger: `for i, line in enumerate(data[2286:3930], start=2287):` — valid
  CPython (`enumerate(iterable, start=0)` accepts `start` as keyword).
  Reproduced live in goa 2026-08-03 (this analysis session): same TypeError
  for `enumerate(["a","b"], start=5)`; additionally `json.dumps(obj, indent=1)`
  fails with `TypeError: 'dumps() indent must be str or None, not int` —
  CPython accepts int indent (spaces) since 3.2 and it is the idiomatic form.
  Each gap costs the model error turns mid-task; they are the same class as
  the OPEN `re` flags entry above (model writes CPython-idiomatic code,
  gpython rejects it). Also hit live while preparing this entry:
  `open(path).readlines()` → `AttributeError: "'file' has no attribute
  'readlines'"` — same parity-gap class (workaround: `.read().split("\n")`).
- **Localization**:
  - `enumerate`: dependency `github.com/pijalu/gpython v0.3.0` (go.mod:40) —
    `py/enumerate.go:40` `EnumerateNew` calls `UnpackTuple(args, kwargs,
    "enumerate", 1, 2, …)`; `py/args.go:634-637` `UnpackTuple` returns
    `TypeError "%s() does not take keyword arguments"` for ANY non-empty
    kwargs. gpython has kwargs-aware parsing (ParseTupleAndKeywords in
    py/args.go) — EnumerateNew just doesn't use it. NOTE: other builtins using
    UnpackTuple with CPython-kwarg signatures have the same latent gap; fixing
    enumerate via the kwargs-aware path sets the pattern.
  - `json.dumps`/`dump`: `internal/python/stdlib/json.go:72-80` —
    `parseIndent` only accepts str via `compat.AsString`; an Int indent hits
    the `indent must be str or None` TypeError at line 78. Fix: accept
    `py.Int` → `strings.Repeat(" ", n)` (CPython: n<0 behaves as 0 — newlines
    only, no spaces); keep str/None paths; `dump` shares the helper.
- **Fix plan**:
  1. gpython fork: switch `EnumerateNew` to kwargs-aware unpacking allowing
     optional kw `start` (Int via `Index`); reject unknown kwargs with the
     standard message. Bump the module version and update go.mod/go.sum here.
  2. This repo: extend `parseIndent` in json.go for `py.Int` (and bool?
     CPython rejects bool indent — verify and match).
  3. Document the supported signatures in the TOOLS.md python section
     (cross-ref the `re` entry's doc step).
- **Test approach**:
  - gpython: `py/enumerate_test.go` — `enumerate(x, start=3)` by keyword and
    positionally, default 0, `start=-1`, unknown kwarg → TypeError.
  - This repo `internal/python/stdlib/json_test.go`: `dumps(obj, indent=2)` →
    two-space output; `indent=0` → newlines only; `indent=-1` ≡ `0`; str
    indent still works; `dump` parity.
  - Regression via the tool layer: run both reported snippets through the
    `python` tool and assert success.
- **Validation steps**:
  1. `go test ./internal/python/... ./tools/ -count=1 -race` green.
  2. Interactive per guideline 5: in the TUI run
     `enumerate(data[5:10], start=6)` and `json.dumps({"a":1}, indent=2)` —
     both must succeed.
  3. Quality gates as above, run separately.
- **Resolution** (2026-08-03, goal kind.marten):
  - `json.dumps`/`dump`: `internal/python/stdlib/json.go` — new `indentValue`
    helper accepts str (verbatim), int (spaces, clamped at 0) and bool (acts
    as int, CPython parity) via `compat.AsInt`; error message now
    "indent must be str, int or None". Regression:
    `TestJsonDumpsIntIndent` (RED → GREEN).
  - `enumerate`: fixed in the gpython fork `~/dev/gpython` (main), commit
    `b042729` — `EnumerateNew` now uses `ParseTupleAndKeywords` with kwlist
    `[iterable, start]`; unknown kwargs still TypeError. Regression:
    `py/enumerate_test.go` (RED → GREEN), full fork suite green.
  - Integration: `go.mod` carries `replace github.com/pijalu/gpython =>
    /Users/muaddib/dev/gpython` until the fork publishes a release
    (ACTION for maintainer: push fork, tag v0.3.1+, `go get` it, drop the
    replace). Tool-layer regressions:
    `TestPythonTool_Execute_EnumerateStartKwarg`,
    `TestPythonTool_Execute_JsonIntIndent` — both green.
  - Note: live in-session `python` tool still runs the pre-fix binary; the
    harness tests exercise the same VM + stdlib path. Awaiting final quality
    gates + archive.
---

## Runaway-loop guardrail bricks the session: `loopStopped` never resets, pause/resume re-enters the same loop — OPEN

- **Observed** (2026-08-03, exports goa-export-20260803-112430.zip and
  ...-112729.zip, frigolite goal-mode session, deepseek-v4-flash-free):
  while doing legitimate iterative work (the model introduced a `dbdb` typo,
  noticed it, and fixed it), the framework interrupted with
  "Goal paused by the system — Paused after detecting a runaway response
  loop" THREE times; the user resumed each time; the third resume ended with
  `[error] The LLM request failed. session stopped due to a runaway loop;
  please review the conversation and retry` — a dead session (only recovery:
  abandon it). agent.log (export 112729):
  - `11:21:24 [WARN] Loop guardrail: assistant message repeated 1 time(s)`
    (warning hint injected),
  - `11:23:26 [WARN] Loop guardrail: assistant message repeated 2 time(s)`
    (latch + error),
  - `[stats] turn 7..12` all with byte-identical `in=101757 out=189` across
    11:23:26 → 11:26:39 — identical token counts across consecutive turns
    are IMPOSSIBLE for real calls with growing history (each turn appends),
    so either the same EventTokenStats was delivered repeatedly (stats/event
    duplication) or the stream produced true zero-progress repeats; both
    variants indicate the loop signal itself is unreliable.
- **Localization** (`internal/agentic/agent.go`):
  - `checkProgressLoop` (agent.go:1148-1210) hashes the last assistant
    message per `processTurn`; on repeatCount==2 it sets
    `a.loopStopped = true` (line 1203) and errors.
  - `a.loopStopped` is NEVER reset (verified by grep: no assignment to false
    anywhere) — once latched, `processTurn` (line 1165) returns the terminal
    error forever: the error text says "review the conversation and retry"
    but retrying hits the same latch. No user action (resume, new prompt,
    ESC) can clear it.
  - The guardrail compares only the last assistant message; if a turn ends
    without producing a NEW assistant message (stream error, retry, pause),
    the next `processTurn` compares the SAME message against itself → a
    false strike with zero actual repetition.
  - The goal driver re-sends the byte-identical continuation prompt on
    resume (core/goal_driver.go:78), so pause → resume deterministically
    re-enters the same conditions that tripped the guardrail.
- **Fix plan**:
  1. Reset `loopStopped`/`assistantRepeatCount` when a genuine new user
     message starts a Run (human input, not a driver continuation), and make
     the latch auto-expire after N minutes — a guardrail must never
     permanently brick a session.
  2. Only count a strike when the completed turn produced a NEW assistant
     message (stamp messages with the turn/round id; skip the check when
     `lastAssistantMessage` predates the current turn) — kills the
     error-path false positive.
  3. Investigate the identical-stats turns: log prev/curr token_stats
     identity (round, prompt_n, cache_read) and dedupe repeated
     EventTokenStats for the same round before incrementing turn counters.
  4. Driver side: on runaway pause, do not auto-retry the identical
     continuation; require user input or inject a varied diagnostic prompt.
- **Test approach** (`internal/agentic/agent_test.go`,
  `core/goal_driver_test.go`):
  - Latch recovery: script a provider that repeats one assistant message 3×
    → guardrail errors → then a NEW user-message Run must clear the latch
    and proceed (regression for the bricking).
  - False positive: fail a turn mid-stream (no new assistant message), then
    succeed — repeat count must NOT increment across the failed turn.
  - Stats: feed duplicate EventTokenStats for the same round → turn counter
    increments once.
- **Validation steps**:
  1. `go test ./internal/agentic/ ./core/ -count=1 -race` green.
  2. Interactive: force a loop (stub provider), hit the latch, then send a
     fresh prompt in the same session — it must recover without restart.
  3. Quality gates per guideline 6, run separately.

---

## /compress:summarize rejected by provider: elided tool-call arguments are not valid JSON — OPEN

- **Observed** (2026-08-03, export goa-export-20260803-112729.zip): the user
  ran `/compress` (11:26:36, `strategy= force=true` →
  `Applied tool_elision to messages before index 112`) and then
  `/compress:summarize` (11:27:02, `strategy=summarize force=true`), which
  failed:
  `compression failed: summarization failed: {"error":{"type":
  "invalid_request_error","message":"Error from provider (Console):
  Upstream request failed: [400] Assistant tool call function.arguments
  must be valid JSON."}}`
  The summarization snapshot (`a.history` minus System) contained the
  assistant tool calls elided 26s earlier — their `function.arguments` is
  the bare string `[elided]`, which is not JSON, and the provider (zen →
  deepseek) validates arguments. Compaction — the feature meant to rescue a
  bloated context — is itself broken by elision. NOTE: hundreds of normal
  rounds after earlier elisions returned 200, so normal requests pass while
  the summarize request fails; candidate difference: the summarize context
  carries no `tools` array (`summarizeHistory`,
  agent_compression.go:84-100, builds `provider.Context{Messages,
  SystemPrompt}` without Tools) — the validator may only run on that shape.
- **Localization**: `internal/agentic/agent_compression.go:361-377`
  `elideToolMessages` writes `msg.ToolCalls[j].Arguments = "[elided]"` (not
  JSON); `internal/agentic/agent_compression.go:84-100` `summarizeHistory`
  ships the snapshot verbatim via `migrateMessages`.
- **Fix plan**: shared with the "[elided] placeholder imitation" entry —
  pick the option that also keeps provider-bound arguments JSON-valid:
  1. Preferred: at serialization time (provider-bound path), convert elided
     assistant tool_call blocks into a plain-text note in the assistant
     content and drop the matching ToolRole result messages (pairing must
     stay consistent); or
  2. Replace call+result pairs with one compact note; or
  3. Emit a schema-valid inert stub (valid JSON per tool).
  All three make summarize work again; 1-2 also remove the imitable
  exemplar. Additionally: before streaming the summarize request, log the
  count of elided-call blocks in the snapshot so future 400s are
  diagnosable from agent.log alone.
- **Test approach** (`internal/agentic/agent_compression_test.go`):
  - After elision, `migrateMessages(snapshot)` yields NO assistant tool_call
    whose arguments are not valid JSON (parse every arguments string).
  - Summarize-path regression: build history with elided pairs, run
    `summarizeHistory` against a stub provider, assert the outbound
    messages contain no `"[elided]"` arguments and tool call/result pairing
    is consistent.
  - Existing elision/summarize tests keep passing.
- **Validation steps**:
  1. `go test ./internal/agentic/ -count=1 -race` green.
  2. Interactive: `/compress` then `/compress:summarize` against
     deepseek-v4-flash-free on opencode — both must succeed.
  3. Quality gates per guideline 6, run separately.

---

## python tool `re` module missing `finditer` (module-level and Pattern) — OPEN

- **Observed** (2026-08-03, export goa-export-20260803-113756.zip; the
  model's script):
  ```
  Error: [python error: execution_error]
  Traceback (most recent call last):
    File "<python>", line 9, in <module>
  AttributeError: "'module' has no attribute 'finditer'"
  ```
  Trigger: `re.finditer(r'func (flatten|formatValue|valStr|toStr)\(', src)`
  — standard CPython. `re.go` registers compile/search/match/findall/sub/
  split/escape but no `finditer`; `Pattern.methods` likewise has no
  `finditer`. Same parity-gap class as the (now fixed) flags entry and the
  enumerate/json entry — every missing CPython staple costs the model error
  turns and pushes it to slower `findall`-based rewrites.
- **Localization**: `internal/python/stdlib/re.go` — module `Methods` list
  (line ~90) and `Pattern.methods` (line ~52) lack `finditer`;
  implementation can reuse `reFindAll`/`patternFindAll` but yield `Match`
  objects instead of strings.
- **Fix plan**:
  1. Add `reFinditer(args)` + `patternFinditer` returning a LIST of `Match`
     objects (Py3.4-subset pragmatism: a list satisfies `for m in
     re.finditer(...)` and `list(...)` usage; document the difference in the
     module Doc). A lazy iterator type is optional polish — gpython has
     EnumerateIterator as a template if desired.
  2. Register both, plus mention in module Doc.
- **Test approach** (`internal/python/stdlib/re_test.go`, pyCode harness):
  - `re.finditer` yields Match objects: group(0), start(), end() correct
    for a 3-match pattern.
  - `p.finditer(text)` parity; empty case returns []; flags combine
    (`re.finditer(r'a', 'A a', re.I)` → 2 matches).
  - Regression via tool layer: the reported snippet runs without error.
- **Validation steps**:
  1. `go test ./internal/python/... ./tools/ -count=1 -race` green.
  2. Interactive: `for m in re.finditer(r'\d+', 'a1 b22'): print(m.group(0))`.
  3. Quality gates per guideline 6, run separately.

---

## Thinking-mode 400: `reasoning_content` not passed back for provider-proxied DeepSeek models (opencode zen) — OPEN

- **Observed** (2026-08-03, export goa-export-20260803-113756.zip, provider
  opencode/zen, model deepseek-v4-flash-free • high):
  ```
  Error: 400 - Error from provider (Console): Upstream request failed:
  [invalid_request_error] The reasoning_content in the thinking mode must
  be passed back to the API.
  ```
  → "Goal paused by the system — Paused after provider request error".
  The model runs with thinking enabled; after the first turn carrying
  reasoning_content, the next request omitted it and the upstream rejected
  the whole call.
- **Localization**: the compat flag exists —
  `OpenAICompletionsCompat.RequiresReasoningContentOnAssistantMessages`
  (internal/agentic/provider/compat.go:23) — and both serializers honor it
  (internal/agentic/provider/protocol/openai_completions.go:280,
  internal/agentic/provider/openai/convert.go:159: inject
  `reasoning_content` on assistant messages when set). Detection is the
  gap: `compat_detect.go:55` computes
  `isDeepSeek = matchesProviderOrURL(p, url, "deepseek", "deepseek.com")`
  from PROVIDER NAME / URL only (line 106 sets the flag from it); the
  session's provider is `opencode` (URL opencode.ai/zen) so isDeepSeek is
  false and the flag stays unset even though the MODEL is deepseek-*. The
  models registry (models/models.go:157,495) knows `deepseek-v4-flash` but
  not the zen-specific `deepseek-v4-flash-free` id.
- **Fix plan**:
  1. Extend the fingerprint to also match the MODEL id (substring
     "deepseek" case-insensitive) so proxied DeepSeek variants inherit the
     flag; keep provider/URL matching. Verify the same gap for other
     model-keyed flags (isZai, isMoonshot, etc.) while touching this.
  2. Confirm which serialization path serves opencode in the current build
     (protocol/ vs openai/ packages both exist — see the legacy-migration
     comments in provider_migrate.go) and cover it with tests.
  3. Optional defense-in-depth: when a 400 message matches
     "reasoning_content", retry once with the flag forced on and persist it
     for the session (mirrors the recorded-usage auto-detect precedent).
- **Test approach** (`internal/agentic/provider/compat_detect_test.go`,
  `protocol/openai_completions_test.go`):
  - fingerprint(provider=opencode, url=opencode.ai/zen,
    model="deepseek-v4-flash-free") → RequiresReasoningContent=true.
  - Serialization: assistant message with Thinking → outbound JSON carries
    `reasoning_content`; without flag it is absent (pin both behaviors).
  - Registry/detect precedence unchanged for known providers.
- **Validation steps**:
  1. `go test ./internal/agentic/provider/... -count=1 -race` green.
  2. Interactive: opencode + deepseek-v4-flash-free • high, run 3+ turns
     with thinking visible — no 400.
  3. Quality gates per guideline 6, run separately.

---

## Session status 2026-08-03 (mid-fix, goal kind.marten)

- FIXED (pending final gates + archive): re flags (DOTALL/MULTILINE/VERBOSE);
  enumerate(start=) kwarg + json.dumps(indent=int).
- OPEN (scheduled): micro-compaction first-turn gate (analysis done, fix not
  applied); tool_elision cache-bust loop (CM:13); [elided] placeholder
  imitation; runaway-loop guardrail bricking; summarize-400; re.finditer;
  reasoning_content detection.
- Working tree carries the fixes above plus `go.mod` replace →
  /Users/muaddib/dev/gpython (drop after fork release b042729).
- Evidence: exports under /Users/muaddib/dev/frigolite/.goa/exports/
  (20260803-094337, -095438, -112430, -112729, -113756).
