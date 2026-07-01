<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Execute shell commands locally.

Parameters:
  command   (required) Shell command to execute
  timeout   (optional) Timeout in seconds (default: %ds, max: %ds)
  workdir   (optional) Working directory
  env       (optional) Environment variables (sensitive values masked in output)

Security:
  - Blocked commands are rejected (configurable in tools.bash.blocked_commands)
  - Allowed commands whitelist mode (configurable in tools.bash.allowed_commands)
  - Env values matching *KEY*, *TOKEN*, *SECRET*, *PASSWORD* are masked
