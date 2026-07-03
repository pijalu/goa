<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Available skills. For action skills, execute with the run_skill tool; for inline/knowledge skills, load with the read tool when the task matches.

<available_skills>
{{range .}}
  <skill>
    <name>{{.Name}}</name>
    <description>{{.Description}}</description>
    <category>{{.Category}}</category>
    <execute_with>{{.ExecuteTool}}</execute_with>
    <location>{{.FilePath}}</location>
  </skill>
{{end}}
</available_skills>
