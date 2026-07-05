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

Troubleshooting search/replace errors:
  • not_found: the old_string did not match the current file. Use 'read' to verify the
    exact current content (the file may have changed since your last read). Ensure the
    old_string includes correct indentation and blank lines. For deletions or large
    multi-line changes, use operation: 'delete_lines' or 'replace_lines' with line numbers.
  • ambiguous_match: the old_string matches more than one location. Add more surrounding
    context to make it unique, or switch to 'replace_lines' with line numbers.
  • invalid_range: start_line/end_line are out of bounds. Use 'read' to confirm the file
    length before editing.
