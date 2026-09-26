// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// testTool is a simple tool for testing.
type testTool struct {
	name string
}

func (t *testTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: t.name, Description: "test tool"}
}
func (t *testTool) Execute(input string) (string, error) { return "ok", nil }
func (t *testTool) IsRetryable(err error) bool           { return false }

// testDocTool implements both Tool and Documentable.
type testDocTool struct {
	testTool
	shortDoc string
	longDoc  string
	examples []string
}

func (t *testDocTool) ShortDoc() string   { return t.shortDoc }
func (t *testDocTool) LongDoc() string    { return t.longDoc }
func (t *testDocTool) Examples() []string { return t.examples }

// TestToolRegistryRegisterAndGet verifies basic registration.
func TestToolRegistryRegisterAndGet(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "read"})

	tool, found := reg.Get("read")
	if !found {
		t.Fatal("Get('read') should find tool")
	}
	if tool.Schema().Name != "read" {
		t.Errorf("Name = %q, want %q", tool.Schema().Name, "read")
	}
}

// TestToolRegistryGetUnknown verifies unknown tool returns false.
func TestToolRegistryGetUnknown(t *testing.T) {
	reg := NewToolRegistry()
	_, found := reg.Get("nonexistent")
	if found {
		t.Error("Get('nonexistent') should return false")
	}
}

// TestToolRegistryAll verifies All returns all tools.
func TestToolRegistryAll(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "beta"})
	reg.Register(&testTool{name: "alpha"})

	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("All = %d, want 2", len(all))
	}
	// Should be sorted
	if all[0].Schema().Name != "alpha" || all[1].Schema().Name != "beta" {
		t.Errorf("All order: %q, %q — want sorted", all[0].Schema().Name, all[1].Schema().Name)
	}
}

// TestToolRegistryAllDocumented verifies documented tool filtering.
func TestToolRegistryAllDocumented(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "undocumented"})
	reg.Register(&testDocTool{
		testTool: testTool{name: "documented"},
		shortDoc: "Short doc",
		longDoc:  "Long doc",
		examples: []string{"example 1"},
	})

	doced := reg.AllDocumented()
	if len(doced) != 1 {
		t.Fatalf("AllDocumented = %d, want 1", len(doced))
	}
	if doced[0].ShortDoc != "Short doc" {
		t.Errorf("ShortDoc = %q, want %q", doced[0].ShortDoc, "Short doc")
	}
	if len(doced[0].Examples) != 1 {
		t.Errorf("Examples = %d, want 1", len(doced[0].Examples))
	}
}

// TestToolRegistryUnregister verifies a tool can be removed at runtime.
func TestToolRegistryUnregister(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "alpha"})
	reg.Register(&testTool{name: "beta"})

	reg.Unregister("alpha")
	if _, found := reg.Get("alpha"); found {
		t.Error("Get('alpha') should return false after Unregister")
	}
	if len(reg.All()) != 1 {
		t.Fatalf("All = %d, want 1", len(reg.All()))
	}
	// Unregistering unknown tools is a no-op.
	reg.Unregister("gamma")
	if len(reg.All()) != 1 {
		t.Fatalf("All = %d, want 1 after unregistering unknown", len(reg.All()))
	}
}

// reentrantTool derives its schema from the live registry, exactly like
// ToolSearchTool (Schema → nameCatalog → deferredTools → reg.All). Register and
// the read paths must never hold the registry lock across Schema().
type reentrantTool struct {
	name string
	reg  *ToolRegistry
}

func (t *reentrantTool) Schema() agentic.ToolSchema {
	_ = t.reg.All()
	return agentic.ToolSchema{Name: t.name, Description: "reentrant"}
}
func (t *reentrantTool) Execute(string) (string, error) { return "ok", nil }
func (t *reentrantTool) IsRetryable(error) bool         { return false }

// reentrantDocTool re-enters the registry from a doc accessor, which
// AllDocumented must call outside its snapshot lock.
type reentrantDocTool struct {
	testDocTool
	reg *ToolRegistry
}

func (t *reentrantDocTool) ShortDoc() string {
	_ = t.reg.All()
	return t.shortDoc
}

// registryRace* size the C1 concurrency test.
const (
	registryRaceWriters    = 4
	registryRaceReaders    = 4
	registryRaceIterations = 150
)

