<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Manage persistent terminal sessions.

Actions:
  open     Create a persistent shell session (type "shell" opens bash)
  close    Close a session and wait until its process tree is gone
  list     List sessions owned by the current agent
  read     Read a bounded page of retained output (offset/count)
  send     Send text to a session; by default Enter is submitted and the
           call waits for output, silence, timeout, or session exit
  signal   Send an allowed signal to the foreground process group

Security:
  - Every send is checked against the terminal allow-list (blocked/allowed
    commands) unless the sandbox is disabled in config
  - Shell-targeted SIGKILL is rejected; use close instead
  - Output is ANSI-stripped and control bytes sanitized before display
