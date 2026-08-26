// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// --- helpers ---------------------------------------------------------------

// probeDeferredTool is a minimal Deferred tool for registry tests.
type probeDeferredTool struct {
	BaseTool
	name string
}

func (p *probeDeferredTool) Deferred() bool { return true }

func (p *probeDeferredTool) Schema() ToolSchema {
	return ToolSchema{Name: p.name, Description: "deferred probe " + p.name}
}

func (p *probeDeferredTool) Execute(input string) (string, error) { return "", nil }

// probeEagerTool is a minimal non-deferred tool.
type probeEagerTool struct {
	BaseTool
	name string
}

func (p *probeEagerTool) Schema() ToolSchema {
	return ToolSchema{Name: p.name, Description: "eager probe " + p.name}
}

func (p *probeEagerTool) Execute(input string) (string, error) { return "", nil }

// probeLoader is the DeferredToolLoader for registry tests.
type probeLoader struct {
	BaseTool
}

func (*probeLoader) IsDeferredToolLoader() bool { return true }

func (*probeLoader) Schema() ToolSchema {
	return ToolSchema{Name: "tool_search", Description: "load deferred tools"}
}

func (*probeLoader) Execute(input string) (string, error) { return "", nil }

// deferralProbeRegistry builds a registry with threshold-1 eager tools, one
// loader, and exactly DeferralThreshold deferred tools so deferral activates.
func deferralProbeRegistry() *ToolRegistry {
	tools := []Tool{
		&probeEagerTool{name: "read"},
		&probeEagerTool{name: "write"},
		&probeLoader{},
	}
	// DeferralThreshold deferred tools (all named d0..d7) so the count is ≥
	// threshold.
	for i := 0; i < DeferralThreshold; i++ {
		tools = append(tools, &probeDeferredTool{name: "d" + string(rune('0'+i))})
	}
	return NewToolRegistry(tools)
}

func schemaNames(schemas []ToolSchema) []string {
	names := make([]string, len(schemas))
	for i, s := range schemas {
		names[i] = s.Name
	}
	return names
}

func marshalSchemas(schemas []ToolSchema) [][]byte {
	out := make([][]byte, len(schemas))
	for i, s := range schemas {
		b, err := json.Marshal(s)
		if err != nil {
			panic(err)
		}
		out[i] = b
	}
	return out
}

func equalBytes(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

// --- registry partition ----------------------------------------------------

// Deferral activates (deferred count ≥ threshold + loader present): the
// eager view is [eager block][loader] and excludes every deferred tool.
func TestDeferralPartitionEagerBlock(t *testing.T) {
	reg := deferralProbeRegistry()
	schemas := reg.Schemas()

	// 2 eager + loader; the 8 deferred tools are withheld.
	if len(schemas) != 3 {
		t.Fatalf("partitioned view has %d schemas, want 3 (2 eager + loader); names=%v",
			len(schemas), schemaNames(schemas))
	}
	want := []string{"read", "write", "tool_search"}
	got := schemaNames(schemas)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("eager block order = %v, want %v", got, want)
		}
	}
}

// Deferral does NOT activate when the deferred count is below the threshold:
// every schema ships eagerly (pre-deferral behavior).
func TestDeferralInactiveBelowThreshold(t *testing.T) {
	tools := []Tool{
		&probeEagerTool{name: "read"},
		&probeLoader{},
	}
	for i := 0; i < DeferralThreshold-1; i++ {
		tools = append(tools, &probeDeferredTool{name: "d" + string(rune('0'+i))})
	}
	reg := NewToolRegistry(tools)
	if len(reg.Schemas()) != len(tools) {
		t.Errorf("below-threshold set must ship all schemas, got %d of %d",
			len(reg.Schemas()), len(tools))
	}
}

// Deferral does NOT activate when the loader is absent: withheld tools would
// be unreachable, so everything ships eagerly.
func TestDeferralInactiveWithoutLoader(t *testing.T) {
	tools := []Tool{}
	for i := 0; i < DeferralThreshold; i++ {
		tools = append(tools, &probeDeferredTool{name: "d" + string(rune('0'+i))})
	}
	reg := NewToolRegistry(tools)
	if len(reg.Schemas()) != len(tools) {
		t.Errorf("no-loader set must ship all schemas, got %d of %d",
			len(reg.Schemas()), len(tools))
	}
}

