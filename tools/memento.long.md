<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Persistent memory file operations.

Memory files are plain Markdown stored in .goa/memory/ (project) and
~/.goa/memory/ (global). Project takes precedence for reads.
All writes go to the project directory.

Actions:
  list    List all memory files with preview
  read    Read a memory file
  write   Create or overwrite a memory file
  append  Append content under a section heading
  delete  Remove a memory file

Name validation: lowercase alphanumeric + hyphens only (^[a-z0-9-]+$)
