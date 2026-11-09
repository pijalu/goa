# Embedded agent-mcp daemon auto-start

Closed: 2026-11-09

## Report

Calling the Goa `mcp` tool with `action="list_tools"`, `server="chrome"`,
and `limit=0` tried to auto-start the shared daemon at `127.0.0.1:7474`.
The imported agent-mcp client re-executed the current binary as
`goa server start --server-idle-time 5m`. Goa interpreted `server` as its Web
UI mode and rejected `start` as a positional prompt:

```text
goa: unknown command "start"
  goa takes no positional arguments — pass a prompt with --prompt "start"
```

Goa must include agent-mcp through its library, with one implementation in
`../agent-mcp`, and must not require an installed `agent-mcp` executable.

## Resolution

### agent-mcp library

- Added `client.SpawnDaemonWithCommand`, preserving `SpawnDaemon`'s existing
  `agent-mcp server start` default while allowing an embedding host to select
  its own subcommand and executable. Config path, listen address, API key, and
  five-minute idle timeout are still propagated.
- Added the public `server` package. `server.Run` owns TOML loading, API-key
  handling, MCP session management, listener setup, graceful shutdown, and
  cancellation. Host applications provide only CLI parsing and signal handling.
- Added configurable auto-start notices. The existing CLI notice remains
  byte-compatible; Goa selects `client.EmbeddedAutoStartNotice`, which does not
  direct users to an external executable.
- Auto-start now verifies a gRPC `Status` response after the TCP readiness
  probe. An unrelated process occupying the port no longer looks like a ready
  daemon, and a child that loses the listen race reports its own startup error.

### Goa

- The default MCP backend launches the current Goa executable with
  `mcp daemon`, rather than inheriting `server start`.
- Added `goa mcp daemon`, dispatched before Goa YAML/native MCP/TUI startup.
  Its flag parser delegates all daemon execution to `agent-mcp/server`.
- Updated tool guidance and user documentation: agent-mcp is linked as a Go
  library and no executable installation is needed. The local development
  `replace github.com/pijalu/agent-mcp => ../agent-mcp` selects the sibling
  library implementation explicitly.
- Preserved shared, warm daemon sessions and the existing nine MCP tool
  actions; no one-shot fallback or feature removal was used.

## Regression coverage

agent-mcp:

- Exact default and embedder daemon argv, including every propagated flag.
- Missing-executable startup failure.
- Post-spawn gRPC readiness validation and occupied-port child diagnostics.
- Embedded server occupied-port error, authenticated Status, absolute config
  path reporting, startup log, and cancellation.

Goa:

- Default backend installs the Goa daemon launcher and host-neutral notice.
- Daemon CLI returns an occupied-listen error before loading Goa YAML.
- Real integration builds Goa, sets `PATH` to an empty directory, auto-starts
  through `goa mcp daemon`, runs the original `list_tools` request against a
  stdio MCP fixture named `chrome`, calls `echo`, and verifies shutdown plus
  idempotent absent shutdown.

## Actual terminal validation

A PTY-backed run used `127.0.0.1:7474` and an empty `PATH`. The original input
returned one `echo` tool with `total: 1` and the embedded auto-start notice.
`call_tool` returned `terminal-validation`; shutdown returned
`shutdown: true, sessions_closed: 1`; the second shutdown returned
`shutdown: false, sessions_closed: 0`. Port 7474 and the process table were
checked afterwards: no Goa daemon, fixture, validation client, or agent-mcp
server remained running.

The user's current default agent-mcp config contains no `chrome` entry, so the
real external Chrome MCP server could not be exercised. The original request
shape and server name were exercised against the real stdio MCP fixture
instead; daemon startup itself was validated on the original address.

## Quality gates

All commands were run separately after the final changes unless noted:

- agent-mcp: `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, and
  `gocyclo -over 12 .` clean. `go test -count=1 -race -cover -timeout 180s ./...`
  passed; client 85.0%, server 84.4%, CLI 89.4%, daemon 82.0%, session 86.3%.
- Goa: `go vet ./...` and `staticcheck ./...` clean.
  `go test -count=1 -race -cover -timeout 600s ./...` passed (all packages;
  tools 84.0%, internal/app 67.2%).
- Goa complexity checks report only unrelated pre-existing warnings:
  - gocognit: B53 filmstrip resize test, provider start-plan auth test,
    GoalDriver.Drive, configurable-tool factory test, default-config mirror
    test, AgentManager.executeRunner, initGoalSystem.
  - gocyclo: B53 filmstrip resize test, start-plan device-mid test,
    GoalDriver.Drive. The new integration test's initial warning was refactored
    away and the check rerun.

During validation, the full agent-mcp suite caught a CLI notice compatibility
regression and the new occupied-port test caught TCP-only readiness. Both were
fixed and the full suites rerun.
