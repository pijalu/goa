<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

{{.Header}}

<available_skills>
{{range .Skills}}
  <skill name="{{.Name}}" category="{{.Category}}" tool="{{.ExecuteTool}}" location="{{.FilePath}}">{{.Description}}</skill>
{{end}}
</available_skills>