// churnRegistry registers and unregisters disposable tools and group tools,
// the way the TUI /config and /tools paths, MCP connect/disconnect, and
// plugin load write to the live registry.
func churnRegistry(reg *ToolRegistry, worker int) {
	for i := 0; i < registryRaceIterations; i++ {
		name := fmt.Sprintf("w%d-t%d", worker, i)
		reg.Register(&testDocTool{testTool: testTool{name: name}, shortDoc: "s"})
		reg.RegisterGroup("plug__", []agentic.Tool{&testTool{name: "plug__" + name}})
		reg.Unregister(name)
		reg.UnregisterGroup("plug__")
	}
}

// readRegistry performs the reads the agent path does on every request
// (tool_search → deferredTools → All, plus lookup and match).
func readRegistry(reg *ToolRegistry, t *testing.T) {
	for i := 0; i < registryRaceIterations; i++ {
		if len(reg.All()) == 0 {
			t.Error("All() returned an empty snapshot while 'stable' is registered")
			return
		}
		if _, ok := reg.Get("stable"); !ok {
			t.Error("Get('stable') lost the tool registered before the race")
			return
		}
		_ = reg.Match("mcp__server__read")
		_ = reg.AllDocumented()
	}
}

// TestToolRegistry_ConcurrentRegisterAndAll is the RED test for the
// unsynchronized registry: writers (TUI /config, /tools, MCP connect, plugin
// load) register/unregister while the agent path reads the live registry on
// every request (tool_search → deferredTools → All). Before the RWMutex the
// -race build reports a data race (and All can observe a torn map); after the
// fix it must be clean and the stable tool must always survive.
func TestToolRegistry_ConcurrentRegisterAndAll(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "stable"})
	reg.RegisterGroup("mcp__server__", []agentic.Tool{&testTool{name: "mcp__server__read"}})

	var wg sync.WaitGroup
	for w := 0; w < registryRaceWriters; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			churnRegistry(reg, w)
		}(w)
	}
	for r := 0; r < registryRaceReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			readRegistry(reg, t)
		}()
	}
	wg.Wait()

	if _, ok := reg.Get("stable"); !ok {
		t.Fatal("Get('stable') = false after the race; a Register was lost")
	}
}

// TestToolRegistry_AllIsSnapshot pins the snapshot-then-release contract: a
// returned slice is immutable from the caller's point of view, never sees
// mutations that happen after it was taken, and is never torn by a writer
// racing it. Unsynchronized, the map read inside All() races Register — the
// -race build reports it (RED before the RWMutex).
func TestToolRegistry_AllIsSnapshot(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "alpha"})
	reg.Register(&testTool{name: "stable"})

	// Deterministic half: a Register after the snapshot is invisible to that
	// snapshot and visible to the next call.
	first := reg.All()
	if len(first) != 2 {
		t.Fatalf("All() = %d tools, want 2", len(first))
	}
	reg.Register(&testTool{name: "beta"})
	if len(first) != 2 {
		t.Errorf("in-flight All() slice grew to %d — it aliases the live map", len(first))
	}
	if got := len(reg.All()); got != 3 {
		t.Errorf("All() after Register = %d tools, want 3", got)
	}
	reg.Unregister("beta")

	// Concurrent half: a writer churning the registry must never tear a
	// snapshot nor make an untouched tool disappear from it.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < registryRaceIterations; i++ {
			name := fmt.Sprintf("snap-%d", i)
			reg.Register(&testTool{name: name})
			reg.Unregister(name)
		}
	}()
	for i := 0; i < registryRaceIterations; i++ {
		seen := make(map[string]bool)
		for _, tool := range reg.All() {
			if tool == nil {
				t.Error("All() snapshot contained a nil tool (torn map read)")
				break
			}
			seen[tool.Schema().Name] = true
		}
		if !seen["alpha"] || !seen["stable"] {
			t.Errorf("All() snapshot lost a registered tool while a writer ran: %v", seen)
			break
		}
	}
	wg.Wait()
}

// groupWriter churns a namespaced group and checks its own invariants: the
// group's tool is matchable while the group exists and not after its removal.
func groupWriter(reg *ToolRegistry, t *testing.T, prefix string) {
	for i := 0; i < registryRaceIterations; i++ {
		name := fmt.Sprintf("%st%d", prefix, i)
		reg.RegisterGroup(prefix, []agentic.Tool{&testTool{name: name}})
		if !reg.Match(name) {
			t.Errorf("Match(%q) = false right after RegisterGroup", name)
			return
		}
		reg.UnregisterGroup(prefix)
		if reg.Match(name) {
			t.Errorf("Match(%q) = true after UnregisterGroup removed the group", name)
			return
		}
	}
}

// groupReader hammers the group list and the tool map while groups come and go.
func groupReader(reg *ToolRegistry, t *testing.T, probe string) {
	for i := 0; i < registryRaceIterations; i++ {
		if !reg.Match(probe) {
			t.Errorf("Match(%q) = false; the base group was lost", probe)
			return
		}
		_ = reg.All()
		_ = reg.AllDocumented()
	}
}

