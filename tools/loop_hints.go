// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
)

// commandRunStatus is the TUI status line shared by shell-style tools
// (bash/terminal): "Running: <command>", truncated, with a fallback when the
// command is absent. It is the tool-side implementation of the status text the
// tool-loop controller used to hardcode by tool name.
func commandRunStatus(arguments string) string {
	cmd := statusJSONStringField(arguments, "command")
	if len(cmd) > 60 {
		cmd = cmd[:57] + "..."
	}
	if cmd == "" {
		return "Running command..."
	}
	return fmt.Sprintf("Running: %s", cmd)
}

// statusJSONStringField reads a string field from a JSON object argument; used
// only to build status lines.
func statusJSONStringField(argsJSON, key string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
