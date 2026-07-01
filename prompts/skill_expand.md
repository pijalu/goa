<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

<skill name="{{.Name}}" location="{{.FilePath}}">

{{.Body}}
</skill>{{if .Args}}

{{.Args}}
{{end}}
