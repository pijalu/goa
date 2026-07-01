<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

/export                    export to default location (.goa/exports/)
/export:<path>             export to the given path
/export:<path>:<description>
                          export with an inline issue description

Creates a ZIP bundle with session events, logs, config, system info, and a
human-readable session summary. If no description is provided, /export prompts
for one on the main input line.
