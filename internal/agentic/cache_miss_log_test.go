// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

func TestLogCacheMissNotices(t *testing.T) {
	ResetAgentLogRing()
	var buf bytes.Buffer
	logger := NewLoggerWithStdLogger(log.New(&buf, "", 0), Warn)

	logCacheMissNotices(logger, []provider.CacheMissNotice{
		{ReportID: 3, Model: "kimi-k2", PrevCacheRead: 83421, CacheRead: 0},
		{ReportID: 4, Model: "deepseek-v4", PrevCacheRead: 5000, CacheRead: 900},
	})

	out := buf.String()
	if !strings.Contains(out, "cache miss #3") || !strings.Contains(out, "83421 -> 0") ||
		!strings.Contains(out, "cache_miss_requests.json") {
		t.Errorf("log line misses key diagnostics:\n%s", out)
	}
	if got := len(strings.Split(strings.TrimSpace(out), "\n")); got != 2 {
		t.Errorf("expected 2 log lines, got %d:\n%s", got, out)
	}

	// The always-on ring captures the same lines (exported as logs/agent.log).
	ring := AgentLogSnapshot()
	if len(ring) != 2 || !strings.Contains(ring[0].Message, "cache miss #3") {
		t.Errorf("agent log ring did not capture the notices: %+v", ring)
	}
	if ring[0].Level != Warn {
		t.Errorf("ring level = %v, want Warn", ring[0].Level)
	}
}

// TestDrainCacheMissNoticesEndToEnd proves the cross-package wiring: a cache
// bust detected by the provider journal (through the generic runtime) is
// drained by the agent into its logger.
func TestDrainCacheMissNoticesEndToEnd(t *testing.T) {
	provider.ResetCacheForensics()
	defer provider.ResetCacheForensics()
	old := transport.Default()
	defer transport.SetDefault(old)

	call := func(cachedTokens int) {
		transport.SetDefault(&forensicsMockTransport{cached: cachedTokens})
		model := schema.Model{
			ID:       "kimi-k2",
			Api:      schema.ApiOpenAICompletions,
			Provider: schema.ProviderKimiCode,
			BaseURL:  "http://example.com/v1/chat/completions",
		}
		stream, err := provider.Stream(model,
			schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
			schema.StreamOptions{SessionID: "sess-1"})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		_ = stream.Result()
	}
	call(100) // establish
	call(0)   // bust

	// The journal attaches usage (and queues the notice) just after the
	// stream closes; reports are a non-destructive signal to wait on.
	deadline := time.Now().Add(2 * time.Second)
	for len(provider.CacheForensicsReports()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(provider.CacheForensicsReports()) != 1 {
		t.Fatalf("expected 1 forensics report, got %d", len(provider.CacheForensicsReports()))
	}

	var buf bytes.Buffer
	a := NewAgent(Config{Logger: NewLoggerWithStdLogger(log.New(&buf, "", 0), Warn)})
	// The notices were recorded under the raw transport session key (no
	// PromptCacheKey on this direct provider.Stream call), so attribute them
	// to that key — the same thing Agent.stream does for its own streams.
	a.activeCacheKey = "sess-1"
	a.drainCacheMissNotices()

	if !strings.Contains(buf.String(), "cache miss #") ||
		!strings.Contains(buf.String(), "kimi-k2") ||
		!strings.Contains(buf.String(), "100 -> 0") {
		t.Errorf("agent logger did not receive the drained notice:\n%s", buf.String())
	}
	// Notices are drained exactly once.
	buf.Reset()
	a.drainCacheMissNotices()
	if buf.Len() != 0 {
		t.Errorf("second drain must be a no-op, got:\n%s", buf.String())
	}
}

// forensicsMockTransport answers an OpenAI-completions SSE stream whose usage
// chunk reports cached tokens, enough for the journal's miss detection.
type forensicsMockTransport struct {
	cached int
}

func (m *forensicsMockTransport) Do(_ context.Context, _ *transport.TransportRequest) (*transport.TransportResponse, error) {
	body := `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n" +
		fmt.Sprintf(`data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":200,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":%d}}}`, m.cached) + "\n\n" +
		`data: [DONE]` + "\n\n"
	return &transport.TransportResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "text/event-stream"},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}
