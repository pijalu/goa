# MCP Chrome DevTools review — 2026-11-09

## Scope

Reviewed Goa's built-in `mcp` tool through the real `AgentMCPTool` and embedded
`agent-mcp` daemon. Exercised the configured Chrome DevTools MCP server with
the `chrome-devtools` skill, then fixed the two reproduced client issues.
Daemon/client transport simplification was explicitly deferred: the process
boundary still provides warm MCP session sharing across Goa clients.

## B1 — cached dead daemon connection

### Observed

After the shared daemon stopped, native `list_tools chrome` repeatedly failed
with:

```text
mcp error: connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:7474: connect: connection refused"
```

A fresh CLI client could use the same restarted daemon, proving the Chrome
server and daemon configuration were healthy. The long-lived Goa client kept
using its cached dead gRPC transport.

### Regression and fix

Added `TestClientReconnectsCachedDeadDaemonBeforeRequest` and
`TestClientReconnectsBeforeCallToolWithoutReplaying` in `agent-mcp/client`.
Both failed before the fix with `mcp error: connection refused`.

The client now checks a cached transport before invoking the operation. An
`Unavailable` transport is closed and redialed, auto-starting the daemon when
necessary. Reconnecting before invocation, rather than retrying a failed tool
call, prevents duplicate execution of non-idempotent tools.

Fixed in `agent-mcp` commit `e11fe2e` (`fix(client): reconnect stale daemon
transports`). Goa's real embedded-daemon integration test now shuts down the
daemon, discovers tools through the same long-lived tool, calls `echo`, and
shuts the restarted daemon down again.

## B2 — optional capabilities leaked raw RPC errors

### Observed

Chrome DevTools is a tools-only server. Native discovery produced:

```text
mcp error: calling "resources/list": Method not found
mcp error: calling "prompts/list": Method not found
```

The generic hint incorrectly directed the caller to check connectivity.

### Regression and fix

Added a tools-only fixture whose optional methods return `Method not found`,
matching Chrome. Before the fix, list operations returned those raw errors and
read/get operations had no clear unsupported-capability classification.

The session manager now respects negotiated capabilities. Unsupported list
operations return empty collections; read/get operations return a typed error
naming the unsupported capability. Daemon and in-process mappings classify it
as `InvalidArgument` / `ErrUsage`, which Goa exposes as `invalid_params`.

Fixed in `agent-mcp` commit `547fa4f` (`fix(session): respect optional MCP
capabilities`).

## Headless Chrome DevTools validation

Used an isolated configuration with `chrome-devtools-mcp@1.12.0`, `--headless`,
`--isolated`, usage statistics disabled, and a local HTML fixture. Chrome's
process arguments confirmed `--headless=new` and a temporary profile.

Validated through the real Goa tool and daemon:

1. Tool discovery returned the `list_pages` schema and total.
2. `list_tools → shutdown → list_tools` through one client auto-started a fresh
   daemon and succeeded, reproducing and closing B1.
3. `list_pages` returned page 1; navigation to the local fixture succeeded.
4. Accessibility snapshot exposed the textbox and button UIDs.
5. `fill_form` entered `goa-mcp-final`; `click` produced
   `TOKEN:goa-mcp-final` in the updated snapshot.
6. `evaluate_script` returned title `MCP Review Fixture` and result
   `TOKEN:goa-mcp-final`.
7. Console inspection returned no messages; network inspection returned the
   fixture's successful `200` request.
8. `list_resources` and `list_prompts` returned empty collections with
   `total: 0`. Read/get returned explicit unsupported-capability errors.

## Automated validation

`agent-mcp`:

- `go test -count=1 -race -timeout 120s ./...` — passed after each fix and after
  the combined changes.
- `go vet ./...` — passed.
- Reconnect regressions failed before the fix and passed afterward.

Goa:

- `go test -count=1 -timeout 90s ./tools -run
  TestAgentMCPEmbeddedDaemonWithoutExecutable -v` — passed, including real
  daemon restart and post-restart tool invocation.
- `go vet ./...` — passed.
- `staticcheck ./...` — passed.
- `go test -count=1 -race -cover -timeout 120s ./...` — passed; `tools`
  coverage 84.0%, `internal/agentic/mcp` 94.8%.
- `gocognit -over 15 .` and `gocyclo -over 12 .` reported only pre-existing,
  unrelated warnings in app/provider/core/config code and tests; no MCP change
  introduced a warning.

## Cleanup

Stopped validation daemons on `127.0.0.1:7474` and `127.0.0.1:17474` through
the MCP shutdown action. This included the manually started daemon PID 51477,
the reconnect harness daemon PID 51719, headless validation daemon PIDs 52725
and 53491, and their Chrome/MCP child trees. Final `ps` and listener checks
showed no validation daemon, `chrome-devtools-mcp`, or automation Chrome
process remaining on either port.
