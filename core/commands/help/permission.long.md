<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

/permission                Show current rules
/permission:list            Alias for show
/permission:add:<pattern>:<decision>[:mode]  Add a rule
/permission:remove:<id>     Remove a rule by index
/permission:clear           Remove all rules

Patterns support * (single-segment) and ** (multi-segment) wildcards.
Decision: allow, deny, ask. Mode: yolo, confirm, review (optional).
