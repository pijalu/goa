// SPDX-License-Identifier: GPL-3.0-or-last
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"regexp"
	"strings"
	"testing"

	"github.com/pijalu/goa/docs"
	"github.com/pijalu/goa/tools"
)

// TestToolsDoc_CoversEveryToolName pins the model-facing documentation
// contract (bugs.md B51 docs deliverable): docs/TOOLS.md must carry a
// `### `name“ section for EVERY tool the runtime can register.
// /docs <tool> falls back to these sections when a tool has no embedded
// Documentable docs, and the deferred tool_search catalog points the model
// here — a tool without a section is invisible to autonomous discovery.
//
// The heading must name the tool EXACTLY (backtick-quoted), so stale names
// (e.g. `goa_command` for the `goa` tool, `terminal` for `terminals`) fail.
func TestToolsDoc_CoversEveryToolName(t *testing.T) {
	content, err := docs.Get("TOOLS")
	if err != nil {
		t.Fatalf("docs.Get(TOOLS): %v", err)
	}

	names := map[string]bool{}
	// Every runtime-toggleable tool, families expanded to members: these are
	// the names /config, /tools and the deferred catalog can surface.
	for _, ct := range tools.ConfigurableTools() {
		for _, member := range ct.MembersOrSelf() {
			names[member] = true
		}
	}
	// Always-registered tools outside the configurable gate.
	for _, always := range []string{"read", "write", "edit", "search", "bash", "tool_search", "run_skill"} {
		names[always] = true
	}

	for name := range names {
		pattern := `(?m)^### ` + "`" + regexp.QuoteMeta(name) + "`"
		if !regexp.MustCompile(pattern).MatchString(content) {
			t.Errorf("docs/TOOLS.md has no `### \\`%s\\`` section — /docs %s degrades to a placeholder", name, name)
		}
	}
}

// TestToolsDoc_MCPSectionSupportsAutonomousWork pins the autonomous-workflow
// vocabulary in the mcp section: the model must be able to discover how to
// connect a server (config_add), enumerate what it offers (list_servers/
// list_tools), use it (call_tool/read_resource/get_prompt), and load the
// deferred schema (tool_search) — without reading the Go source.
func TestToolsDoc_MCPSectionSupportsAutonomousWork(t *testing.T) {
	content, err := docs.Get("TOOLS")
	if err != nil {
		t.Fatalf("docs.Get(TOOLS): %v", err)
	}
	section := extractSection(content, "mcp")
	if section == "" {
		t.Fatal("docs/TOOLS.md has no mcp section")
	}

	for _, marker := range []string{
		"config_add", "list_servers", "list_tools", "call_tool",
		"read_resource", "get_prompt", "tool_search", "invalid_params",
		"mcp_error",
	} {
		if !strings.Contains(section, marker) {
			t.Errorf("mcp section missing %q — the model cannot learn the autonomous workflow from the docs", marker)
		}
	}
}
