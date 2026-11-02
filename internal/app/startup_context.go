// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// Startup context accounting (bugs.md B52): the fixed payload every provider
// request carries before any conversation history — the assembled system
// prompt plus the eager tool schemas (deferred tools ship only the
// tool_search catalog). StartupContextBreakdown reports it per component so
// waste (duplicates, stale sections, over-eager schemas) stays visible, and
// the budget-pinning test (startup_context_test.go) keeps it bounded.

// StartupContextComponent is one measured piece of the first-request payload.
type StartupContextComponent struct {
	Name   string // stable component id, e.g. "system:mode-prompt", "tool:bash"
	Bytes  int    // serialized size in bytes (prompt text / schema JSON)
	Tokens int    // estimated tokens (4 chars/token, matching systemPromptBudget)
	Note   string // optional human-readable remark
}

// TokensFromBytes estimates tokens the way the system-prompt budget does.
func TokensFromBytes(b int) int {
	if b <= 0 {
		return 0
	}
	return (b + 3) / 4
}

// startupContextComponents is the shared shape behind the breakdown and its
// formatting; components are already ordered heaviest-last by the callers.
type startupContextComponents []StartupContextComponent

func (cs startupContextComponents) totalBytes() int {
	total := 0
	for _, c := range cs {
		total += c.Bytes
	}
	return total
}

// StartupContextBreakdown computes the per-component byte/token cost of the
// fixed first-request payload for this session: every system-prompt section
// plus each eager tool schema. It is pure with respect to the subsystems
// snapshot it receives (no provider traffic), so tests and the budget guard
// can call it repeatedly. Deferred tools are withheld by the registry and
// appear only via the tool_search catalog; they are reported in aggregate as
// "tool:tool_search (catalog)" inside the tool_search schema.
func StartupContextBreakdown(subs *subsystems) startupContextComponents {
	out := systemPromptBreakdown(subs)
	out = append(out, toolSchemaBreakdown(subs)...)
	return out
}

// systemPromptBreakdown reports each system-prompt section post-budget (what
// is actually sent), flagging sections the budget dropped and duplicated
// texts.
func systemPromptBreakdown(subs *subsystems) startupContextComponents {
	var out startupContextComponents
	budget := systemPromptBudget(modelContextWindow(subs))
	sections := buildSystemPromptSections(subs)
	_, sent := assembleSystemPrompt(sections, budget)
	sentCount := make(map[string]int, len(sent))
	for _, t := range sent {
		sentCount[t]++
	}
	for _, s := range sections {
		if sentCount[s.text] == 0 {
			out = append(out, StartupContextComponent{
				Name: "system:" + s.name,
				Note: "dropped by system-prompt budget",
			})
			continue
		}
		note := ""
		if sentCount[s.text] > 1 {
			note = fmt.Sprintf("text appears %dx in the prompt — duplicate", sentCount[s.text])
		}
		out = append(out, StartupContextComponent{
			Name:   "system:" + s.name,
			Bytes:  len(s.text),
			Tokens: TokensFromBytes(len(s.text)),
			Note:   note,
		})
	}
	return out
}

// toolSchemaBreakdown reports what the agent registry actually sends: the
// live registry holds every tool, and PartitionDeferred is the single source
// of truth for the eager/deferred split, so this is exactly the schemas a
// first request carries — eager tools + the tool_search loader (whose
// description embeds the compact catalog). Deferred tools are counted but
// not itemized (they ship only via the catalog).
func toolSchemaBreakdown(subs *subsystems) startupContextComponents {
	if subs.toolRegistry == nil {
		return nil
	}
	all := subs.toolRegistry.All()
	loaderName, deferredNames := agentic.PartitionDeferred(all)
	deferredSet := make(map[string]bool, len(deferredNames))
	for _, n := range deferredNames {
		deferredSet[n] = true
	}
	out := make(startupContextComponents, 0, len(all))
	var eagerTotal int
	var deferredCount int
	for _, t := range all {
		s := t.Schema()
		b, err := json.Marshal(s)
		if err != nil {
			continue
		}
		if deferredSet[s.Name] {
			deferredCount++
			continue // withheld: ships only via the tool_search catalog
		}
		out = append(out, StartupContextComponent{
			Name:   "tool:" + s.Name,
			Bytes:  len(b),
			Tokens: TokensFromBytes(len(b)),
		})
		eagerTotal += len(b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	out = append(out, StartupContextComponent{
		Name:   "tool:eager-total",
		Bytes:  eagerTotal,
		Tokens: TokensFromBytes(eagerTotal),
		Note:   fmt.Sprintf("loader=%s, %d deferred tools withheld", loaderName, deferredCount),
	})
	return out
}

// Report renders the breakdown as a fixed-width text table (bytes and
// estimated tokens per component; sections in assembly order, tools heaviest
// first), with a TOTAL row last.
func (cs startupContextComponents) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-34s %9s %8s  %s\n", "component", "bytes", "~tokens", "note")
	total := cs.totalBytes()
	for _, c := range cs {
		fmt.Fprintf(&b, "%-34s %9d %8d  %s\n", c.Name, c.Bytes, c.Tokens, c.Note)
	}
	fmt.Fprintf(&b, "%-34s %9d %8d\n", "TOTAL", total, TokensFromBytes(total))
	return b.String()
}
