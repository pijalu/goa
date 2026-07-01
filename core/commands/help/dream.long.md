<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /dream[:apply|:review|:status]

Run memory consolidation to merge raw memory files into a single, cleaned
memory file. Without apply the result is written to .goa/memory.dream/ for
review. With apply it is also copied to .goa/memory.consolidated/ and the
original memories are backed up.

Examples:
  /dream        Run dream and produce a review file
  /dream:apply  Run dream and apply the consolidated memory immediately
  /dream:review Run dream and show a diff summary
  /dream:status Show dream configuration status
