<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Available skills provide specialized instructions for specific tasks. Invoke a skill with /skill:<name>.

<available_skills>
{{range .}}
  <skill>
    <name>{{.Name}}</name>
    <description>{{.Description}}</description>
    <category>{{.Category}}</category>
{{- if .FilePath}}
    <location>{{.FilePath}}</location>
{{- end}}
  </skill>
{{end}}
</available_skills>
