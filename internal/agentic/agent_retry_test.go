// SPDX-License-Identifier: GPL-3.0-or-later

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

func TestAgent_RetriesStreamError(t *testing.T) {
	p := registerFlakyTestProvider(1, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered"},
	})
	agent := NewAgent(Config{
		Model:         testModel(p.API()),
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		StreamOptions: provider.StreamOptions{MaxRetries: 2},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var contents []string
	var endWithError bool
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == Assistant {
			contents = append(contents, e.Text)
		}
		if e.Type == EventEnd && e.Text != "" {
			endWithError = true
		}
	}
	if endWithError {
		t.Error("expected retry to succeed, but EventEnd carried an error")
	}
	if !containsContent(contents, "Recovered") {
		t.Errorf("expected recovered assistant content, got %q", contents)
	}
}

func TestAgent_RetriesStreamError_EmitsSystemNotification(t *testing.T) {
	p := registerFlakyTestProvider(1, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered"},
	})
	agent := NewAgent(Config{
		Model:         testModel(p.API()),
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		StreamOptions: provider.StreamOptions{MaxRetries: 2},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var notifications []OutputEvent
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System && e.Metadata != nil && e.Metadata["category"] == "system-notification" {
			notifications = append(notifications, e)
		}
	}
	// The retry lifecycle is fully visible (Issue 17): the failure
	// bubble ("retrying") followed by the durable "Connection restored"
	// confirmation once a retry succeeds.
	if len(notifications) != 2 {
		t.Fatalf("expected 2 system notifications (retrying + restored), got %d", len(notifications))
	}
	if !strings.Contains(notifications[0].Text, "retrying") {
		t.Errorf("expected first notification to mention retrying, got %q", notifications[0].Text)
	}
	if !strings.Contains(notifications[1].Text, "Connection restored") {
		t.Errorf("expected second notification to confirm reconnection, got %q", notifications[1].Text)
	}
}

// testResponseError is a test double for provider.HTTPResponseError.
type testResponseError struct {
	status int
	body   string
}

func (e *testResponseError) Error() string        { return fmt.Sprintf("test error %d", e.status) }
func (e *testResponseError) StatusCode() int      { return e.status }
func (e *testResponseError) ResponseBody() string { return e.body }

func TestFormatRetryMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "openai-style error body",
			err: &testResponseError{
				status: 503,
				body:   `{"error":{"message":"Inference is temporarily unavailable","type":"server_error","code":"failover_exhausted"}}`,
			},
			want: "Error: 503 - Inference is temporarily unavailable (failover_exhausted) - retrying",
		},
		{
			name: "plain http error",
			err:  &testResponseError{status: 500, body: "internal server error"},
			want: "Error: 500 - internal server error - retrying",
		},
		{
			name: "provider error from hooks",
			err: (&hooks.ErrorContext{
				StatusCode: 503,
				Body:       `{"error":{"message":"Inference is temporarily unavailable","code":"failover_exhausted"}}`,
			}).ToError(),
			want: "Error: 503 - Inference is temporarily unavailable (failover_exhausted) - retrying",
		},
		{
			name: "generic error",
			err:  fmt.Errorf("SSE stream ended prematurely"),
			want: "Error: SSE stream ended prematurely - retrying",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatRetryMessage(tc.err)
			if got != tc.want {
				t.Errorf("formatRetryMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgent_StreamErrorRetriesExhausted(t *testing.T) {
	p := registerFlakyTestProvider(3, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Never reached"},
	})
	agent := NewAgent(Config{
		Model:         testModel(p.API()),
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		StreamOptions: provider.StreamOptions{MaxRetries: 2},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := agent.Run(ctx, "prompt")
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}
	if !strings.Contains(err.Error(), "LLM connection lost after retries") {
		t.Errorf("expected retries-exhausted error, got %v", err)
	}
}

func TestAgent_RetriesStreamError_HonorsMaxRetries(t *testing.T) {
	p := registerFlakyTestProvider(2, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered on third retry"},
	})
	agent := NewAgent(Config{
		Model:         testModel(p.API()),
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		StreamOptions: provider.StreamOptions{MaxRetries: 3},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var contents []string
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == Assistant {
			contents = append(contents, e.Text)
		}
	}
	if !containsContent(contents, "Recovered on third retry") {
		t.Errorf("expected content recovered after configured retries, got %q", contents)
	}
}

func TestAgent_RetriesInitialStreamError_408(t *testing.T) {
	startErr := (&hooks.ErrorContext{
		StatusCode:  http.StatusRequestTimeout,
		Body:        `{"error":{"message":"request timeout","type":"request_timeout"}}`,
		IsRetryable: true,
	}).ToError()
	p := registerFlakyStartProvider(1, startErr, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered after 408"},
	})
	agent := NewAgent(Config{
		Model:         testModel(p.API()),
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		StreamOptions: provider.StreamOptions{MaxRetries: 2},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var contents []string
	var retryNotifications int
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == Assistant {
			contents = append(contents, e.Text)
		}
		if e.Type == EventContent && e.Role == System && e.Metadata != nil && e.Metadata["category"] == "system-notification" {
			if strings.Contains(e.Text, "retrying") {
				retryNotifications++
			}
		}
	}
	if retryNotifications == 0 {
		t.Errorf("expected a retry notification, got none")
	}
	if !containsContent(contents, "Recovered after 408") {
		t.Errorf("expected content recovered after initial 408 retry, got %q", contents)
	}
}

func containsContent(contents []string, text string) bool {
	for _, c := range contents {
		if strings.Contains(c, text) {
			return true
		}
	}
	return false
}

func TestAgent_ToolResultTooLarge_TruncatesWithNotice(t *testing.T) {
	p := registerTestProvider("huge-result", []provider.AssistantMessageEvent{
		{Type: provider.EventToolCallEnd, ContentIndex: 0, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolCallID: "call_1",
			ToolName: "huge_tool", ToolArguments: `{}`,
		}},
	})

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{hugeResultTool{
			name:   "huge_tool",
			schema: ToolSchema{Name: "huge_tool", Description: "test"},
			size:   20000, // well above the 2048-char limit when MaxTokens=8192
		}},
		ContextCompression: ContextCompressionConfig{MaxTokens: 8192},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "call huge tool"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var toolResults []OutputEvent
	for _, e := range obs.Events() {
		if e.Type == EventToolResult {
			toolResults = append(toolResults, e)
		}
	}
	if len(toolResults) == 0 {
		t.Fatal("expected a tool result event")
	}
	result := toolResults[0].Text
	if strings.HasPrefix(result, "Error:") {
		t.Errorf("expected truncated result, got error: %q", result)
	}
	if !strings.Contains(result, "[goa-system] Tool result was truncated") {
		t.Errorf("expected truncation notice, got %q", result)
	}
	if !strings.Contains(result, "original 20000 bytes") {
		t.Errorf("expected original size in notice, got %q", result)
	}
	if len(result) <= 100 {
		t.Errorf("expected non-trivial truncated content, got %q", result)
	}
}

// TestAgent_AlwaysModeRetriesUntilSuccess verifies the P8 (DS4) acceptance
// criterion: an always-mode retry policy retries every model-request failure
// — far beyond the finite MaxRetries budget — until the provider succeeds.
func TestAgent_AlwaysModeRetriesUntilSuccess(t *testing.T) {
	// Rate-limit error with a 1ms Retry-After so retries are fast.
	rateLimitErr := (&hooks.ErrorContext{
		StatusCode:   http.StatusTooManyRequests,
		Body:         `{"error":{"message":"slow down","type":"rate_limit"}}`,
		IsRateLimit:  true,
		IsRetryable:  true,
		RetryAfterMs: 1,
	}).ToError()

	// The provider fails 5 times, far more than MaxRetries=1, then succeeds.
	// Always mode must keep retrying past the finite budget.
	steps := make([]scriptedStreamStep, 0, 6)
	for i := 0; i < 5; i++ {
		steps = append(steps, scriptedStreamStep{err: rateLimitErr})
	}
	steps = append(steps, scriptedStreamStep{events: []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered."},
	}})
	p := &scriptedStreamProvider{
		api:   provider.Api(fmt.Sprintf("test-always-%d", testProviderCounter.Add(1))),
		steps: steps,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries: 1, // finite budget would give up after 1 retry
			RetryPolicy: &provider.RetryPolicy{
				Mode:       provider.RetryModeAlways,
				MaxRetries: 1,
				Backoff:    provider.RetryBackoff{InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Jitter: 0},
			},
		},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var contents []string
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == Assistant {
			contents = append(contents, e.Text)
		}
	}
	if !containsContent(contents, "Recovered.") {
		t.Errorf("expected content recovered after always-mode retries, got %q", contents)
	}
	if p.Calls() < 6 {
		t.Errorf("expected >= 6 provider calls (1 initial + 5 retries), got %d", p.Calls())
	}
}

