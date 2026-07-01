<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /tools [tool-name] [:on|:off] [:param=value,...]

List all registered tools, show detailed documentation, toggle an
optional tool, or execute a tool directly with parameters.

When a tool is disabled it is removed from the model's tool list;
enabling it registers the tool immediately and sends the model a
system message so it can be used on the next turn.

Tool execution runs the tool directly and displays the output in the
conversation (not saved to model history). Parameters are given as
comma-separated key=value pairs after the tool name.

Configurable tools:
  bg_exec        enabled by default
  delegate_to    disabled by default (multi-agent)
  memento        disabled by default
  pty_exec       disabled by default
  request_review disabled by default (multi-agent)
  ssh_bash       disabled by default

Examples:
  /tools                          List all tools
  /tools:read                     Show detailed info about read
  /tools:memento:on               Enable memento
  /tools:bg_exec:off              Disable bg_exec
  /tools:search:pattern=TODO      Search for TODO
  /tools:search:glob=*.go,pattern=func.*Handler  Search Go files
  /tools:read:path=main.go        Read a file
