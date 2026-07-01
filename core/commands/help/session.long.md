<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /session[:list|:save[:name]|:restore[:name]|:delete[:name]|:new|:import:<path>]

Manage saved sessions and session lifecycle.

Subcommands:
  list                 List all saved sessions
  save [name]          Save the current session (timestamp name if omitted)
  restore [name]       Restore a session (interactive picker if name omitted; default)
  delete [name]        Delete a session (interactive picker if name omitted)
  new                  Start a fresh session (clears history, stats, viewport)
  import <path>        Import a session from an export ZIP

Aliases: /new, /reset, /clear

User aliases (config aliases:{}):
  Define shortcuts in .goa/config.yaml:
    aliases:
      n: session:new
      r: session:restore
      l: session:list

Examples:
  /session                  Show session picker (default)
  /session:save:my-work     Save the current session as 'my-work'
  /session:restore:my-work  Restore 'my-work'
  /session:delete:my-work   Delete 'my-work'
  /session:new              Start a fresh session
  /session:import:path/to/zip  Import session from an export ZIP
