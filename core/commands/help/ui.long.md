<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /ui:action[:args]

Actions:
  theme:set:<token>:<color>   Change a theme token color
  pane:show:<id>              Show a pane
  pane:hide:<id>              Hide a pane
  flash:<message>             Show a flash message

Examples:
  /ui:theme:set:accent:#ff6600
  /ui:flash:Task completed
