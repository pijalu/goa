---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: agent_mcp
description: Author MCP server configs and drive MCP servers through the agent_mcp tool — discover tools/resources/prompts, call tools, add servers to agent-mcp config
inline: true
category: action
command: agent_mcp
mode: coder
temperature: 0.2
---

## Driving MCP servers with agent_mcp

Prerequisites: the `agent_mcp` tool must be enabled (`tools.enabled.agent_mcp: true` in goa
config); it talks to the shared agent-mcp daemon, auto-starting it on first use.

1. **Discover before calling.** `{"action":"list_servers"}` answers from config (never
   spawns anything). Then `{"action":"list_tools","server":"<name>"}`; use `search` to
   filter and `limit` to cap; results carry `total`/`truncated`.
2. **Call precisely.** `{"action":"call_tool","server":S,"tool":T,"args":{...}}` — args is
   the tool's input schema, verbatim. A tool-reported error surfaces as an error, never
   as success text; read its message before retrying.
3. **Unknown name = parameter mistake.** `invalid_params` on server/tool means the name
   is wrong or the server is not in agent-mcp config. Re-run list_servers/list_tools;
   never guess names.
4. **Add a server** with `{"action":"config_add","name":N,"command":C,"argv":[...]}`
   (stdio) or `{"url":U,"transport":"streamable"}` (remote). The result reports
   `added` vs `updated` and the config path written.
5. **Resources and prompts**: `read_resource` takes `uri`; `get_prompt` takes
   `prompt` plus a JSON string map in `args`.
6. **Lifecycle**: the daemon outlives the session by design (warm sessions shared with
   other agent-mcp clients). `{"action":"shutdown"}` only when teardown is explicitly
   wanted; it is idempotent when the daemon is already gone.

Prefer pagination (`cursor`) and `search` over pulling full catalogs; discovery lists
can be large and their metadata is already stripped for you.
