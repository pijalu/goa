<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Execute shell commands in a hardened sandbox.

Parameters:
  command   (required) Shell command to execute
  timeout   (optional) Timeout in seconds

Security:
  - Runs in a per-session sandbox directory with repointed HOME/TMPDIR
  - Command-position blocklist (rm, sudo, curl, ssh, etc.)
  - Credential stripping from child environment
  - Process group isolation and timeout enforcement
