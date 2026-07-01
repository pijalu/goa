<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Edit files using search/replace with fuzzy matching.

Primary usage — search/replace (recommended):
  {"path": "file.go", "old_string": "text to find", "new_string": "replacement text"}
  Uses 3-tier matching: exact → trailing whitespace → fuzzy whitespace + reindent.

Legacy operations: replace_lines, replace_pattern, insert_after, insert_before, delete_lines
Indent modes: preserve (default), normalize, as-is
