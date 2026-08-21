package agentic

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
)

func countRetryProgress(events []OutputEvent) (attempt1, attempt2, restored int) {
	for _, event := range events {
		if event.Type == EventProgress {
			if strings.Contains(event.Text, "attempt 1/2") {
				attempt1++
			}
			if strings.Contains(event.Text, "attempt 2/2") {
				attempt2++
			}
		}
		if event.Type == EventContent && event.Role == System && strings.Contains(event.Text, "Connection restored") {
			restored++
		}
	}
	return
}

// TestAgent_RetryBudgetResetsAfterSuccess proves Issue 17: once a
// stream failure is recovered by a retry, a LATER failure — in the same turn
// or the next turn — gets a full fresh retry budget (attempts restart at
// 1/MaxRetries), and every episode is visible to the user (progress attempts
// + durable "Connection restored" bubble).
func TestAgent_RetryBudgetResetsAfterSuccess(t *testing.T) {
	// Rate-limit error with a 1ms Retry-After so the retry backoff is ~1ms
	// and the test stays fast.
	rateLimitErr := (&hooks.ErrorContext{
		StatusCode:   http.StatusTooManyRequests,
		Body:         `{"error":{"message":"slow down","type":"rate_limit"}}`,
		IsRateLimit:  true,
		IsRetryable:  true,
		RetryAfterMs: 1,
	}).ToError()

	toolCallEvents := []provider.AssistantMessageEvent{
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolName: "echo", ToolArguments: `{"text":"hi"}`,
		}},
	}
	// NOTE: the recovered text must end with terminal punctuation — after real
	// tool work the premature-stop guard (shouldAutoContinue) treats an
	// unpunctuated answer as truncated and auto-continues the turn.
	textEvents := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered."},
	}

	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("test-scripted-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{err: rateLimitErr},      // turn 1, episode 1: initial failure
			{events: toolCallEvents}, //   episode 1: retry 1 succeeds → tool round continues the turn
			{err: rateLimitErr},      // turn 1, episode 2: initial failure
			{err: rateLimitErr},      //   episode 2: retry 1 ALSO fails
			{events: textEvents},     //   episode 2: retry 2 succeeds — FULL fresh budget was available
			{err: rateLimitErr},      // turn 2, episode 3: initial failure
			{events: textEvents},     //   episode 3: retry 1 succeeds
		},
	}
	provider.RegisterApiProvider(p)

	echoTool := mockTool{
		name: "echo",
		schema: ToolSchema{
			Name:        "echo",
			Description: "echoes input",
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"text": map[string]string{"type": "string"}},
			},
		},
	}
	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "retry-budget-test",
			Api:        p.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		Tools:         []Tool{echoTool},
		StreamOptions: provider.StreamOptions{MaxRetries: 2},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "first"); err != nil {
		t.Fatalf("Run first: %v", err)
	}
	if err := agent.Run(ctx, "second"); err != nil {
		t.Fatalf("Run second: %v", err)
	}

	if got := p.Calls(); got != 7 {
		t.Fatalf("expected 7 Stream calls across 3 episodes, got %d", got)
	}

	attempt1, attempt2, restored := countRetryProgress(obs.Events())
	if attempt1 != 3 {
		t.Errorf("expected 3 fresh attempt-1 retries (one per episode), got %d — retry budget did not reset", attempt1)
	}
	if attempt2 != 1 {
		t.Errorf("expected episode 2 to reach attempt 2/2 (full fresh budget), got %d", attempt2)
	}
	if restored != 3 {
		t.Errorf("expected 3 'Connection restored' bubbles (one per recovered episode), got %d", restored)
	}
}
