// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import "testing"

// TestConfigurableTools_IncludeMCPAndFamilies is the RED test for bugs.md B51:
// the `mcp` tool (agent-mcp bridge) and the `schedule`/`session` gated tool
// families are config-gated like every other optional tool, so they must be
// runtime-toggleable through every config surface. Their absence made the mcp
// tool invisible in /config and re-enableable only by hand-editing YAML.
func TestConfigurableTools_IncludeMCPAndFamilies(t *testing.T) {
	names := ConfigurableToolNames()
	for _, n := range []string{"mcp", "schedule", "session"} {
		found := false
		for _, name := range names {
			if name == n {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ConfigurableToolNames missing %q: %v", n, names)
		}
	}
}

// TestConfigurableTools_FamilyMembers pins the member lists of the gated
// families: one config flag gates N tools, and the lifecycle needs the
// member names to register/unregister them together.
func TestConfigurableTools_FamilyMembers(t *testing.T) {
	want := map[string][]string{
		"schedule": {"schedule_create", "schedule_delete", "schedule_list"},
		"session":  {"session_search", "session_event_read"},
	}
	for _, tool := range ConfigurableTools() {
		members, ok := want[tool.Name]
		if !ok {
			continue
		}
		if len(tool.Members) != len(members) {
			t.Errorf("family %q members = %v, want %v", tool.Name, tool.Members, members)
			continue
		}
		for i, m := range members {
			if tool.Members[i] != m {
				t.Errorf("family %q member[%d] = %q, want %q", tool.Name, i, tool.Members[i], m)
			}
		}
	}
}
