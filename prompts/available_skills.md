<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Action skills: run_skill. Inline/knowledge: read.

<available_skills>
{{range .}}
  <skill name="{{.Name}}" category="{{.Category}}" tool="{{.ExecuteTool}}" location="{{.FilePath}}">{{.Description}}</skill>
{{end}}
</available_skills>
