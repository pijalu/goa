# Fix plan — Hy4 tagged reasoning/tool markers

Source: `bugs.md` item “Hy4 tagged reasoning/tool markers leak into assistant output”.
Evidence: `/Users/muaddib/dev/panelcraft/.goa/exports/goa-export-20261118-081708.zip`.

## Root cause and constraints

- `parseOpenAIStream` forwards content and thinking deltas directly into `ContentBuf` and `ThinkingBuf`; only unsuffixed generic tool markup is recognized later by the agent.
- `Protocol.ParseResponse` currently receives neither resolved model nor variant profile. Dialect selection therefore needs a model/profile-aware response entry point.
- Hy4 handling must be profile-scoped, streaming-safe, and limited to known suffixed tags. It must not globally delete arbitrary XML or disturb native tool calls.

## Implementation

1. Add a `compat.response_dialect` string capability, default off with explicit `none` override, and cover layered merge. Match only OpenAI-completions `hy4-flash-free`; do not enable provider-wide or by broad model-prefix heuristics.
2. Add a model/profile-aware OpenAI response entry point. Runtime supplies `req.Model` and resolved `req.Profile`; existing `ParseResponse` retains standard behavior and other protocols remain unchanged.
3. Implement a stateful Hy4 normalizer with independent content/thinking pending buffers. Hold only possible partial known markers, emit unrelated prose byte-for-byte, handle EOF leftovers, and clean emitted events plus final `ContentBuf`, `ThinkingBuf`, and `stream.Result()`.
4. Parse `<tool_calls:suffix>`, `<tool_call:suffix>`, `<arg_key:suffix>`, and `<arg_value:suffix>` with matching hexadecimal suffixes and closing tags. Preserve multiline values and typed JSON values; never treat arbitrary XML as Hy4.
5. Emit recovered calls only after complete wrapper closure. Keep native IDs, indexes, arguments, and relative call order untouched; avoid native/recovered index collisions. Remove recovered markup before generic auto-heal sees buffers.
6. Route identifiable incomplete recovered calls through the native non-executing diagnostic/recovery path delivered by the preceding fix. Never fabricate executable arguments or execute an incomplete tagged call.
7. Split tokenizer, marker classification, call construction, and argument conversion into small composable helpers within complexity budgets.

## Regression tests

- Exact `.</think:6124c78e>` capture, another suffix, and every byte-split boundary for each marker; concatenated output and final history remain clean and surrounding prose is byte-exact.
- Content and thinking paths, final assistant history, and final protocol result contain no Hy4 control bytes.
- Complete tagged calls execute once; incomplete wrappers execute zero times and produce non-executing diagnostics when identifiable.
- Adjacent and mixed native/tagged calls preserve native IDs, arguments, order, and exactly-once behavior without index collision.
- Unrelated XML, unknown tags, non-hex suffixes, DeepSeek reasoning fields, generic auto-heal, non-Hy4 profiles, and explicit `none` retain existing behavior.
- Profile resolver and layered override tests prove narrow activation.

## Validation and closure

- Run parser and agent regressions RED before implementation, then GREEN with race detector and timeouts.
- Replay export shapes through protocol → agent → real app events; inspect every filmstrip frame and scrollback for marker absence and correct tool widgets.
- Run real PTY replay and one live `opencode-go` / `hy4-flash-free` tool-using turn; record actual terminal evidence. Unavailable live access remains an explicit blocker, never a silent pass.
- Stop every validation server/process and verify ports.
- Run all quality gates separately, record results, archive this plan and bug report, remove the fixed item from `bugs.md`, and commit as `fix(provider): normalize Hy4 streamed control markers`.

## Closure

All implementation and regression items were completed. Real app filmstrip/terminal-emulator replay, real 3-byte-delta PTY replay, and a live `opencode-go` / `hy4-flash-free` native tool turn passed; detailed evidence is in `docs/archive/bugs-20261118-hy4-stream-markers.md`. The final full race/cover suite passed. `go vet` and `staticcheck` passed; complexity commands reported only the documented unchanged baseline findings.
