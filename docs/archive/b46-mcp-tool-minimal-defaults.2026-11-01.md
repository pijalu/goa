# B46 — mcp tool: wrong name, off by default, no automatic-connect guidance

Closed: 2026-11-01

## Report

The agent-mcp bridge tool landed (B43) as `agent_mcp`, gated behind
`tools.enabled.agent_mcp` (default false — opt-in), so a fresh install had no
MCP bridge tool at all and nothing to discover via tool search until a config
edit. Nothing told goa how to connect/configure an MCP server automatically
either: `config_add` returned a bare JSON upsert with no next step, and the
model had to already know the daemon's config path to wire a server.

Additionally, the default tool set had drifted from the "keep the default
minimal" principle: python, terminals, and run_code shipped enabled, and the
schedule/session query families had no gate at all. The agreed minimal set is
bash, edit/write/read (ungated core), goals, todos, webfetch, and mcp.

## Resolution

- **Rename the surface.** `tools/agent_mcp*.go|md` → `tools/mcp*.go|md`
  (git mv), schema name/errors `agent_mcp` → `mcp`, renderer title, TUI
  renderer registration key + streaming-validation lists, config yaml key
  `agent_mcp` → `mcp` (field `AgentMCP`, comment notes opt-out), embedded
  skill `skills/agent_mcp` → `skills/mcp` + loader test. Go identifiers keep
  the library-derived names (AgentMCPTool, AgentMCPRenderer) — the public
  name is the registry/config key.
- **Default ON (opt-out).** `config/configs/default.yaml` sets
  `mcp: true`; registration gating already keys off `cfg.Tools.Enabled`,
  so /config and /tools toggles work with no extra code. The embedded skill
  stays default-off per repo norm.
- **Self-service connect.** `mcp.long.md` gained "Connecting a server
  automatically" (config_add stdio/remote → verify with list_tools → call;
  "no restart, no manual TOML editing"); `config_add` now appends an explicit
  next-step hint to its result ("Verify the connection with
  {"action":"list_tools","server":...} before calling its tools."); the
  renderer treats any blank-line tail after a valid-JSON head as accent
  notice lines (`json.Valid` guard replaces the old "auto-starting"
  substring check), so the hint and daemon notices render accent-colored.
- **Minimal default set.** `python`, `terminals`, `run_code` flip to false
  (run_code from true); `todo_list` true (todos are in the set); webfetch
  stays true; NEW `tools.enabled.schedule` and `tools.enabled.session`
  fields gate the schedule tools (subsystems_agent.go) and the session query
  tools (registerSessionQueryTools) at registration — stores still build so
  /schedule-style commands keep their backing state; smartsearch stays
  gated off by `tools.smartsearch.enabled`.
- **Documentation sweep.** docs/TOOLS.md gained a `mcp` tool section
  (default-on, deferred, actions, self-service connect, errors); docs/MCP.md
  gained "Agent-driven connects" pointing at the `mcp` tool;
  docs/CONFIGURATION.md's tools.enabled sample now matches the shipped
  defaults; zero stale `agent_mcp` references outside closed-history records.

## Validation

- RED (observed): config default tests failed against the old defaults
  (mcp absent, python/terminals/run_code on, todo_list off, no
  schedule/session gates); tools schema test failed (`name=agent_mcp want
  mcp`); skills loader test failed (`agent_mcp not in embedded set`).
- GREEN: `go test -count=1 -timeout 120s` per touched package — tools
  (30.1s), config (5.1s), skills (0.44s), tui (3.7s), internal/app (36.6s),
  core/commands (4.2s) — all pass; full race suite green after B48 (see its
  archive).
- Gates run separately: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .`, `gocyclo -over 12 .` — clean except documented
  pre-existing hotspots unrelated to this change (provider
  TestResolveStartPlanAPIKey gocognit 23, core GoalDriver.Drive gocognit 21 /
  gocyclo 13, app initGoalSystem gocognit 16, core executeRunner gocognit
  16, startplan devicemid_test gocyclo 13).
- Streaming-validation registry test (TestToolStreaming_RegistryIsComplete)
  covers the renamed renderer key; config cascade tests pin that a user home
  config lacking the new keys inherits the new embedded defaults
  (ToolEnabledConfig.ApplyTo copies only explicitly-set keys).

## Follow-ups

- Publish-time: the agent-mcp replace directive in go.mod must become a
  versioned require when agent-mcp is tagged.
- Installed binaries predate the rename: rebuild `goa` to pick up the new
  defaults; a home config that explicitly pins a flipped key keeps its
  explicit value (documented cascade behavior).