// TestAgent_AlwaysModeStopsOnCancel verifies the always-mode "until cancel"
// semantics: with a permanently failing provider, canceling the parent context
// stops the retry loop promptly instead of retrying forever.
func TestAgent_AlwaysModeStopsOnCancel(t *testing.T) {
	rateLimitErr := (&hooks.ErrorContext{
		StatusCode:   http.StatusTooManyRequests,
		Body:         `{"error":{"message":"slow down","type":"rate_limit"}}`,
		IsRateLimit:  true,
		IsRetryable:  true,
		RetryAfterMs: 1,
	}).ToError()

	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("test-always-cancel-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{err: rateLimitErr}, // repeat-last keeps failing forever
		},
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			RetryPolicy: &provider.RetryPolicy{
				Mode:    provider.RetryModeAlways,
				Backoff: provider.RetryBackoff{InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Jitter: 0},
			},
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, "prompt")
	}()

	// Let the retry loop start, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after cancel, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("always-mode retry loop did not stop on cancel within 5s")
	}
}

// TestAgent_AlwaysModeStopsOnOverflow verifies that always mode does NOT loop
// forever on a context-overflow error: the overflow compress+retry is bounded
// once per turn (handleStreamFailure), and a retry attempt that still
// overflows terminates the loop instead of retrying the impossible request.
func TestAgent_AlwaysModeStopsOnOverflow(t *testing.T) {
	overflowErr := (&hooks.ErrorContext{
		StatusCode:        400,
		Body:              `{"error":{"message":"This model's maximum context length is 4096 tokens. However, you requested 5000 tokens","type":"invalid_request_error"}}`,
		IsContextOverflow: true,
		IsRetryable:       true,
	}).ToError()

	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("test-always-overflow-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{err: overflowErr}, // repeat-last keeps overflowing forever
		},
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			RetryPolicy: &provider.RetryPolicy{
				Mode:    provider.RetryModeAlways,
				Backoff: provider.RetryBackoff{InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Jitter: 0},
			},
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err == nil {
		t.Fatal("expected an error after always-mode overflow, got nil")
	}
	if p.Calls() < 2 {
		t.Errorf("expected at least the initial call + one retry attempt, got %d calls", p.Calls())
	}
}

// TestAgent_RetryEventsVisibleInLog verifies the P8 (DS4) acceptance
// criterion "events visible in goa.log": each retry attempt emits a durable
// "retry scheduled" event (before the backoff wait) and a "retry started"
// event (after the wait, before the request), captured in the agent log ring
// regardless of file logging.
func TestAgent_RetryEventsVisibleInLog(t *testing.T) {
	ResetAgentLogRing()
	defer ResetAgentLogRing()

	rateLimitErr := (&hooks.ErrorContext{
		StatusCode:   http.StatusTooManyRequests,
		Body:         `{"error":{"message":"slow down","type":"rate_limit"}}`,
		IsRateLimit:  true,
		IsRetryable:  true,
		RetryAfterMs: 1,
	}).ToError()

	p := registerFlakyStartProvider(2, rateLimitErr, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered."},
	})
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries: 2,
			RetryPolicy: &provider.RetryPolicy{
				Mode:       provider.RetryModeNormal,
				MaxRetries: 2,
				Backoff:    provider.RetryBackoff{InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Jitter: 0},
				Codes:      []string{provider.RetryCodeRateLimit},
			},
		},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ring := AgentLogSnapshot()
	var scheduled, started int
	for _, line := range ring {
		if strings.Contains(line.Message, "retry scheduled") {
			scheduled++
		}
		if strings.Contains(line.Message, "retry started") {
			started++
		}
	}
	if scheduled == 0 {
		t.Fatal("expected at least one 'retry scheduled' event in the agent log")
	}
	if started == 0 {
		t.Fatal("expected at least one 'retry started' event in the agent log")
	}
	if scheduled != started {
		t.Errorf("scheduled=%d started=%d, want equal (every scheduled retry that starts has a started event)", scheduled, started)
	}
}
