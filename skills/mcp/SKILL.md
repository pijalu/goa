---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: mcp
description: Author MCP server configs and drive MCP servers through the mcp tool — discover tools/resources/prompts, call tools, connect new servers automatically
inline: true
category: action
command: mcp
mode: coder
temperature: 0.2
---

## Driving MCP servers with mcp

Prerequisites: the `mcp` tool is enabled by default (`tools.enabled.mcp: false`
turns it off); it talks to the shared agent-mcp daemon, auto-starting it on
first use.

1. **Discover before calling.** `{"action":"list_servers"}` answers from config (never
   spawns anything). Then `{"action":"list_tools","server":"<name>"}`; use `search` to
   filter and `limit` to cap; results carry `total`/`truncated`.
2. **Call precisely.** `{"action":"call_tool","server":S,"tool":T,"args":{...}}` — args is
   the tool's input schema, verbatim. A tool-reported error surfaces as an error, never
   as success text; read its message before retrying.
3. **Unknown name = parameter mistake.** `invalid_params` on server/tool means the name
   is wrong or the server is not in agent-mcp config. Re-run list_servers/list_tools;
   never guess names.
4. **Connect a server automatically** with
   `{"action":"config_add","name":N,"command":C,"argv":[...]}` (stdio) or
   `{"action":"config_add","name":N,"url":U,"transport":"streamable"}` (remote). The
   result reports `added` vs `updated`, the config path written, and the next step —
   verify with `{"action":"list_tools","server":N}` before calling its tools. No manual
   TOML editing, no daemon restart: the daemon picks up the entry on its next call.
5. **Resources and prompts**: `read_resource` takes `uri`; `get_prompt` takes
   `prompt` plus a JSON string map in `args`.
6. **Lifecycle**: the daemon outlives the session by design (warm sessions shared with
   other agent-mcp clients). `{"action":"shutdown"}` only when teardown is explicitly
   wanted; it is idempotent when the daemon is already gone.

Prefer pagination (`cursor`) and `search` over pulling full catalogs; discovery lists
can be large and their metadata is already stripped for you.
