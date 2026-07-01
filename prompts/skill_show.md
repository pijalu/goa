<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

[skill: {{.Name}}]
Description: {{.Description}}
Source: {{.Source}}
Path: {{.FilePath}}
Category: {{.Category}}
{{- if .Inline}}
Mode: inline
{{- else}}
Mode: sub-agent
{{- end}}
{{- if .Profile}}
Profile: {{.Profile}}
{{- end}}

---
{{.Body}}
