// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

The `task_outcome` tool reports the result of executing a plan item. It is only
available to worker agents during plan execution.

## Status values

- **done**: Work completed successfully. `summary` is required.
- **needs_clarification**: More information needed. `question` is required and
  `summary` is optional. The orchestrator will answer or escalate to the user.
- **blocked**: Cannot proceed. `summary` is required with the reason.

The `summary` field is capped at 4000 characters.
