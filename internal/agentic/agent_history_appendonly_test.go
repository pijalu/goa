// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// The append-only history contract (bugs.md prefix-cache contract): every
// provider request must carry the previous request's history verbatim, and
// the ONLY permitted in-place change is compression tool_elision — which may
// rewrite each already-sent tool call/result exactly once (raw payload →
// elision form). A tool message must never mutate again after its elision.
//
// Consecutive requests are aligned left-to-right (elision never reorders):
// each prev message must either match the next cursor exactly, transition to
// its elided form, or be dropped as the orphaned result of an elided call;
// anything left over in next is appended tail growth.

// kindOf classifies a provider message for the alignment walk.
type msgKind int

const (
	kindOther  msgKind = iota // user prompt / plain assistant text (append-once)
	kindCall                  // assistant message carrying tool_call blocks
	kindNote                  // assistant elision note (elided form of a call)
	kindResult                // tool result message
)

func classify(m provider.Message) (kind msgKind, callID, toolName string, resultElided bool) {
	kind = kindOther
	for _, b := range m.Content {
		switch b.Type {
		case provider.ContentBlockToolCall:
			return kindCall, b.ToolCallID, b.ToolName, false
		case provider.ContentBlockToolResult:
			return kindResult, b.ToolCallID, "", b.Text == elidedToolResultContent
		}
	}
	for _, b := range m.Content {
		if b.Type == provider.ContentBlockText &&
			strings.HasPrefix(b.Text, "[earlier call") && strings.HasSuffix(b.Text, " elided]") {
			return kindNote, "", "", false
		}
	}
	return kindOther, "", "", false
}

// equalWire compares the wire-relevant fields of two provider messages
// (role + every content block's payload).
func equalWire(a, b provider.Message) bool {
	if a.Role != b.Role || len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		x, y := a.Content[i], b.Content[i]
		if x.Type != y.Type || x.Text != y.Text || x.Thinking != y.Thinking ||
			x.ToolCallID != y.ToolCallID || x.ToolName != y.ToolName ||
			x.ToolArguments != y.ToolArguments {
			return false
		}
	}
	return true
}

// noteNamesTool reports whether an elision note names the given tool.
func noteNamesTool(note provider.Message, toolName string) bool {
	for _, b := range note.Content {
		if b.Type == provider.ContentBlockText && strings.Contains(b.Text, toolName) {
			return true
		}
	}
	return false
}

