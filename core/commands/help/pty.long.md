<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /pty[:subcommand[args]]

Manage pseudo-terminal sessions started by the agent via pty_exec.

Subcommands:
  (no args)           List all active PTY sessions
  ps                  List all active PTY sessions
  kill:<id>           Terminate a session (SIGTERM → SIGKILL)
  read:<id>           Read last 100 lines of output (plain text)
  read:<id>:<N>       Read last N lines of output (plain text)
  read:<id>:N:ansi    Read last N lines with ANSI codes preserved
  monitor:<id>        Open a live monitor overlay for the session
  write:<id>:<text>   Send input to a session

Examples:
  /pty                    List sessions
  /pty:ps                 List sessions
  /pty:kill:pty-123       Kill session pty-123
  /pty:read:pty-123       Read last 100 lines
  /pty:read:pty-123:50    Read last 50 lines
  /pty:read:pty-123:20:ansi  Read last 20 lines with ANSI codes
  /pty:monitor:pty-123    Open live monitor overlay
  /pty:write:pty-123:q    Send "q" to session
