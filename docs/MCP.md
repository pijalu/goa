# MCP — Model Context Protocol Servers

Goa is an MCP client. Connect MCP servers and their tools become available to
the agent under the `mcp__<server>__<tool>` namespace. Changes take effect
immediately — the agent's live toolset is refreshed without a restart.

## Quick start

Install a server from the shell (no TUI needed):

```bash
# Project-level (saved to .goa/config.yaml)
goa mcp add chrome-devtools -- npx -y chrome-devtools-mcp@latest

# Global (saved to ~/.goa/config.yaml, available in every project)
goa mcp add --global github --url https://api.githubcopilot.com/mcp/ \
    --header Authorization=Bearer $GITHUB_TOKEN

goa mcp list
goa mcp remove chrome-devtools
```

Or interactively inside Goa:

```
/mcp:add chrome-devtools -- npx -y chrome-devtools-mcp@latest
/mcp:add github --url https://api.githubcopilot.com/mcp/ --header Authorization=Bearer $TOKEN
/mcp:list
```

`/config` → **MCP servers** lists every installed server and lets you toggle
it on/off — exactly like the Tools menu. Toggles are persisted.

## Scope: project vs global

| Scope   | File                  | Flag      | Applies to        |
|---------|-----------------------|-----------|-------------------|
| Project | `.goa/config.yaml`    | (default) | this project only |
| Global  | `~/.goa/config.yaml`  | `--global`, `-g` | every project |

A project-level server with the same name overrides the global one (config
cascade). `--global` works on `add`, `remove`, `enable`, `disable`, and the
CLI equivalents.

## Config file format

```yaml
mcp:
  chrome-devtools:                 # local stdio server
    type: local
    command: ["npx", "-y", "chrome-devtools-mcp@latest"]
    cwd: .                         # optional; relative to project dir
    environment:                   # optional env overrides
      DEBUG: "1"
    timeout: 60s
    enabled: true

  github:                          # remote HTTP/SSE server
    type: remote
    url: https://api.githubcopilot.com/mcp/
    headers:
      Authorization: Bearer ${GITHUB_TOKEN}   # ${VAR} interpolation works
```

## Commands

| Command | Description |
|---------|-------------|
| `goa mcp add [--global] <name> --url <u> [--header K=V]...` | Install a remote server |
| `goa mcp add [--global] <name> -- <cmd...> [--env K=V]...` | Install a local stdio server |
| `goa mcp list` | List servers with status |
| `goa mcp remove [--global] <name>` | Uninstall a server |
| `/mcp:list` | Table: name, status (✓/○/✗), tool count, target |
| `/mcp:add [--global] ...` | Same syntax as the CLI |
| `/mcp:remove [--global] <name>` | Remove + disconnect |
| `/mcp:enable [--global] <name>` | Enable + connect (persisted) |
| `/mcp:disable [--global] <name>` | Disable + disconnect (persisted) |
| `/mcp:reconnect <name>` | Drop and re-establish the connection |
| `/mcp:debug <name>` | One-shot connect, show status + tool count |

## Lifecycle behavior

- **Tool refresh** — when a server sends `notifications/tools/list_changed`,
  Goa re-lists its tools and swaps the registered set live.
- **Roots** — Goa advertises the project directory as a `file://` root so
  servers can scope file access.
- **Process cleanup** — stdio servers run in their own process group; closing
  the connection kills the whole tree (no orphaned grandchildren).
- **Logs** — server log notifications are forwarded to Goa's logger.

## Troubleshooting

- `/mcp:debug <name>` connects one-shot and prints the tool count — fastest
  way to validate a server definition.
- A ✗ in `/mcp:list` shows the last connect error.
- Local servers need the command on `PATH` (e.g. `npx`); remote servers need
  network reachability and valid headers.
