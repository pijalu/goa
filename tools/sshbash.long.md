<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Execute commands on remote hosts via the system ssh binary.

Parameters:
  host_id   (required) Host ID from SSH configuration
  command   (required) Command to execute
  timeout   (optional) Timeout in seconds
  workdir   (optional) Working directory on remote host

Host verification is enforced with StrictHostKeyChecking=yes.
