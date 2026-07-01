<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /memory[:show[:<name>[;<name>;...]]|:edit[:<name>]|:clear[:<name>]]

Manage Goa's persistent memory files stored in .goa/memory/.
Memory files are plain Markdown files with optional YAML frontmatter.

Examples:
  /memory                  List all memory files
  /memory:show             List all memory files
  /memory:show:context     Show the context memory file
  /memory:show:a;b;c       Show multiple memory files (semicolon-separated)
  /memory:edit:decisions   Edit the decisions file
  /memory:clear:todos      Clear the todos file
