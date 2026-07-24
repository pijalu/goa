<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Execute shell commands locally.

Working directory:
  Commands run with the working directory set to the project root by default.
  Do NOT prepend `cd <project root> &&` — it is redundant. Only pass the
  `workdir` parameter (or `cd`) when a command must run in a different
  directory.

Parameters:
  command   (required) Shell command to execute
  timeout   (optional) Timeout in seconds (default: %ds, max: %ds)
  workdir   (optional) Working directory (default: project root)
  env       (optional) Environment variables (sensitive values masked in output)

Security:
  - Blocked commands are rejected (configurable in tools.bash.blocked_commands)
  - Allowed commands whitelist mode (configurable in tools.bash.allowed_commands)
  - Env values matching *KEY*, *TOKEN*, *SECRET*, *PASSWORD* are masked

Tool selection:
  - For searching the codebase, PREFER the `search` tool over grep/rg.
    `search` is faster (parallel), auto-excludes noise dirs, and returns
    structured results. Reserve bash+grep for tasks `search` cannot do
    (filtering command output, pipes, find -exec, PCRE-only constructs).
