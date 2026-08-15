// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools"
)

// deferredProbeSet builds a registry + loader with a controlled deferred set
// so tests do not depend on the full default tool set.
func deferredProbeSet() (*tools.ToolRegistry, []agentic.Tool) {
	reg := tools.NewToolRegistry()
	deferred := []agentic.Tool{
		&tools.WebFetchTool{},
		&tools.MementoTool{},
		&tools.SmartSearchTool{},
	}
	for _, t := range deferred {
		reg.Register(t)
	}
	return reg, deferred
}

// The loader's catalog lists every deferred tool with its name.
func TestToolSearchCatalogListsDeferred(t *testing.T) {
	reg, deferred := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)
	desc := loader.Schema().Description

	for _, d := range deferred {
		if !strings.Contains(desc, d.Schema().Name) {
			t.Errorf("catalog missing deferred tool %q", d.Schema().Name)
		}
	}
}

// select:Name loads the named tools: the result carries the full schemas and
// Meta["load_tools"] with the comma-separated names.
func TestToolSearchSelectLoads(t *testing.T) {
	reg, _ := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)

	res, err := loader.ExecuteWithResult(`{"query":"select:memento,webfetch"}`)
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	got := res.Meta[agentic.MetaLoadTools]
	if got != "memento,webfetch" {
		t.Errorf("Meta[load_tools] = %q, want %q", got, "memento,webfetch")
	}

	var schemas []agentic.ToolSchema
	if err := json.Unmarshal([]byte(res.Output), &schemas); err != nil {
		t.Fatalf("output is not a schema JSON array: %v", err)
	}
	if len(schemas) != 2 {
		t.Fatalf("got %d schemas, want 2", len(schemas))
	}
	if schemas[0].Name != "memento" || schemas[1].Name != "webfetch" {
		t.Errorf("schema order = [%s %s], want [memento webfetch]", schemas[0].Name, schemas[1].Name)
	}
}

// select: with an unknown name drops it and loads only the known ones.
func TestToolSearchSelectIgnoresUnknown(t *testing.T) {
	reg, _ := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)

	res, err := loader.ExecuteWithResult(`{"query":"select:memento,does_not_exist"}`)
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	if got := res.Meta[agentic.MetaLoadTools]; got != "memento" {
		t.Errorf("Meta[load_tools] = %q, want %q", got, "memento")
	}
}

// select: with nothing matching returns the fallback catalog, no load.
func TestToolSearchSelectNoMatchFallback(t *testing.T) {
	reg, _ := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)

	res, err := loader.ExecuteWithResult(`{"query":"select:nope"}`)
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	if _, ok := res.Meta[agentic.MetaLoadTools]; ok {
		t.Error("Meta[load_tools] set on no-match select; want unset")
	}
	if !strings.Contains(res.Output, "Deferred tools available") {
		t.Errorf("no-match select should list the catalog, got: %.120s", res.Output)
	}
}

// Keyword queries match by name and description but do NOT load.
func TestToolSearchKeywordDiscovery(t *testing.T) {
	reg, _ := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)

	res, err := loader.ExecuteWithResult(`{"query":"memory"}`)
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	if _, ok := res.Meta[agentic.MetaLoadTools]; ok {
		t.Error("keyword discovery must not set Meta[load_tools]")
	}
	var schemas []agentic.ToolSchema
	if err := json.Unmarshal([]byte(res.Output), &schemas); err != nil {
		t.Fatalf("output is not a schema JSON array: %v", err)
	}
	if len(schemas) != 1 || schemas[0].Name != "memento" {
		t.Errorf("keyword 'memory' matched %d schemas, want exactly memento", len(schemas))
	}
}

// A keyword with no matches returns the fallback catalog.
func TestToolSearchKeywordNoMatch(t *testing.T) {
	reg, _ := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)

	res, err := loader.ExecuteWithResult(`{"query":"zzz_nothing_matches"}`)
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	if !strings.Contains(res.Output, "No deferred tools match") {
		t.Errorf("no-match keyword should say so, got: %.120s", res.Output)
	}
}

// The catalog is byte-stable: repeated Schema() calls return identical text
// (prompt-cache stability for the eager block).
func TestToolSearchCatalogStable(t *testing.T) {
	reg, _ := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)
	a := loader.Schema().Description
	b := loader.Schema().Description
	if a != b {
		t.Error("catalog description not byte-stable across Schema() calls")
	}
}

// The catalog respects its byte budget.
func TestToolSearchCatalogBudget(t *testing.T) {
	reg := tools.NewToolRegistry()
	// Deferred tools with long one-line descriptions force the budget to
	// truncate and emit a count-summary.
	deferred := []agentic.Tool{
		&tools.WebFetchTool{},
		&tools.MementoTool{},
		&tools.SmartSearchTool{},
		&tools.SSHBashTool{},
		&tools.TerminalsTool{},
		&tools.BGExecTool{},
		&tools.SessionSearchTool{},
		&tools.SessionEventReadTool{},
	}
	for _, t := range deferred {
		reg.Register(t)
	}
	// A custom deferred tool with a very long description occupies the budget
	// fast so truncation triggers deterministically.
	reg.Register(&longDescDeferredTool{})
	loader := tools.NewToolSearchTool(reg)

	desc := loader.Schema().Description
	// The catalog portion must stay under the 512-byte budget.
	idx := strings.Index(desc, "Deferred tools available")
	if idx < 0 {
		t.Fatal("catalog header not found in description")
	}
	catalog := desc[idx:]
	if len(catalog) > 600 { // budget 512 + header slack
		t.Errorf("catalog = %d bytes, want ≤ ~512", len(catalog))
	}
	if !strings.Contains(catalog, "and ") || !strings.Contains(catalog, "more") {
		t.Errorf("expected count-summary for truncated catalog, got: %.160s", catalog)
	}
}

// longDescDeferredTool is a deferred tool with a deliberately long
// description used to force catalog truncation in budget tests.
type longDescDeferredTool struct {
	agentic.BaseTool
}

func (*longDescDeferredTool) Deferred() bool { return true }

func (*longDescDeferredTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "long_desc_probe",
		Description: strings.Repeat("A very long tool description that consumes catalog budget quickly ", 20),
		Schema:      map[string]interface{}{"type": "object"},
	}
}

func (*longDescDeferredTool) Execute(input string) (string, error) {
	return "", nil
}
