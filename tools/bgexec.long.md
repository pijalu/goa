<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Manage long-running background processes.

Actions:
  start   Start a new background process
  status  Check process status (running/exited)
  read    Read recent output from stdout/stderr
  write   Write input to process stdin
  stop    Terminate a process (SIGTERM → SIGKILL)
  list    List all active processes

Output is buffered in a 10,000-line ring buffer per stream.
