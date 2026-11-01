# mcp

Drive MCP (Model Context Protocol) servers through the [agent-mcp](https://github.com/pijalu/agent-mcp)
client library. All operations run against the shared agent-mcp daemon, so
sessions and MCP child processes stay warm across goa runs and are shared
with any other agent-mcp client (CLI, other agents).

## When to use

- The project relies on MCP servers managed by agent-mcp (`agent-mcp config add ...`,
  or self-register one with `config_add` — see "Connecting a server automatically").
- You need an MCP tool call, resource, or prompt that goa's native MCP
  integration (`internal/mcp`) does not expose as a `mcp__server__tool`.
- You need to discover what an MCP server offers before calling it.

## Actions

| action | required params | notes |
|---|---|---|
| `list_servers` | — | answers from the agent-mcp config; never spawns a daemon |
| `list_tools` | `server` | paginated; pass `cursor` from a previous result |
| `call_tool` | `server`, `tool` | `args` is a JSON object; tool-reported errors surface as tool errors |
| `list_resources` | `server` | paginated |
| `read_resource` | `server`, `uri` | returns the resource body |
| `list_prompts` | `server` | paginated |
| `get_prompt` | `server`, `prompt` | `args` is a JSON string map |
| `config_add` | `name` + `command` or `url` | upserts into the agent-mcp config; reports added/updated + next step |
| `shutdown` | — | stops the shared daemon; idempotent when absent |

## Connecting a server automatically

You do not need to know the config layout — `config_add` is the whole flow:

1. Upsert the server: `{"action":"config_add","name":"docs","command":"npx",
   "argv":["-y","@modelcontextprotocol/server-docs"]}` for a stdio server, or
   `{"action":"config_add","name":"api","url":"https://mcp.example.com/mcp"}`
   (optionally `"transport":"sse"|"streamable"`, `"headers":{...}`) for a
   remote one.
2. The result reports `added` vs `updated`, the config path written, and the
   next step: verify with `{"action":"list_tools","server":"<name>"}`.
3. Call it: `{"action":"call_tool","server":"<name>","tool":T,"args":{...}}`.

The daemon picks up the new entry on its next client call — no restart, no
manual TOML editing.

## Filtering and shaping

`search` filters discovery lists across name/title/description/URI.
`limit` caps the returned entries; the result reports `total` and
`truncated` so a capped list is never mistaken for a short one.
`raw: true` returns the untouched MCP payload instead.

## Errors

Errors carry the agent-mcp taxonomy: bad parameters (`invalid_params`),
MCP-level failures (`mcp_error` — unreachable servers, tool-reported
errors), unexpected failures (`internal_error`). Unknown server or tool
names are parameter mistakes: run `list_servers` or `list_tools` first.

## Lifecycle notes

The first daemon-backed call auto-starts the daemon (stderr hint is
appended to the result); the daemon outlives the goa process by design.
Use `shutdown` when you want it gone. A daemon serving a different config
file than this client's is rejected with a config-mismatch error rather
than silently answered from the wrong servers.
