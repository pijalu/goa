# B51 — agent-mcp `mcp` tool invisible in /config and not seen by the agent

Closed: 2026-11-01

## Report

The agent could not see or use the `mcp` tool (agent-mcp bridge), and
/config offered no way to find or re-enable it. Verified chain: the home
config carried an explicit `tools.enabled.mcp: false`, the project layer
omits the key, the merged cascade keeps `false`, `registerAgentMCPTool`
never registers `AgentMCPTool`, and the tool is absent from the model's
tool set AND the tool_search deferred catalog. Every re-enable surface
then failed independently: no `mcp` entry in `tools.ConfigurableTools()`
(invisible in /config, `/tools:mcp:on` rejected), no `mcp` builder in
`runtimeToolBuilders` (a toggle could never build it at runtime), and no
`tools.enabled.mcp` key in `configSetters` (`/config set` failed).

The 2026-11-01 audit extended the scope: `schedule`
(schedule_create/delete/list) and `session`
(session_search/session_event_read) were missing from ALL THREE surfaces,
`configSetters` hardcoded only `tools.enabled.goal|lsp`, and
`ApplyToolToggle` could not toggle a family — one config flag gating N
tools had no lifecycle. Additionally, the docs deliverable audit found 13
tools with no `### `name`` section in docs/TOOLS.md (the /docs fallback
and the model-facing reference), three stale headings (`goa_command` for
`goa`, `terminal` for `terminals`, `read_media` for `read_media_file`),
and five `ConfigurableTools()` Default flags contradicting the embedded
default config (pre-B46 values for python/run_code/terminals/todo_list;
mcp inverted) — the /config and /docs "default" labels lied about a fresh
install.

## Resolution

- **Registry metadata.** `ConfigurableTool` gained `Members []string`
  (nil = single tool) with `MembersOrSelf()`/`FamilyMembers()`.
  `ConfigurableTools()` now includes `mcp` (Default:true), `schedule`
  (Members: schedule_create/delete/list) and `session` (Members:
  session_search/session_event_read).
- **Family-aware lifecycle** (core/commands/tool_lifecycle.go): enable
  builds+registers every member through the host factory with ONE
  `refreshToolRegistry` push; worst outcome wins (restart-required if ANY
  member fails); disable unregisters every member so teardown hooks fire
  per member. Single-member tools behave exactly as before.
- **Runtime builders** (internal/app/commandcontext.go): `mcp`,
  schedule_create/delete/list (wired to the schedule store), and
  session_search/session_event_read (session store). `scheduleStore` is
  now copied into the `subsystems` struct so the factory can reach it.
- **Generic setter + completions.** `setConfigField` routes ANY
  `tools.enabled.<configurable>` through `setToolEnabled` (ask_user_question
  inversion and smartsearch special case included) and handles
  `tools.smartsearch.enabled`; unknown tool names are rejected loudly.
  `tools.enabled.*` completions derive from `ConfigurableToolNames()`
  instead of the hardcoded `goal` key.
- **Defaults mirror the shipped config.** ConfigurableTools Default flags
  corrected to match default.yaml (python/run_code/terminals → false,
  todo_list → true, mcp → true); new total mirror test
  (config/tools_default_mirror_test.go) fails for ANY future drift,
  reflection-driven over the ToolEnabledConfig yaml tags. The stale
  registry tests asserting the pre-B46 defaults were fixed to pin the
  shipped state.
- **Documentation coverage (model-facing).** docs/TOOLS.md gained the 13
  missing sections (tool_search, run_skill, run_code, goal, delegate_to,
  request_review, schedule_create/delete/list, session_search/
  session_event_read + the `goa` rename), the mcp section now carries the
  full autonomous connect-and-use workflow (config_add → list_servers/
  list_tools → call_tool/read_resource/get_prompt, daemon auto-start,
  /docs mcp pointer), and the two registry-less fallback lists use the
  real `goa` name. New pinning tests (core/docengine_toolsdoc_test.go)
  fail when any registerable tool lacks a TOOLS.md section (exact
  backtick-name match, so stale names fail too) or when the mcp section
  loses the autonomous-workflow vocabulary.

## Validation

- RED observed: registry names test (mcp/schedule/session absent), factory
  builder coverage (members not built), setter test (unknown key),
  family toggle tests (silent no-op on family names), mirror test (5
  Defaults wrong), doc-coverage test (13 sections missing, 2 stale names).
- GREEN: tools, config, core, core/commands, internal/app suites; goal
  verifyCommand `go test -count=1 -race -timeout 600s ./tools/...
  ./core/commands/... ./internal/app/... ./config/... && go vet ./...`;
  gates run separately (vet, staticcheck, gocognit -over 15, gocyclo
  -over 12) with only pre-existing warnings unrelated to the change
  (noted: provider TestResolveStartPlanAPIKey 23, core GoalDriver.Drive
  21/13, core executeRunner 16, app initGoalSystem 16, startplan
  devicemid_test 13).
- Live-path coverage: `/tools:mcp:on` with `tools.enabled.mcp: false`
  config registers a live AgentMCPTool and pushes it to the running agent
  without restart; schedule/session family toggles register/unregister
  every member; every configurable tool either registers or reports the
  documented restart (smartsearch only), asserted against the REAL host
  factory.

## Residual risk

- `internal/filefind` TestComplete_AbsoluteOutsideRoot/fd_rescue proved
  load-flaky under the full-suite race run (passes standalone with and
  without -race); recorded as B54, unrelated to this change.
