// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools"
)

// pluginToolDeferredProbe mirrors pluginToolWrapper shape for the search test.
func TestPluginRegisteredTool_VisibleViaToolSearch(t *testing.T) {
	s := newPluginTestSubsystems(t)
	rt := newPluginRuntime(s)
	register := pluginRegisterTool(s)
	_ = rt // runtime owns command/completion wiring; tools register on s directly
	if err := register("py_quota_tool", "Python quota fetcher", func(map[string]any) (interface{}, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// 1. The wrapper must be Deferred so tool_search withholds + serves it.
	tool, ok := s.toolRegistry.Get("py_quota_tool")
	if !ok {
		t.Fatal("plugin tool not in app tool registry")
	}
	d, ok := tool.(agentic.Deferred)
	if !ok || !d.Deferred() {
		t.Fatal("plugin tool is not Deferred: it can never appear in tool_search")
	}

	// 2. tool_search must serve it: keyword discovery + select: load.
	loader := tools.NewToolSearchTool(s.toolRegistry)
	s.toolRegistry.Register(loader)
	res, err := loader.ExecuteWithResult(`{"query":"quota"}`)
	if err != nil {
		t.Fatalf("keyword search: %v", err)
	}
	if !strings.Contains(res.Output, "py_quota_tool") {
		t.Fatalf("keyword search missed plugin tool:\n%s", res.Output)
	}
	sel, err := loader.ExecuteWithResult(`{"query":"select:py_quota_tool"}`)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if sel.Meta[agentic.MetaLoadTools] != "py_quota_tool" {
		t.Fatalf("select: Meta[load_tools] = %q, want py_quota_tool", sel.Meta[agentic.MetaLoadTools])
	}
}
