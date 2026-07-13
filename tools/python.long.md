<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

The `python` tool runs Python code inside Goa using the embedded gpython
interpreter. Each call gets a fresh interpreter context; stdout and stderr are
captured and returned.

Use it for quick calculations, data transformation, or small scripts without
leaving the agent conversation. The interpreter supports the Python 3.4
language subset implemented by gpython plus its stdlib modules.

Output is truncated automatically when it exceeds the configured limits; the
full output is saved to a file in those cases.

The tool is enabled by default. Disable it via `/config` → Tools, or with
`/tools python:off`.