// TestToolRegistry_GroupOpsConcurrent pins group safety: RegisterGroup and
// UnregisterGroup may run while readers iterate Match/All, the group list is
// never observed half-filtered, and a group tool is always matchable between
// its registration and its group's removal.
func TestToolRegistry_GroupOpsConcurrent(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "stable"})
	reg.RegisterGroup("base__", []agentic.Tool{&testTool{name: "base__read"}})

	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			groupWriter(reg, t, fmt.Sprintf("grp%d__", w))
		}(w)
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			groupReader(reg, t, "base__read")
		}()
	}
	wg.Wait()

	if !reg.Match("base__read") {
		t.Error("the base group no longer matches after the race")
	}
	if _, ok := reg.Get("base__read"); !ok {
		t.Error("base__read disappeared from the registry")
	}
}

// TestToolRegistry_AllDocumentedSnapshotAndOrder pins deterministic, stable
// output (map iteration order is random) and snapshot semantics.
func TestToolRegistry_AllDocumentedSnapshotAndOrder(t *testing.T) {
	reg := NewToolRegistry()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		reg.Register(&testDocTool{testTool: testTool{name: n}, shortDoc: n + " doc"})
	}

	first := reg.AllDocumented()
	if len(first) != 3 {
		t.Fatalf("AllDocumented() = %d, want 3", len(first))
	}
	want := []string{"alpha", "mid", "zeta"}
	for i, w := range want {
		if got := first[i].Tool.(agentic.Tool).Schema().Name; got != w {
			t.Errorf("AllDocumented()[%d] = %q, want %q (stable order)", i, got, w)
		}
	}

	reg.Register(&testDocTool{testTool: testTool{name: "zzz"}, shortDoc: "zzz doc"})
	if len(first) != 3 {
		t.Errorf("in-flight AllDocumented() slice grew to %d", len(first))
	}
}

// TestToolRegistry_AllWhileSchemaReenters guards the non-reentrant lock: a
// registered tool whose Schema() (or doc accessor) calls back into the
// registry must not deadlock Register, All, or AllDocumented.
func TestToolRegistry_AllWhileSchemaReenters(t *testing.T) {
	reg := NewToolRegistry()

	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.Register(&reentrantTool{name: "tool_search", reg: reg})
		reg.Register(&reentrantDocTool{
			testDocTool: testDocTool{testTool: testTool{name: "docs"}, shortDoc: "d"},
			reg:         reg,
		})
		for i := 0; i < 25; i++ {
			for _, tool := range reg.All() {
				_ = tool.Schema()
			}
			_ = reg.AllDocumented()
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: the registry lock is held across a call into a registered tool")
	}

	if _, ok := reg.Get("tool_search"); !ok {
		t.Error("re-entrant tool was not registered")
	}
}

// TestConfigurableTools_IncludesVerifyAndClarify ensures the runtime-toggleable
// list (which drives the /config → Tools screen and docs) covers verify and
// ask_user_question, the two opt-out tools, in addition to the opt-in extras.
func TestConfigurableTools_IncludesVerifyAndClarify(t *testing.T) {
	names := ConfigurableToolNames()
	want := map[string]bool{"verify": true, "ask_user_question": true, "bg_exec": true, "webfetch": true}
	for n := range want {
		found := false
		for _, got := range names {
			if got == n {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ConfigurableToolNames missing %q: %v", n, names)
		}
	}
}

func TestConfigurableTools_IncludesPython(t *testing.T) {
	names := ConfigurableToolNames()
	found := false
	for _, n := range names {
		if n == "python" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ConfigurableToolNames missing %q: %v", "python", names)
	}

	for _, tool := range ConfigurableTools() {
		if tool.Name == "python" && !tool.Default {
			t.Errorf("python should default to enabled, got Default=%v", tool.Default)
		}
	}
}

// TestConfigurableTools_IncludesRunCode ensures the run_code code-mode tool
// (gap TL7) is runtime-toggleable and opt-out (default enabled), mirroring
// python.
func TestConfigurableTools_IncludesRunCode(t *testing.T) {
	names := ConfigurableToolNames()
	found := false
	for _, n := range names {
		if n == "run_code" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ConfigurableToolNames missing %q: %v", "run_code", names)
	}

	for _, tool := range ConfigurableTools() {
		if tool.Name == "run_code" && !tool.Default {
			t.Errorf("run_code should default to enabled, got Default=%v", tool.Default)
		}
	}
}
