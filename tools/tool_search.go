// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
)

// catalogBudget caps the compact catalog text embedded in the tool_search
// schema description. The catalog is the model's discovery surface for
// deferred tools; it must stay small so the loader schema itself is a cheap
// addition to the eager block (~0.5KB per the P1 plan).
const catalogBudget = 512

// catalogDescriptionRunes caps each deferred tool's one-line description in
// the catalog.
const catalogDescriptionRunes = 80

// ToolSearchTool is the deferred-tool loader (P1 deferred tool loading).
// Its schema is tiny (one query string) and its description embeds the
// compact catalog of deferred tools (name + one-line description, budgeted).
// The model calls it to discover and pull deferred tool schemas on demand
// instead of shipping every schema with every request.
//
// Query semantics:
//
//	select:Name1,Name2  — load the named deferred tools (exact match);
//	                      returns their full schemas and sets
//	                      Meta[MetaLoadTools] so the agent loop exposes them
//	                      next round.
//	<keywords>           — discovery only: returns matching deferred tool
//	                      schemas without loading them.
//	unknown name         — fallback: lists all available deferred tools.
//
// The loader is model-free: resolving a query never calls an LLM.
type ToolSearchTool struct {
	agentic.BaseTool
	// reg is the app-level registry the loader queries lazily for the
	// deferred tool set (the tool set is fully registered by the time the
	// loader's schema is first built).
	reg *ToolRegistry

	catalogOnce sync.Once
	catalogText string
}

// NewToolSearchTool creates the loader bound to the app-level registry. The
// compact catalog is computed lazily on first Schema() call so registration
// order does not matter.
func NewToolSearchTool(reg *ToolRegistry) *ToolSearchTool {
	return &ToolSearchTool{reg: reg}
}

// IsDeferredToolLoader identifies tool_search as the deferred-tool loader
// (agentic.DeferredToolLoader).
func (t *ToolSearchTool) IsDeferredToolLoader() bool { return true }

// Schema returns the tiny loader schema. The description embeds the compact
// catalog of deferred tools so the model can discover and pull them.
func (t *ToolSearchTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "tool_search",
		Description: t.description(),
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type": "string",
					"description": "Tool name or keyword to search for. " +
						"Prefix with 'select:' to load exact tools, e.g. 'select:webfetch,memento'. " +
						"Plain keywords match names and descriptions (discovery only). " +
						"Unknown names list all available deferred tools.",
				},
			},
			"required": []string{"query"},
		},
	}
}

// Execute runs the loader with a background context (base Tool contract).
func (t *ToolSearchTool) Execute(input string) (string, error) {
	res, err := t.ExecuteWithResult(input)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}

// ExecuteWithResult resolves the query and returns the matched tool schemas
// as JSON. For select: queries it also sets Meta[MetaLoadTools] to the
// comma-separated loaded names so the agent loop exposes them next round.
func (t *ToolSearchTool) ExecuteWithResult(input string) (agentic.ToolResult, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return agentic.ToolResult{}, fmt.Errorf("tool_search: invalid input: %w", err)
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return agentic.ToolResult{Output: t.catalog()}, nil
	}

	byName := t.deferredByName()
	if sel, ok := strings.CutPrefix(query, "select:"); ok {
		return t.resolveSelect(sel, byName)
	}
	return t.resolveKeywords(query, byName)
}

// resolveSelect loads the named deferred tools (exact match, order preserved)
// and returns their full schemas. Unknown names are dropped; when nothing
// matches, the fallback catalog is returned instead.
func (t *ToolSearchTool) resolveSelect(sel string, byName map[string]agentic.Tool) (agentic.ToolResult, error) {
	var names []string
	for _, n := range strings.Split(sel, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := byName[n]; ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return agentic.ToolResult{Output: "No matching deferred tools. " + t.catalog()}, nil
	}
	out, err := json.Marshal(t.schemasFor(names, byName))
	if err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{
		Output: string(out),
		Meta:   map[string]string{agentic.MetaLoadTools: strings.Join(names, ",")},
	}, nil
}

// resolveKeywords matches deferred tools by name or description substring.
// Discovery only: no load is requested — the model picks exact names with
// select: after seeing the schemas.
func (t *ToolSearchTool) resolveKeywords(query string, byName map[string]agentic.Tool) (agentic.ToolResult, error) {
	q := strings.ToLower(query)
	var names []string
	for n := range byName {
		if strings.Contains(strings.ToLower(n), q) || strings.Contains(strings.ToLower(byName[n].Schema().Description), q) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return agentic.ToolResult{Output: fmt.Sprintf("No deferred tools match %q. %s", query, t.catalog())}, nil
	}
	sort.Strings(names)
	out, err := json.Marshal(t.schemasFor(names, byName))
	if err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{Output: string(out)}, nil
}

// schemasFor returns the full schemas of the given names in order.
func (t *ToolSearchTool) schemasFor(names []string, byName map[string]agentic.Tool) []agentic.ToolSchema {
	out := make([]agentic.ToolSchema, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n].Schema())
	}
	return out
}

// deferredTools returns the deferred-eligible tools in the app-level registry
// (alpha order via All()). Nil-safe.
func (t *ToolSearchTool) deferredTools() []agentic.Tool {
	if t.reg == nil {
		return nil
	}
	var out []agentic.Tool
	for _, tool := range t.reg.All() {
		if d, ok := tool.(agentic.Deferred); ok && d.Deferred() {
			out = append(out, tool)
		}
	}
	return out
}

// deferredByName indexes deferred tools by name.
func (t *ToolSearchTool) deferredByName() map[string]agentic.Tool {
	tools := t.deferredTools()
	m := make(map[string]agentic.Tool, len(tools))
	for _, tool := range tools {
		m[tool.Schema().Name] = tool
	}
	return m
}

// description returns the loader schema description embedding the catalog.
func (t *ToolSearchTool) description() string {
	return "Search and load deferred tools. Deferred tools are withheld from the main tool set to save context; load them here before calling them.\n\n" + t.catalog()
}

// catalog returns the compact, byte-stable catalog of deferred tools
// (name + one-line description), computed once. Always lists at least one
// tool; overflow is summarized with a count line.
func (t *ToolSearchTool) catalog() string {
	t.catalogOnce.Do(func() {
		tools := t.deferredTools()
		var b strings.Builder
		b.WriteString("Deferred tools available to load (call with \"select:Name1,Name2\"):\n")
		listed := 0
		for _, tool := range tools {
			s := tool.Schema()
			line := fmt.Sprintf("- %s: %s\n", s.Name, firstRunes(s.Description, catalogDescriptionRunes))
			// Always list at least one; after that, respect the budget.
			if listed > 0 && b.Len()+len(line) > catalogBudget {
				break
			}
			b.WriteString(line)
			listed++
		}
		if remaining := len(tools) - listed; remaining > 0 {
			fmt.Fprintf(&b, "- … and %d more (unknown names list all)\n", remaining)
		}
		t.catalogText = b.String()
	})
	return t.catalogText
}

// firstRunes returns the first n runes of s on a single line.
func firstRunes(s string, n int) string {
	s = strings.SplitN(s, "\n", 2)[0]
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return s
}