// --- load + cache stability ------------------------------------------------

// Loading a deferred tool appends it to the tail; the eager prefix (eager
// block + loader) stays byte-identical.
func TestDeferralLoadAppendsTail(t *testing.T) {
	reg := deferralProbeRegistry()
	before := marshalSchemas(reg.Schemas())

	loaded := reg.LoadDeferred([]string{"d3", "d0"})
	if len(loaded) != 2 {
		t.Fatalf("LoadDeferred returned %v, want [d3 d0]", loaded)
	}

	after := reg.Schemas()
	if len(after) != len(before)+2 {
		t.Fatalf("after load: %d schemas, want %d", len(after), len(before)+2)
	}
	// Prefix byte-identical (prompt-cache stability).
	afterBytes := marshalSchemas(after)
	if !equalBytes(before, afterBytes[:len(before)]) {
		t.Error("eager prefix changed after load — prompt cache would bust")
	}
	// Tail is load-order append-only.
	var tailNames []string
	for i := len(before); i < len(after); i++ {
		if !strings.HasPrefix(string(afterBytes[i]), "{") {
			t.Fatalf("tail element %d is not a JSON object: %.40s", i, afterBytes[i])
		}
		tailNames = append(tailNames, after[i].Name)
	}
	if tailNames[0] != "d3" || tailNames[1] != "d0" {
		t.Errorf("tail order = %v, want [d3 d0] (load order)", tailNames)
	}
}

// Loading the same tool twice is idempotent: no duplicate tail entry.
func TestDeferralLoadIdempotent(t *testing.T) {
	reg := deferralProbeRegistry()
	reg.LoadDeferred([]string{"d1"})
	second := reg.LoadDeferred([]string{"d1"})
	if len(second) != 0 {
		t.Errorf("second load of d1 returned %v, want none", second)
	}
	if len(reg.Schemas()) != 4 { // 2 eager + loader + d1
		t.Errorf("after idempotent load: %d schemas, want 4", len(reg.Schemas()))
	}
}

// Loading an unknown or non-deferred name is a no-op.
func TestDeferralLoadIgnoresNonDeferred(t *testing.T) {
	reg := deferralProbeRegistry()
	loaded := reg.LoadDeferred([]string{"read", "nope"})
	if len(loaded) != 0 {
		t.Errorf("LoadDeferred(non-deferred) = %v, want none", loaded)
	}
	if len(reg.Schemas()) != 3 {
		t.Errorf("schema count changed after non-deferred load: %d", len(reg.Schemas()))
	}
}

// --- gating ----------------------------------------------------------------

// DeferredStatus reports unloaded deferred tools with the loader name for the
// redirect hint; eager/unknown/loaded tools report false.
func TestDeferralStatus(t *testing.T) {
	reg := deferralProbeRegistry()

	if loader, unloaded := reg.DeferredStatus("d0"); !unloaded || loader != "tool_search" {
		t.Errorf("DeferredStatus(d0) = (%q, %v), want (tool_search, true)", loader, unloaded)
	}
	if _, unloaded := reg.DeferredStatus("read"); unloaded {
		t.Error("DeferredStatus(read) unloaded = true, want false (eager)")
	}
	if _, unloaded := reg.DeferredStatus("nope"); unloaded {
		t.Error("DeferredStatus(nope) unloaded = true, want false (unknown)")
	}

	reg.LoadDeferred([]string{"d0"})
	if _, unloaded := reg.DeferredStatus("d0"); unloaded {
		t.Error("DeferredStatus(d0) after load = unloaded, want false")
	}
}

// AllSchemas exposes the full tool set regardless of the deferred partition
// (used by the MCP publisher).
func TestDeferralAllSchemas(t *testing.T) {
	reg := deferralProbeRegistry()
	all := reg.AllSchemas()
	if len(all) != 2+1+DeferralThreshold {
		t.Errorf("AllSchemas() = %d, want full set %d", len(all), 2+1+DeferralThreshold)
	}
}

// --- agent loop Meta wiring ------------------------------------------------

