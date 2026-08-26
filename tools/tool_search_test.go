// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/todo"
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
func TestDefaultToolsTodoAndVerifyAreDeferred(t *testing.T) {
	reg := tools.NewToolRegistry()
	deferred := []agentic.Tool{
		&tools.TerminalsTool{}, &tools.WebFetchTool{}, &tools.BGExecTool{},
		&tools.MementoTool{}, &tools.SmartSearchTool{}, &tools.SSHBashTool{},
		&tools.SessionSearchTool{}, &tools.SessionEventReadTool{},
		&tools.VerifyTool{}, &todo.TodoListTool{}, &tools.LSPTool{},
	}
	for _, tool := range deferred {
		reg.Register(tool)
	}
	loader := tools.NewToolSearchTool(reg)
	reg.Register(loader)
	all := append([]agentic.Tool{}, deferred...)
	all = append(all, loader)
	eagerSchemas := agentic.NewToolRegistry(all).Schemas()
	eager := make([]string, len(eagerSchemas))
	for i, schema := range eagerSchemas {
		eager[i] = schema.Name
	}
	for _, name := range []string{"todo_list", "verify", "lsp"} {
		if containsName(eager, name) {
			t.Fatalf("%s unexpectedly included in eager schemas: %v", name, eager)
		}
	}
	res, err := loader.ExecuteWithResult(`{"query":"select:todo_list,verify,lsp"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Meta[agentic.MetaLoadTools]; got != "todo_list,verify,lsp" {
		t.Fatalf("loaded tools = %q", got)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

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

// The results catalog respects its byte budget.
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

	// The annotated catalog ships in RESULTS (empty query = full catalog).
	res, err := loader.ExecuteWithResult(`{"query":""}`)
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	catalog := res.Output
	idx := strings.Index(catalog, "Deferred tools available")
	if idx < 0 {
		t.Fatal("catalog header not found in result")
	}
	catalog = catalog[idx:]
	if len(catalog) > 600 { // budget 512 + header slack
		t.Errorf("catalog = %d bytes, want ≤ ~512", len(catalog))
	}
	if !strings.Contains(catalog, "and ") || !strings.Contains(catalog, "more") {
		t.Errorf("expected count-summary for truncated catalog, got: %.160s", catalog)
	}
}

// The schema description's name-only list must track registry changes
// IMMEDIATELY: tools can be registered/unregistered at runtime (/tools,
// /config, MCP), and the once-cached catalog went stale after such toggles
// (bugs.md 2026-08-26). Regression: Schema() computed once via sync.Once.
func TestToolSearchDescriptionTracksRegistryChanges(t *testing.T) {
	reg, _ := deferredProbeSet()
	loader := tools.NewToolSearchTool(reg)

	before := loader.Schema().Description
	if strings.Contains(before, "long_desc_probe") {
		t.Fatal("probe tool unexpectedly listed before registration")
	}

	reg.Register(&longDescDeferredTool{})
	afterRegister := loader.Schema().Description
	if !strings.Contains(afterRegister, "long_desc_probe") {
		t.Error("description not refreshed after Register — catalog went stale")
	}

	reg.Unregister("long_desc_probe")
	afterUnregister := loader.Schema().Description
	if strings.Contains(afterUnregister, "long_desc_probe") {
		t.Error("description still lists tool after Unregister — catalog went stale")
	}
}

// The name-only description list is capped: beyond the budget it summarizes
// the remainder instead of listing every name.
func TestToolSearchNameCatalogCap(t *testing.T) {
	reg := tools.NewToolRegistry()
	for i := 0; i < 70; i++ {
		reg.Register(&namedDeferredProbe{name: fmt.Sprintf("deferred_probe_%02d", i)})
	}
	loader := tools.NewToolSearchTool(reg)
	desc := loader.Schema().Description

	if !strings.Contains(desc, "and 6 more") { // 70 - 64 listed
		t.Errorf("expected overflow summary for capped name list, got:\n%s", desc)
	}
	if strings.Contains(desc, "deferred_probe_69") {
		t.Error("name list exceeded its cap")
	}
	if !strings.Contains(desc, "deferred_probe_00") {
		t.Error("name list missing its first entry")
	}
}

// namedDeferredProbe is a deferred tool with a configurable name for cap tests.
type namedDeferredProbe struct {
	agentic.BaseTool
	name string
}

func (p *namedDeferredProbe) Deferred() bool { return true }

func (p *namedDeferredProbe) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: p.name, Description: "probe", Schema: map[string]interface{}{"type": "object"}}
}

func (*namedDeferredProbe) Execute(input string) (string, error) { return "", nil }

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

// scheduleToolNames lists the schedule tools under test.
var scheduleToolNames = []string{"schedule_create", "schedule_delete", "schedule_list"}

// TestScheduleToolsAreDeferred pins the 2026-08-21 bugs.md feature request
// "schedule_create/delete/list deferred to tool_search": the scheduler tools
// must NOT ship in the eager schema block (they implement Deferred in
// tools/deferred.go) — the model discovers them via the tool_search catalog
// and loads them on demand with select:schedule_create,…. This deliberately
// reverses the 2026-08-17 NOT-A-BUG decision that pinned them eager. Guards
// against someone removing the Deferred markers.
func TestScheduleToolsAreDeferred(t *testing.T) {
	reg := newScheduleTestRegistry()
	areg := agentic.NewToolRegistry(reg.All())

	// Deferral is active (sanity: a real deferred tool is withheld).
	if _, unloaded := areg.DeferredStatus("webfetch"); !unloaded {
		t.Fatal("webfetch should be deferred (deferral active) but is not")
	}
	assertScheduleToolsWithheld(t, reg, areg)
	assertScheduleToolsLoadable(t, areg)
}

// newScheduleTestRegistry builds a registry with the full deferred family (so
// deferral crosses the activation threshold), the schedule tools, and the
// tool_search loader last (bootstrap order).
func newScheduleTestRegistry() *tools.ToolRegistry {
	reg := tools.NewToolRegistry()
	for _, d := range []agentic.Tool{
		&tools.TerminalsTool{}, &tools.WebFetchTool{}, &tools.BGExecTool{},
		&tools.MementoTool{}, &tools.SmartSearchTool{}, &tools.SSHBashTool{},
		&tools.SessionSearchTool{}, &tools.SessionEventReadTool{},
	} {
		reg.Register(d)
	}
	reg.Register(&tools.ScheduleCreateTool{})
	reg.Register(&tools.ScheduleDeleteTool{})
	reg.Register(&tools.ScheduleListTool{})
	reg.Register(tools.NewToolSearchTool(reg)) // loader, last (bootstrap order)
	return reg
}

// assertScheduleToolsWithheld asserts the schedule tools are deferred: absent
// from the eager schema block and advertised in the tool_search catalog.
func assertScheduleToolsWithheld(t *testing.T, reg *tools.ToolRegistry, areg *agentic.ToolRegistry) {
	t.Helper()
	eager := map[string]bool{}
	for _, s := range areg.Schemas() {
		eager[s.Name] = true
	}
	catalog := catalogText(reg)
	for _, want := range scheduleToolNames {
		if _, unloaded := areg.DeferredStatus(want); !unloaded {
			t.Errorf("%s is EAGER, want deferred (loadable via tool_search)", want)
		}
		if eager[want] {
			t.Errorf("%s present in eager schema block, want withheld", want)
		}
		if !strings.Contains(catalog, want) {
			t.Errorf("%s missing from tool_search deferred catalog", want)
		}
	}
}

// assertScheduleToolsLoadable loads the schedule tools via the select: path
// and asserts they become callable (status cleared + schema served).
func assertScheduleToolsLoadable(t *testing.T, areg *agentic.ToolRegistry) {
	t.Helper()
	loaded := areg.LoadDeferred(scheduleToolNames)
	if len(loaded) != len(scheduleToolNames) {
		t.Fatalf("LoadDeferred loaded %v, want all %d schedule tools", loaded, len(scheduleToolNames))
	}
	served := map[string]bool{}
	for _, s := range areg.Schemas() {
		served[s.Name] = true
	}
	for _, name := range scheduleToolNames {
		if _, unloaded := areg.DeferredStatus(name); unloaded {
			t.Errorf("%s still deferred after LoadDeferred", name)
		}
		if _, ok := areg.Get(name); !ok {
			t.Errorf("%s not in registry after LoadDeferred", name)
		}
		if !served[name] {
			t.Errorf("%s schema not served after LoadDeferred", name)
		}
	}
}

// catalogText returns the loader's catalog description for assertions.
func catalogText(reg *tools.ToolRegistry) string {
	l, ok := reg.Get("tool_search")
	if !ok {
		return ""
	}
	return l.Schema().Description
}
