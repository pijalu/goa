package agentic

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestAgentCacheKeyRotatesOnClear(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ID: "model", Provider: provider.ProviderOpenAI}})
	first := a.cacheKey(a.cfg.Model)
	a.Clear()
	if got := a.cacheKey(a.cfg.Model); got == first {
		t.Fatal("clear retained cache identity")
	}
}

func TestAgentCacheKeyKeepsAppendIdentityAndRotatesOnReplacement(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ID: "model", Provider: provider.ProviderOpenAI}})
	first := a.cacheKey(a.cfg.Model)
	if first == "" {
		t.Fatal("cache key is empty")
	}
	a.history = append(a.history, Message{Role: User, Content: "append"})
	if got := a.cacheKey(a.cfg.Model); got != first {
		t.Fatal("ordinary append rotated cache identity")
	}
	a.SetHistory([]Message{{Role: User, Content: "replacement"}})
	if got := a.cacheKey(a.cfg.Model); got == first {
		t.Fatal("history replacement retained cache identity")
	}
}

func TestBuildProviderHistoryDoesNotMutateAgentHistory(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "system"})
	a.SetHistory([]Message{
		{Role: System, Content: "system"},
		{Role: System, Content: "runtime note"},
	})
	before := a.GetHistory()
	_ = a.buildProviderContext(context.Background())
	after := a.GetHistory()
	if len(before) != len(after) || before[1].Role != after[1].Role {
		t.Fatalf("provider projection mutated history: before=%#v after=%#v", before, after)
	}
}

func TestProviderContextSnapshotSurvivesLaterHistoryMutation(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ID: "model"}})
	a.SetHistory([]Message{{Role: User, Content: "original"}})
	ctx := a.buildProviderContext(context.Background())
	a.SetHistory([]Message{{Role: User, Content: "replacement"}})
	if got := ctx.Messages[0].Content[0].Text; got != "original" {
		t.Fatalf("recorded provider context changed after replacement: %q", got)
	}
}
