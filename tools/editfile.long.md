<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Edit files using search/replace with fuzzy matching.

Primary usage — search/replace (recommended):
  {"path": "file.go", "old_string": "text to find", "new_string": "replacement text"}
  Uses 4-tier matching: exact → trailing whitespace → fuzzy whitespace + reindent →
  exact substring.
  Tiers 1-3 match whole lines: old_string must be a contiguous block of one or more
  lines in the file. Tier 4 (exact substring) applies only to a SINGLE-line old_string
  that is a byte-exact substring of a longer file line — it locates text inside a line,
  e.g. a fragment of a long JSON/log/config line, without touching the rest of that
  line. Its anchor must occur exactly once in the whole file: zero occurrences is
  not_found, two or more is ambiguous_match, and the file is left untouched.
  new_string must be non-empty: an empty replacement is rejected instead of
  silently deleting the matched block. To remove lines deliberately, use
  operation 'delete_lines' with start_line/end_line.

Operation alias:
  {"path": "file.go", "operation": "replace", "old_string": "text to find", "new_string": "replacement text"}
  This is identical to the search/replace form above.

Batch edits (preferred when making several changes to the same file):
  {"path": "file.go", "edits": [
    {"old_string": "first text", "new_string": "first replacement"},
    {"operation": "insert_after", "pattern": "func main()", "new_content": "\tlog.Println(\"hi\")"},
    {"old_string": "another block", "new_string": "revised block"}
  ]}
  All edits apply in order against the same file and are written atomically:
  either every edit succeeds and the file is written once, or one fails and
  nothing is written.
  A batch targets exactly one file. If the top-level "path" is omitted it is
  taken from the edits entries; entry paths that disagree are rejected.
  Content edits (old_string/new_string) and pattern-addressed inserts
  ("pattern" without start_line) re-resolve against the current content before
  every edit, so they stay valid anywhere in a batch. Absolute line numbers do
  not: they were harvested BEFORE the call, so a line-addressed edit
  (replace_lines, delete_lines, insert_after/insert_before with start_line)
  may not follow an edit that adds or removes lines. Such a batch is rejected
  up-front with stale_line_numbers and nothing is written. Combine the two
  kinds with two calls instead:
    1. line-shifting edits (delete_lines / insert_* / replace_lines that change
       the line count), then
    2. re-harvest line numbers (grep -n or read), then
    3. line-addressed edits with the fresh numbers.
  Never assume line numbers in a later edit account for an earlier edit's
  inserted or deleted lines.

Legacy operations: replace_lines, replace_pattern, insert_after, insert_before, delete_lines
Indent modes: as-is (default for insert_after/insert_before/replace_lines), preserve, normalize
  By default caller content lands byte-for-byte: padding an insertion to the target
  line's indent silently corrupts semantic-whitespace formats (Markdown, YAML). Pass
  indent_mode: "preserve" to re-indent inserted content to the target line's indent, or
  "normalize" to re-indent relative to the first inserted line. replace_pattern's
  multi-line block path still defaults to preserve; the single-line replace_pattern
  path ignores indent_mode because there is nothing to re-indent.

replace_pattern (single-line pattern):
  "pattern" is matched against each line; "occurrence" selects the Nth MATCHING LINE
  (default 1). Every match of the pattern inside that line is replaced and the rest of
  the line (prefix and suffix) is preserved byte-for-byte. The replacement is literal:
  "$1"-style group references are NOT expanded — pass the exact replacement text. An
  invalid pattern is treated as a literal string (uniform fallback), and pattern_flags
  "i" matches case-insensitively. A pattern containing a real newline is matched as a
  multi-line fuzzy block instead (occurrence must be 1).
  Errors: pattern_not_found (no line matches); occurrence_not_found (the pattern matched
  fewer lines than "occurrence" requested — nothing is written); no_change (pattern and
  replacement produce an identical line — nothing is written); missing_pattern.

Troubleshooting search/replace errors:
  • not_found: the old_string did not match the current file. The error reports the
    best CONTIGUOUS match (N/M lines, with the file line range) — an old_string whose
    lines only match scattered across non-adjacent regions can never match as one
    block; split it into one edit per region. Use 'read' to verify the exact current
    content (the file may have changed since your last read). Ensure the old_string
    includes correct indentation and blank lines. For a single-line old_string, tier 4
    already searched for a byte-exact substring of every line — a not_found therefore
    means that exact text (including internal spaces) occurs nowhere in the file, so
    suspect a typo or a whitespace difference rather than a matching bug. For deletions
    or large multi-line changes, use operation: 'delete_lines' or 'replace_lines' with
    line numbers.
  • ambiguous_match: the old_string matches more than one location (for a single-line
    anchor, tier 4 also counts every byte-exact occurrence in the file). Add more
    surrounding context to make it unique, or switch to 'replace_lines' with line numbers.
  • stale_line_numbers: a batch contains a line-addressed edit (replace_lines,
    delete_lines, insert_after/insert_before with start_line) AFTER an edit that changes
    the line count. Pre-harvested line numbers would target stale positions, so the whole
    batch is rejected and nothing is written. Split it into two calls: apply the
    line-shifting edits first, re-harvest line numbers (grep -n or read), then apply the
    line-addressed edits with the fresh numbers.
  • occurrence_not_found: replace_pattern's 'occurrence' is larger than the number of
    matching lines. Lower it, or read the file to confirm the pattern still matches; the
    file is untouched.
  • no_change: the edit would leave the file byte-identical (e.g. replace_pattern whose
    replacement equals the matched text, or old_string equal to new_string); nothing is
    written.
  • write_verify_failed: the write reported success but the read-back differs — an
    external process modified the file concurrently. Check for other writers, then retry.
  • invalid_range: start_line/end_line are out of bounds. Use 'read' to confirm the file
    length before editing.
  • missing_parameter: when using operation: 'replace', both old_string and new_string
    are required, and new_string must be non-empty — an empty replacement would delete
    the matched block, so it is refused (all-or-nothing). Provide the replacement text,
    or use 'delete_lines' for deliberate removal.
  • missing_parameter: operation 'replace_pattern' requires 'new_content' (or
    'new_string') with the replacement text; without it the edit fails up-front and the
    file is untouched.
  • Explicitly empty new_content: "new_content": "" on replace_lines/insert_after/
    insert_before is valid and writes ONE empty line (replacing one line with one line,
    so later line numbers stay valid). Omitting new_content (or leaving new_string
    empty) is still refused, because a lost payload used to delete the target range
    while reporting success. new_string must stay non-empty for the classic
    search/replace form.
