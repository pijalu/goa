<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Read the contents of a file, with optional line range filtering.

Parameters:
  path         (required) Path to the file to read
  start_line   (optional) First line (1-indexed, default: 1)
  end_line     (optional) Last line (default: end of file)
  max_lines    (optional) Max lines to return (default: 500, max: 4096)
  max_bytes    (optional) Max bytes to return (default: 50000)
  show_numbers (optional) Show line numbers (default: true)

Large files are truncated to the line/byte limit with clear metadata so the
LLM knows the read succeeded and how much remains.
Binary files are detected and reported without content.
Protected paths (.goa/, .git/) are rejected.

Embedded Goa documentation can be read using the goa:// scheme, for example
goa://docs/SKILLS.md or goa://TOOLS. Use /docs to list available documents.
