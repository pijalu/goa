<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Fetch a URL and convert the page to Markdown.

Parameters:
  url        (required) Absolute URL to fetch
  action     (optional) fetch (default) or summarize
  start_line (optional) First line to return (1-indexed, default: 1)
  end_line   (optional) Last line to return (default: end)
  max_lines  (optional) Maximum lines to return
  prompt     (optional) Steering prompt for summarize action

Fetched pages are cached for the current session and can be read in ranges.
