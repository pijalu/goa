// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"testing"
)

// mockStickyProvider returns a fixed set of sticky instruction blocks.
type mockStickyProvider struct {
	blocks []string
}

func (m *mockStickyProvider) StickyInstructions() []string { return m.blocks }

// countHistoryContaining counts history messages containing the marker.
func countHistoryContaining(a *Agent, marker string) int {
	n := 0
	for _, m := range a.GetHistory() {
		if strings.Contains(m.Content, marker) {
			n++
		}
	}
	return n
}

// TestStickyInstructions_PersistedOncePerSession verifies sticky blocks are
// appended as user-role history after the first user message and are NOT
// re-appended on subsequent turns while unchanged.
func TestStickyInstructions_PersistedOncePerSession(t *testing.T) {
	cap := registerCapturingProvider("sticky-once")
	agent := NewAgent(Config{
		Model:          testModel(cap.api),
		SystemPrompt:   "system prompt",
		Logger:         NewLogger(Error),
		StickyProvider: &mockStickyProvider{blocks: []string{"STICKY RULES"}},
	})

	ctx := context.Background()
	if err := agent.Run(ctx, "turn one"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countHistoryContaining(agent, "STICKY RULES"); got != 1 {
		t.Fatalf("after turn 1: history contains STICKY RULES %d times, want 1", got)
	}

	if err := agent.Run(ctx, "turn two"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countHistoryContaining(agent, "STICKY RULES"); got != 1 {
		t.Fatalf("after turn 2: history contains STICKY RULES %d times, want 1 (dedup)", got)
	}

	// User-role, never system: strict chat templates reject mid-conversation
	// system messages (LM Studio HTTP 400; Anthropic/Google silently drop).
	pctx := cap.Captured()
	if strings.Contains(pctx.SystemPrompt, "STICKY RULES") {
		t.Error("sticky instructions must not live in the cached system prompt")
	}
	idx := indexOfSlotContaining(pctx.Messages, "STICKY RULES")
	if idx < 0 || pctx.Messages[idx].Role != "user" {
		roles := []string{}
		for _, m := range pctx.Messages {
			roles = append(roles, string(m.Role))
		}
		t.Errorf("sticky block idx=%d, want user-role message; roles=%v", idx, roles)
	}
}

// TestStickyInstructions_RePersistOnChange verifies a changed sticky set
// (skill enabled/disabled/edited) re-appends the new block.
func TestStickyInstructions_RePersistOnChange(t *testing.T) {
	cap := registerCapturingProvider("sticky-change")
	sp := &mockStickyProvider{blocks: []string{"VERSION ONE"}}
	agent := NewAgent(Config{
		Model:          testModel(cap.api),
		SystemPrompt:   "system prompt",
		Logger:         NewLogger(Error),
		StickyProvider: sp,
	})
	ctx := context.Background()
	if err := agent.Run(ctx, "turn one"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sp.blocks = []string{"VERSION TWO"}
	if err := agent.Run(ctx, "turn two"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countHistoryContaining(agent, "VERSION TWO"); got != 1 {
		t.Errorf("changed sticky set must re-persist: found %d, want 1", got)
	}
}

// TestStickyInstructions_RePersistAfterSummarize verifies a summarize
// compaction (which replaces history wholesale, dropping the sticky block)
// triggers re-persistence on the next turn: the compression path resets the
// dedup state via emitCompaction → InvalidateStickyInstructions, and the
// history scan finds the block gone.
func TestStickyInstructions_RePersistAfterSummarize(t *testing.T) {
	cap := registerCapturingProvider("sticky-compact")
	agent := NewAgent(Config{
		Model:          testModel(cap.api),
		SystemPrompt:   "system prompt",
		Logger:         NewLogger(Error),
		StickyProvider: &mockStickyProvider{blocks: []string{"STICKY RULES"}},
	})
	ctx := context.Background()
	if err := agent.Run(ctx, "turn one"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countHistoryContaining(agent, "STICKY RULES"); got != 1 {
		t.Fatalf("pre-compact: %d sticky blocks, want 1", got)
	}

	// Simulate summarize compaction: history replaced wholesale with the
	// summary pair (sticky block gone), then the compression emission resets
	// the dedup state (emitCompaction → InvalidateStickyInstructions).
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "summary request"},
		{Type: Content, Role: Assistant, Content: "summary of the conversation"},
	})
	agent.InvalidateStickyInstructions()

	if err := agent.Run(ctx, "turn two"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countHistoryContaining(agent, "STICKY RULES"); got != 1 {
		t.Errorf("post-compaction turn must re-persist sticky block into the new history: found %d, want 1", got)
	}
}

// TestStickyInstructions_NoDupeOnRestore verifies SetHistory with a history
// that ALREADY carries the identical sticky block does not duplicate it.
func TestStickyInstructions_NoDupeOnRestore(t *testing.T) {
	cap := registerCapturingProvider("sticky-restore")
	agent := NewAgent(Config{
		Model:          testModel(cap.api),
		SystemPrompt:   "system prompt",
		Logger:         NewLogger(Error),
		StickyProvider: &mockStickyProvider{blocks: []string{"STICKY RULES"}},
	})
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "earlier turn"},
		{Type: Content, Role: User, Content: "[sticky instructions — always active]\nSTICKY RULES"},
	})
	if err := agent.Run(context.Background(), "continued"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countHistoryContaining(agent, "STICKY RULES"); got != 1 {
		t.Errorf("restored history already carries the block: found %d, want 1 (no dupe)", got)
	}
}

// TestStickyInstructions_NilProvider verifies a nil provider is a no-op.
func TestStickyInstructions_NilProvider(t *testing.T) {
	cap := registerCapturingProvider("sticky-nil")
	agent := NewAgent(Config{
		Model:        testModel(cap.api),
		SystemPrompt: "system prompt",
		Logger:       NewLogger(Error),
	})
	if err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countHistoryContaining(agent, "sticky_skill"); got != 0 {
		t.Errorf("nil provider must persist nothing, found %d", got)
	}
}