// runTool rejects an unloaded deferred tool with a clear redirect error.
func TestRunToolDeferredRedirect(t *testing.T) {
	agent := NewAgent(Config{
		Model:              testModel(provider.ApiOpenAICompletions),
		SystemPrompt:       "test",
		Tools:              []Tool{&probeEagerTool{name: "read"}, &probeEagerTool{name: "write"}, &probeLoader{}, &probeDeferredTool{name: "d0"}},
		ContextCompression: ContextCompressionConfig{MaxTokens: 0},
	})
	// Build a registry that has deferral active (≥ threshold deferred tools +
	// loader) and swap it in.
	agent.reg = deferralProbeRegistry()

	_, err := agent.runTool(context.Background(), "d0", `{}`)
	if err == nil {
		t.Fatal("expected deferred redirect error for unloaded d0")
	}
	if !strings.Contains(err.Error(), "deferred") || !strings.Contains(err.Error(), "tool_search") {
		t.Errorf("redirect error = %q, want a 'deferred ... tool_search' redirect", err)
	}

	// After loading, the same call reaches the tool (no redirect).
	reg := deferralProbeRegistry()
	agent.reg = reg
	reg.LoadDeferred([]string{"d0"})
	if _, err := agent.runTool(context.Background(), "d0", `{}`); err != nil {
		t.Errorf("d0 call after load failed: %v", err)
	}
}

// applyToolLoadRequests reads Meta[MetaLoadTools] from results and loads the
// named deferred tools.
func TestApplyToolLoadRequests(t *testing.T) {
	reg := deferralProbeRegistry()
	agent := NewAgent(Config{
		Model:              testModel(provider.ApiOpenAICompletions),
		SystemPrompt:       "test",
		Tools:              []Tool{&probeEagerTool{name: "read"}, &probeLoader{}, &probeDeferredTool{name: "d0"}, &probeDeferredTool{name: "d1"}},
		ContextCompression: ContextCompressionConfig{MaxTokens: 0},
	})
	agent.reg = reg

	agent.applyToolLoadRequests([]ToolCallResult{
		{Name: "tool_search", Output: `[{"name":"d0"}]`, Meta: map[string]string{MetaLoadTools: "d0, d1"}},
		{Name: "read", Output: "ok"},
	})

	if _, unloaded := reg.DeferredStatus("d0"); unloaded {
		t.Error("d0 not loaded after Meta load_tools request")
	}
	if _, unloaded := reg.DeferredStatus("d1"); unloaded {
		t.Error("d1 not loaded after Meta load_tools request")
	}
	if len(reg.Schemas()) != 5 { // 2 eager + loader + d0 + d1
		t.Errorf("schema count = %d, want 5", len(reg.Schemas()))
	}
}

// PartitionDeferred is the single partition source of truth shared with the
// prompt builder: same activation rules, alpha-sorted names.
func TestPartitionDeferred_ActivatesLikeRegistry(t *testing.T) {
	loader, deferred := PartitionDeferred([]Tool{
		&probeEagerTool{name: "read"},
		&probeLoader{},
		&probeDeferredTool{name: "zeta"},
		&probeDeferredTool{name: "alpha"},
		&probeDeferredTool{name: "mid"},
		&probeDeferredTool{name: "d3"},
		&probeDeferredTool{name: "d4"},
		&probeDeferredTool{name: "d5"},
		&probeDeferredTool{name: "d6"},
		&probeDeferredTool{name: "d7"},
		&probeDeferredTool{name: "d8"},
	})
	if loader != "tool_search" {
		t.Errorf("loader = %q, want tool_search", loader)
	}
	if len(deferred) != 9 {
		t.Fatalf("deferred = %v (%d), want 9 entries", deferred, len(deferred))
	}
	for i := 1; i < len(deferred); i++ {
		if deferred[i-1] > deferred[i] {
			t.Errorf("deferred not sorted: %v", deferred)
			break
		}
	}
}

func TestPartitionDeferred_BelowThresholdNil(t *testing.T) {
	_, deferred := PartitionDeferred([]Tool{
		&probeLoader{},
		&probeEagerTool{name: "read"},
		&probeDeferredTool{name: "only-one"},
	})
	if deferred != nil {
		t.Errorf("below threshold deferred = %v, want nil", deferred)
	}
}

func TestPartitionDeferred_NoLoaderNil(t *testing.T) {
	var many []Tool
	for i := 0; i < DeferralThreshold+2; i++ {
		many = append(many, &probeDeferredTool{name: fmt.Sprintf("p%d", i)})
	}
	loader, deferred := PartitionDeferred(many)
	if loader != "" || deferred != nil {
		t.Errorf("no-loader partition = (%q, %v), want empty", loader, deferred)
	}
}
