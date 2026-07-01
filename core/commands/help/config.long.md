<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /config[:set <key> <value> | :add <provider|model> ... | :remove <provider|model> <id> | :reload]

Note: after the subcommand, keys and values are space-separated (the config
command re-splits on whitespace), e.g. /config:set execution.mode confirm.

Without arguments, opens an interactive settings menu.
With 'set', updates a configuration key. Keys use dotted notation:
  active_profile, active_provider, active_model
  execution.mode, execution.max_tool_calls, execution.max_tool_repeat
  tui.theme
  tui.transparency.show_thinking, tui.transparency.thinking_collapsed
  logging.level, logging.file

With 'add', register a new provider or model:
  /config:add provider <id> <endpoint> [api-key]
  /config:add model <id> <provider-id> <model-name>

With 'remove', remove a provider or model:
  /config:remove provider <id>
  /config:remove model <id>

With 'reload', reloads config from all cascade layers.

Examples:
  /config                    Open settings menu
  /config:set execution.mode confirm
  /config:set tui.theme light
  /config:set tui.transparency.thinking_collapsed off
  /config:add provider local http://localhost:1234/v1
  /config:add model qwen local qwen/qwen3.5-9b
  /config:remove provider lmstudio
  /config:remove model qwen
  /config:reload
