# /mcp — Manage MCP servers

Goa is an MCP (Model Context Protocol) client. Connect MCP servers and their
tools become available to the agent under the `mcp__<server>__<tool>` namespace.

## Subcommands

- `/mcp:list` — List configured servers with status (✓ connected / ○ disabled / ✗ failed), tool count, and target.
- `/mcp:add <name> --url <url> [--header K=V]...` — Add a remote (HTTP/SSE) server and connect it.
- `/mcp:add <name> -- <cmd...> [--env K=V]...` — Add a local stdio server and connect it.
- `/mcp:remove <name>` — Remove a server and disconnect it.
- `/mcp:enable <name>` — Enable and connect a server.
- `/mcp:disable <name>` — Disable and disconnect a server.
- `/mcp:reconnect <name>` — Drop and re-establish a server connection.
- `/mcp:debug <name>` — Connect one-shot and show status + tool count.

Servers are persisted to the project `.goa/config.yaml` under `mcp:` and take
effect immediately (no restart needed) — the agent's toolset is refreshed live.

## Examples

```
/mcp:add chrome-devtools -- npx -y chrome-devtools-mcp@latest
/mcp:add github --url https://api.githubcopilot.com/mcp/ --header Authorization=Bearer $TOKEN
/mcp:list
/mcp:reconnect chrome-devtools
```
