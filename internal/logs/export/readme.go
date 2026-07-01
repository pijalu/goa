// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"fmt"
	"strings"
)

// renderReadme produces the agent-facing README for a bundle.
func renderReadme(m Manifest) string {
	var b strings.Builder
	b.WriteString("# Goa Diagnostic Bundle\n\n")
	b.WriteString(fmt.Sprintf("**Exported:** %s\n", m.ExportedAt))
	b.WriteString(fmt.Sprintf("**Goa version:** %s\n", m.GoaVersion))
	b.WriteString(fmt.Sprintf("**Workspace:** %s\n", m.WorkspaceDir))
	b.WriteString(fmt.Sprintf("**Active mode:** %s\n", m.ActiveMode))
	b.WriteString(fmt.Sprintf("**Session:** %s\n", m.SessionID))
	b.WriteString("\n## Issue Description\n\n")
	if m.IssueDescription != "" {
		b.WriteString(m.IssueDescription)
		b.WriteString("\n")
	} else {
		b.WriteString("*(no description provided)*\n")
	}
	b.WriteString("\n## How to Investigate\n\n")
	b.WriteString("1. Start with `manifest.json` for metadata and the list of bundled files.\n")
	b.WriteString("2. Read `issue.md` for the user's own words about the problem.\n")
	b.WriteString("3. Inspect `session/events.jsonl` for the raw agent event stream.\n")
	b.WriteString("4. Check `logs/goa.log` for agent/LLM debug output.\n")
	b.WriteString("5. If the problem is TUI/input related, check `logs/keys.log`.\n")
	b.WriteString("6. Compare `config/*.yaml` to verify provider/model settings (secrets redacted).\n")
	b.WriteString("7. Check `prompts/mode/` for user-defined or customized modes.\n")
	b.WriteString("8. Check `logs/http.jsonl` for the last 20 HTTP request/response transactions (URL, status, timing, response preview).\n")
	b.WriteString("9. `session.md` contains a human-readable summary of the session state.\n")
	b.WriteString("\n## Files\n\n")
	b.WriteString("- `session/events.jsonl` — raw agent events\n")
	b.WriteString("- `logs/goa.log` — agent debug log\n")
	b.WriteString("- `logs/http.jsonl` — last 20 HTTP request/response transactions\n")
	b.WriteString("- `logs/keys.log` — key-trace log\n")
	b.WriteString("- `config/project.yaml` — project configuration\n")
	b.WriteString("- `config/user.yaml` — user configuration\n")
	b.WriteString("- `config/local.yaml` — local overrides\n")
	b.WriteString("- `prompts/mode/` — user-defined modes\n")
	b.WriteString("- `system/info.json` — runtime environment\n")
	b.WriteString("- `issue.md` — issue description\n")
	b.WriteString("- `session.md` — session summary\n")
	if len(m.MissingFiles) > 0 {
		b.WriteString("\n## Missing Files\n\n")
		for _, f := range m.MissingFiles {
			b.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
	}
	return b.String()
}