// TestAgent_HistoryAppendOnlyExceptElision is the end-to-end property check
// for the append-only history guarantee. The scenario drives an 8-round tool
// loop across the elision trigger so compression fires repeatedly mid-turn,
// recording every round's provider context; each consecutive request pair is
// then validated against the contract above, and every elision is tracked so
// no already-sent message is elided twice or mutated after its elision.
func TestAgent_HistoryAppendOnlyExceptElision(t *testing.T) {
	p := &gateProbeProvider{
		api: provider.Api(fmt.Sprintf("append-only-probe-%d", testProviderCounter.Add(1))),
		// 12 tool rounds so the 50% trigger is crossed in THREE separate
		// elision waves (rounds ~6, ~9, ~11): notes elided in an early wave
		// persist through later waves, which is what actually proves an elided
		// message is never mutated again.
		toolRounds: 12,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{hugeResultTool{
			name:   "huge_tool",
			schema: ToolSchema{Name: "huge_tool", Description: "test"},
			size:   2400,
		}},
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 2000,
			Strategy:  CompressionToolElision,
			// Trigger at 50% so elision fires repeatedly through the turn —
			// multiple compression passes against a growing history is what
			// stresses the append-only / elide-exactly-once invariant.
			Thresholds: CompressionThresholds{TriggerPercent: 50},
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	ctxs := p.recorded()
	if len(ctxs) != 13 {
		t.Fatalf("expected 13 stream rounds, got %d", len(ctxs))
	}

	sawElision := false
	// Per tool-call ID: has this call/result already been elided once?
	elidedCalls := map[string]bool{}
	elidedResults := map[string]bool{}

	for r := 1; r < len(ctxs); r++ {
		round := fmt.Sprintf("round %d -> %d", r-1, r)
		if alignAppendOnly(t, round, ctxs[r-1].Messages, ctxs[r].Messages, elidedCalls, elidedResults) {
			sawElision = true
		}
	}

	if !sawElision {
		t.Error("no elision observed across 13 rounds; the compression gate did not exercise the elision path this test exists to validate")
	}
}

// alignAppendOnly walks one prev/next request pair left-to-right, reporting
// every append-only violation via t.Error and marking elisions in the shared
// maps. It returns whether this pair contained any elision. Remaining next
// messages past the aligned prefix are appended tail growth — always legal.
func alignAppendOnly(t *testing.T, round string, prev, next []provider.Message, elidedCalls, elidedResults map[string]bool) (sawElision bool) {
	t.Helper()
	j := 0 // cursor into next
	for i, pm := range prev {
		advance, elided := alignOneMessage(t, round, i, j, pm, next, elidedCalls, elidedResults)
		j += advance
		sawElision = sawElision || elided
		if advance < 0 {
			break // prev truncated: no counterpart in next
		}
	}
	return sawElision
}

// alignOneMessage aligns a single prev message at (i) against next[j],
// returning how far the next cursor advances (-1 = abort the pair) and
// whether an elision occurred. Elision state is updated in the shared maps.
func alignOneMessage(t *testing.T, round string, i, j int, pm provider.Message, next []provider.Message, elidedCalls, elidedResults map[string]bool) (advance int, elided bool) {
	t.Helper()
	pk, pCallID, pTool, pResElided := classify(pm)
	frozen := pk == kindResult && elidedResults[pCallID] || pk == kindCall && elidedCalls[pCallID] || pk == kindNote

	if frozen {
		return alignFrozen(t, round, i, j, pm, pk, pResElided, next)
	}
	if j >= len(next) {
		t.Errorf("%s: prev idx %d has no counterpart in next (history truncated, not elided)\n  msg: %s", round, i, wire(pm))
		return -1, false
	}
	nm := next[j]
	nk, _, _, _ := classify(nm)

	if equalWire(pm, nm) {
		return 1, false // unchanged, in order
	}
	if adv, el, ok := elideCall(t, round, pm, nm, pk, nk, pTool, pCallID, elidedCalls); ok {
		return adv, el
	}
	if adv, el, ok := elideResult(t, round, pk, nk, pCallID, pResElided, nm, elidedResults); ok {
		return adv, el
	}
	if adv, el, ok := dropOrphanedResult(t, round, pk, pCallID, elidedCalls, elidedResults); ok {
		return adv, el
	}
	t.Errorf("%s: non-elision mutation at prev idx %d\n  prev: %s\n  next: %s", round, i, wire(pm), wire(nm))
	return 1, false // resync to keep subsequent errors meaningful
}

// elideCall handles an assistant call elided into its note (exactly once).
func elideCall(t *testing.T, round string, pm, nm provider.Message, pk, nk msgKind, pTool, pCallID string, elidedCalls map[string]bool) (advance int, elided, matched bool) {
	t.Helper()
	if pk != kindCall || nk != kindNote || !noteNamesTool(nm, pTool) {
		return 0, false, false
	}
	if elidedCalls[pCallID] {
		t.Errorf("%s: call %s elided twice", round, pCallID)
	}
	elidedCalls[pCallID] = true
	return 1, true, true
}

// elideResult handles a tool result body elided in place (exactly once).
func elideResult(t *testing.T, round string, pk, nk msgKind, pCallID string, pResElided bool, nm provider.Message, elidedResults map[string]bool) (advance int, elided, matched bool) {
	t.Helper()
	if pk != kindResult || nk != kindResult || pResElided || !resultIsElidedForm(nm) {
		return 0, false, false
	}
	if elidedResults[pCallID] {
		t.Errorf("%s: tool result %s elided twice", round, pCallID)
	}
	elidedResults[pCallID] = true
	return 1, true, true
}

// dropOrphanedResult handles a tool result dropped because its call (seen
// just before, since the call precedes its result) was elided this round, so
// migrateMessages removed the now-unreferenced result.
func dropOrphanedResult(t *testing.T, round string, pk msgKind, pCallID string, elidedCalls, elidedResults map[string]bool) (advance int, elided, matched bool) {
	t.Helper()
	if pk != kindResult || !elidedCalls[pCallID] {
		return 0, false, false
	}
	if elidedResults[pCallID] {
		t.Errorf("%s: tool result %s removed after a prior elision", round, pCallID)
	}
	elidedResults[pCallID] = true
	return 0, true, true // consume prev only; nothing in next advances
}

// alignFrozen validates that a message elided in an earlier round is now
// FROZEN: unchanged at the next cursor. An already-elided result may be
// dropped once its call's note is in place (the pair collapse), allowed once.
func alignFrozen(t *testing.T, round string, i, j int, pm provider.Message, pk msgKind, pResElided bool, next []provider.Message) (advance int, elided bool) {
	t.Helper()
	if j < len(next) && equalWire(pm, next[j]) {
		return 1, false
	}
	if pk == kindResult && pResElided {
		return 0, false // consumed (dropped); nothing in next advances
	}
	t.Errorf("%s: previously elided message (prev idx %d) mutated or vanished\n  msg: %s", round, i, wire(pm))
	return 0, false
}

// resultIsElidedForm reports whether a tool-result message body is the
// elided placeholder.
func resultIsElidedForm(m provider.Message) bool {
	for _, b := range m.Content {
		if b.Type == provider.ContentBlockToolResult {
			return b.Text == elidedToolResultContent
		}
	}
	return false
}

// wire renders a message's content blocks compactly for failure output.
func wire(m provider.Message) string {
	var b strings.Builder
	b.WriteString(string(m.Role))
	b.WriteString("{")
	for i, c := range m.Content {
		if i > 0 {
			b.WriteString(" ")
		}
		switch c.Type {
		case provider.ContentBlockToolCall:
			fmt.Fprintf(&b, "call(%s,%s,%q)", c.ToolCallID, c.ToolName, clip(c.ToolArguments))
		case provider.ContentBlockToolResult:
			fmt.Fprintf(&b, "result(%s,%q)", c.ToolCallID, clip(c.Text))
		case provider.ContentBlockText:
			fmt.Fprintf(&b, "text(%q)", clip(c.Text))
		default:
			fmt.Fprintf(&b, "%s(%q)", c.Type, clip(c.Text))
		}
	}
	b.WriteString("}")
	return b.String()
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
