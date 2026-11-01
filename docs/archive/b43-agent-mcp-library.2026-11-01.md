# B43 — agent-mcp as an embedded MCP bridge library

Closed: 2026-11-01

## Report

Feature request: integrate `github.com/pijalu/agent-mcp` (the shared MCP
daemon ecosystem — warm sessions across processes, TOML config, API-key auth,
JSON envelopes) as a tool with goa, so goa can create skills that connect to
MCP servers without additional tooling. Updating agent-mcp was allowed,
without breaking its existing CLI features.

Survey findings:

- goa already speaks MCP natively (`internal/mcp`, `mcp__<server>__<tool>`
  tools, `/mcp` lifecycle), but those sessions live and die with the goa
  process. agent-mcp is the complementary piece: its daemon keeps sessions
  warm across processes (chrome-devtools/blender/rea skills depend on a
  `pageId` surviving between calls) and owns its own config/auth/registry.
- All agent-mcp logic lived under `internal/` (`internal/cli`,
  `internal/daemon`, `internal/session`, `internal/config`,
  `internal/output`), so nothing was importable from goa (Go's internal
  package rule: `github.com/pijalu/goa` may not import
  `github.com/pijalu/agent-mcp/internal/...`).

## Resolution

### agent-mcp: `client` library package (commits f9ce1be, aaa2eb9)

- New package `github.com/pijalu/agent-mcp/client` extracted from
  `internal/cli`: ops (`Request`/`Op` + `Validate` carrying the CLI's exact
  wording), payload shaping (`Shape`: discovery totals/truncation, metadata
  strip, raw passthrough), discovery filtering, daemon connect (probe,
  auto-start, wait-ready, config-match), error sentinels
  (`ErrUsage`/`ErrMCP`/`ErrInternal`, `RPCFailure`/`SessionFailure`), and a
  `Client` facade (`Do`/`Close`/`ShutdownDaemon`) with injectable test hooks
  (`SpawnDaemon`, `Dial`, `NewManager`).
- `internal/cli` is rewired onto that core through thin private adapters;
  all 132 CLI tests compile unchanged and pass — no flag, message, envelope,
  or exit-code change (byte-identical CLI behavior).
- External-facing embedder API (commit aaa2eb9) because the daemon's config
  and session types stay internal: `ServerEntry` + `UpsertServerEntry`
  (external shape of one MCP server config), `DialSessionFunc` +
  `NewSessionManager` (in-process manager over named servers with a custom
  dial hook), `NewWithManager` (bind a Client to a ready backend, skipping
  config load). This is the seam goa uses.

### goa: `agent_mcp` tool + renderer + skill

- `tools/agent_mcp.go`: deferred tool over the library. Nine actions —
  `list_servers`, `list_tools`, `call_tool`, `list_resources`,
  `read_resource`, `list_prompts`, `get_prompt`, `config_add`, `shutdown` —
  mapped through `AgentMCPBackend` (interface over `*client.Client`) so tests
  inject fakes; daemon notices (auto-start hints) are captured by rewiring
  `Options.Stderr` and appended to results; `ToolError` taxonomy maps
  `ErrUsage`→`invalid_params`, `ErrMCP`→`mcp_error`,
  `ErrInternal`→`internal_error`.
- `tools/agent_mcp_renderer.go`: call header (action + server + target +
  discovery filters) and folded result summaries (server states, list counts
  with totals/truncation, config_add report, shutdown result, MCP content
  text, daemon notice lines). Refactored once to meet the gocyclo ≤ 12
  budget (`RenderCall`/`summarizeAgentMCPPayload` split into small helpers),
  with a table-driven renderer test pinning all shapes.
- Registration: `internal/app/bootstrap_tools.go` gates on
  `cfg.Tools.Enabled.AgentMCP` (opt-in: `tools.enabled.agent_mcp` defaults
  to `false` in `config/configs/default.yaml`); renderer registered in
  `tui/register_renderers.go`; deferred marker in `tools/deferred.go`; the
  `agent_mcp` tool joined `tui/tool_streaming_validation_test.go` (both the
  partial-args case table and the registry-completeness list — the
  completeness test caught the omission in the full race suite).
- `skills/agent_mcp/SKILL.md`: embedded skill (default off, per repo norm)
  documenting discover-before-call usage and how to author MCP-driving
  skills on the tool.

### Dependency wiring

`go.mod` requires `github.com/pijalu/agent-mcp` plus a local `replace`
directive to the development checkout (repo precedent: `gpython` was wired
the same way before its publish). The require pin still points at the
pre-library commit (`c700133`); when agent-mcp is pushed, bump the pin to
`aaa2eb9` and drop the replace — no code change needed, the `client` API is
stable by the tests in both repos.

## Validation

agent-mcp (both commits):

- `go test -count=1 -race -timeout 120s ./client/` — ok (1.566s); library
  tests cover the NoServer round-trip through an in-memory go-sdk MCP
  fixture (echo tool, `mem://` resource, prompt), auto-start flow with a
  real TCP listener standing in for the daemon child, spawn-failure and
  daemon-only paths, config mismatch, unauthenticated-no-auto-start,
  upsert add/update/invalid, per-op daemon stubs, error taxonomy mapping,
  shutdown (absent-idempotent / refused / sessions-closed), child-process
  lifecycle against a real `sh -c` child.
- Full `-race` suite + vet + staticcheck + coverage gate (≥80% for `client`)
  green; all 132 `internal/cli` tests unchanged and green — CLI behavior
  byte-identical.

goa:

- RED caught by the gate: the full race suite failed
  `TestToolStreaming_RegistryIsComplete` ("registry tool \"agent_mcp\" is
  not covered by streaming validation") — fixed by adding the tool to the
  streaming-validation table; `go test -count=1 -race ./tui/` green after.
- Complexity gate caught three NEW gocyclo violations (>12) in the new
  renderer/tool code (RenderCall 14, summarizeAgentMCPPayload 13, Execute
  13); all three refactored under 12, with equivalence pinned by the new
  table-driven renderer tests (22 cases) — all green.
- Final gates, run separately: `go vet ./...` clean; `staticcheck ./...`
  clean; `gocognit -over 15 .` — four pre-existing warnings only, all
  unrelated (provider startplan test, core GoalDriver, executeRunner,
  initGoalSystem); `gocyclo -over 12 .` — two pre-existing warnings only,
  unrelated (startplan devicemid test, core GoalDriver);
  `go test -count=1 -race -timeout 600s ./...` — exit 0, 91 packages ok.
- `tools/agent_mcp_test.go`: 17 fake-backend tests (action dispatch,
  parameter mapping, error classes, notice append/consume, raw passthrough,
  schema validity, config_add argv/env, shutdown). `tools/agent_mcp_live_test.go`:
  live round-trip through the real library + in-process go-sdk MCP server
  (echo tool / mem://hello resource / greet prompt), unknown-server error
  class, tool-error class mapping.
- `skills/agent_mcp_skill_test.go`: skill embedded and default-off.

Commits: agent-mcp `f9ce1be` (client library extraction), `aaa2eb9`
(external embedder API); goa `feat(tools): agent_mcp tool — embedded
agent-mcp MCP bridge` (this commit).
