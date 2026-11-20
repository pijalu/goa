# Fix plan: actionable ask_user_question validation and bounded repeat recovery

**Resolved:** 2026-11-20

## Evidence and diagnosis

Exports `/Users/muaddib/dev/panelcraft/.goa/exports/goa-export-20261120-070908.zip` and `goa-export-20261120-071001.zip` show Kimi `k3.1-pro` emitting malformed native arguments:

- `session/events.jsonl:1666,1713` in the first export: `{"questions":[{}]}` twice.
- `session/events.jsonl:2266,2287,2308,2329` in the second export: `{"questions":[]}` four times.
- `session/events.jsonl:2309,2330`: repeated hard-loop hints did not end ordinary tool-enabled rounds.

Goa parsed the native calls correctly and rejected their invalid payloads. The framework defects addressed here were incomplete corrective guidance, an advertised schema that accepted an empty array, and consecutive-repeat handling that kept issuing nudges instead of converging the turn.

## Item 1: actionable validation and schema

- **Observed:** the schema lacked `minItems`, and malformed-input, empty-array, and missing-question errors named missing fields without showing a complete valid payload.
- **Expected:** reject `{"questions":[]}` under the advertised schema and include `{"questions":[{"question":"Which option?","options":["A","B"]}]}` in every related input error.
- **Plan:** add `minItems: 1`; centralize one complete input hint in `tools/ask/ask_user.go`; reuse it for malformed JSON, an empty array, and a missing question without changing valid-call behavior.
- **Schema-budget adjustment:** the first full race run measured 22,010 default-schema bytes against the existing 22,000-byte ceiling. Shortening the `questions` description from `clarifying questions, each asked separately` to `questions, each asked separately` preserved its meaning and brought the aggregate back under the unchanged ceiling; the ceiling was not raised.

## Item 2: terminate consecutive tool repeats

- **Observed:** with `max_tool_repeat_consecutive=2`, the first two identical invalid calls executed, but later calls received repeated synthetic hints while ordinary tool rounds continued.
- **Expected:** the first call beyond the limit remains protocol-paired but is skipped, receives exactly one hard-loop result, and leads to bounded text-only recovery. A genuine new user turn restores normal tools.
- **Plan:** latch consecutive-repeat recovery when the hard guard fires; consume the latch after tool results are committed and before ordinary convergence/re-streaming; delegate to the existing three-round `runRecoveryStream` bound. Preserve total-repeat, rolling-window, state-aware reset, legitimate second-call, and same-batch pairing behavior.

## Regression tests and RED/GREEN evidence

Initial RED command:

```sh
go test -count=1 -timeout 30s ./tools/ask ./internal/agentic -run 'TestSchema_EmptyQuestionsInvalid|TestExecute_InputErrorsIncludeValidExample|TestConsecutiveRepeat_EntersBoundedTextOnlyRecovery'
```

Observed before implementation:

- `agentic.Validate` accepted the empty `questions` array.
- All three input errors lacked the complete example.
- The repeat regression produced three hard-loop results instead of one and six stream rounds instead of four.

The same focused tests passed after implementation. Final focused package validation also passed:

```sh
go test -count=1 -timeout 60s ./tools/ask ./internal/agentic ./internal/app
```

Coverage includes schema validity, all three corrective errors, two real invalid executions, exactly one synthetic hard-loop result, three ordinary streams plus one `NoTools` recovery stream, and normal tools on the next user turn.

## Actual terminal/filmstrip validation

```sh
go test -count=1 -timeout 60s -v ./internal/app -run '^TestAskUserQuestionRepeatRecoveryFilmstrip$'
```

Both subtests passed using a real `agentic.Agent`, the real `ask.AskUserQuestionTool`, captured production `OutputEvent`s, `App.handleAgentOutputEvent`, and ANSI-free `tui.Filmstrip` output. For each exported payload shape, the rendered output contained:

```text
Error: [ask_user_question error: missing_questions]
Hint: Provide valid JSON with a non-empty questions array; each item requires a non-empty question string. Example: {"questions":[{"question":"Which option?","options":["A","B"]}]}
```

or the corresponding `missing_question` error, followed by exactly one visible guardrail and the recovered assistant response:

```text
[goa-system] Loop guardrail: this exact tool call was repeated 3 consecutive times (limit: 2). Stop repeating the same call. Change your approach or produce a final answer.
Recovered without repeating the invalid question.
```

The test also requires two real errors, three tool calls, and exactly four provider streams. No live model, server, browser, or throwaway process was used.

## Quality gates

Each required command was run separately:

- `go vet ./...` — passed.
- `staticcheck ./...` — passed.
- `gocognit -over 15 .` — no findings in changed code after splitting the new filmstrip test into focused helpers. The command still exits 1 for six pre-existing, unrelated findings: `provider.TestResolveStartPlanAPIKey` (23), `tools.TestAgentMCPCloseRemovesExtractedArtifacts` (23), `app.TestMakeToolFactory_BuildsEveryConfigurableTool` (19), `core.(*AgentManager).executeRunner` (16), `config.TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig` (16), and `app.initGoalSystem` (16).
- `gocyclo -over 12 .` — no findings in changed code after extracting repeat-event/context assertions. The command still exits 1 for pre-existing, unrelated `agentic.alignOneMessage` (13).
- `go test -count=1 -race -cover ./...` — passed across the repository after the schema-description adjustment. The first run caught the related 22,010-byte schema-ceiling regression; `TestTotalToolSchemaJSONCeiling` and the complete race suite passed after the adjustment.

## Resolution

Both items are implemented, regression-tested, filmstrip-validated, documented in `docs/LOOP-DETECTION.md`, and ready to close. No validation servers or processes require cleanup.
