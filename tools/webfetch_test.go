// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/netutil"
)

type fetchFunc func(ctx context.Context, url string) (*netutil.Response, error)

func (f fetchFunc) Fetch(ctx context.Context, url string) (*netutil.Response, error) {
	return f(ctx, url)
}

func TestWebFetchFetchAndCache(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	html := "<html><body><h1>Hello</h1><p>World</p></body></html>"
	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		return &netutil.Response{
			URL:         url,
			StatusCode:  http.StatusOK,
			ContentType: "text/html",
			Body:        []byte(html),
		}, nil
	})

	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			MaxLinesDefault: 250,
			MaxLinesHard:    4096,
			MaxTotalBytes:   20 * 1024 * 1024,
			AllowedSchemes:  []string{"https", "http"},
		},
	}

	out, err := tool.Execute(`{"url":"https://example.com"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "# Hello") {
		t.Errorf("missing heading in output: %q", out)
	}
	if !strings.Contains(out, "World") {
		t.Errorf("missing paragraph in output: %q", out)
	}

	// Second call should use cache (fetcher not called again).
	calls := 0
	fetcher2 := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		calls++
		return fetcher.Fetch(ctx, url)
	})
	tool.Fetcher = fetcher2
	out2, err := tool.Execute(`{"url":"https://example.com"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if calls != 0 {
		t.Errorf("expected cache hit, fetcher called %d times", calls)
	}
	if out2 != out {
		t.Errorf("cache output differed:\nfirst: %q\nsecond: %q", out, out2)
	}
}

func TestWebFetchLineRange(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	html := "<html><body><p>" + strings.Join([]string{"a", "b", "c", "d", "e"}, "<br>") + "</p></body></html>"
	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		return &netutil.Response{
			URL:         url,
			StatusCode:  http.StatusOK,
			ContentType: "text/html",
			Body:        []byte(html),
		}, nil
	})

	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			MaxLinesDefault: 250,
			MaxLinesHard:    4096,
			MaxTotalBytes:   20 * 1024 * 1024,
			AllowedSchemes:  []string{"https", "http"},
		},
	}

	out, err := tool.Execute(`{"url":"https://example.com","start_line":2,"end_line":4}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "https://example.com:2:4") {
		t.Errorf("expected line range header, got %q", out)
	}
}

func TestWebFetchInvalidURL(t *testing.T) {
	tool := &WebFetchTool{
		Config: WebFetchConfig{
			AllowedSchemes: []string{"https"},
		},
	}
	_, err := tool.Execute(`{"url":"ftp://example.com"}`)
	if err == nil {
		t.Fatal("expected error for invalid scheme")
	}
}

func TestWebFetchDefaultMaxLines(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	// Generate HTML that converts to 1500 lines of Markdown.
	paragraphs := make([]string, 1500)
	for i := range paragraphs {
		paragraphs[i] = "<p>line</p>"
	}
	html := "<html><body>" + strings.Join(paragraphs, "") + "</body></html>"
	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		return &netutil.Response{
			URL:         url,
			StatusCode:  http.StatusOK,
			ContentType: "text/html",
			Body:        []byte(html),
		}, nil
	})

	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			MaxLinesDefault: 250,
			MaxLinesHard:    4096,
			MaxTotalBytes:   20 * 1024 * 1024,
			AllowedSchemes:  []string{"https", "http"},
		},
	}

	out, err := tool.Execute(`{"url":"https://example.com"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "250 lines shown") {
		t.Errorf("expected 250 lines shown by default, got %q", out)
	}
	if !strings.Contains(out, "remaining") {
		t.Errorf("expected remaining count in output, got %q", out)
	}
}

func TestWebFetchSummarizeDisabled(t *testing.T) {
	tool := &WebFetchTool{
		Config: WebFetchConfig{
			Summary: WebFetchSummaryConfig{Enabled: false},
		},
		HasModel: false,
	}
	schema := tool.Schema()
	actions := schema.Schema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	for _, a := range actions {
		if a == "summarize" {
			t.Fatal("summarize should be hidden when disabled")
		}
	}
}

func TestWebFetchSummarizeHiddenWithoutModel(t *testing.T) {
	tool := &WebFetchTool{
		Config: WebFetchConfig{
			Summary: WebFetchSummaryConfig{Enabled: true},
		},
		HasModel: false,
	}
	schema := tool.Schema()
	actions := schema.Schema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	for _, a := range actions {
		if a == "summarize" {
			t.Fatal("summarize should be hidden when no model configured")
		}
	}
}

func TestWebFetchSummarizeVisible(t *testing.T) {
	tool := &WebFetchTool{
		Config: WebFetchConfig{
			Summary: WebFetchSummaryConfig{Enabled: true},
		},
		HasModel: true,
	}
	schema := tool.Schema()
	actions := schema.Schema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	found := false
	for _, a := range actions {
		if a == "summarize" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("summarize should be visible when enabled and model configured")
	}
}

func TestWebFetchExtractsMainContent(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	html := `<html><head><style>.x{color:red}</style></head>
<body>
<header><nav><a href="/">home</a></nav></header>
<main><h1>Real title</h1><p>Real paragraph</p></main>
<aside><p>ad</p></aside>
<footer><p>copyright</p></footer>
<script>alert('x')</script>
</body></html>`

	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		return &netutil.Response{
			URL:         url,
			StatusCode:  http.StatusOK,
			ContentType: "text/html",
			Body:        []byte(html),
		}, nil
	})

	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			MaxLinesDefault: 250,
			MaxLinesHard:    4096,
			MaxTotalBytes:   20 * 1024 * 1024,
			AllowedSchemes:  []string{"https", "http"},
		},
	}

	out, err := tool.Execute(`{"url":"https://example.com"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "Real title") {
		t.Errorf("main content missing: %q", out)
	}
	if strings.Contains(out, "home") {
		t.Errorf("navigation leaked into output: %q", out)
	}
	if strings.Contains(out, "ad") {
		t.Errorf("aside ad leaked into output: %q", out)
	}
	if strings.Contains(out, "copyright") {
		t.Errorf("footer leaked into output: %q", out)
	}
	if strings.Contains(out, "alert") {
		t.Errorf("script leaked into output: %q", out)
	}
}

func TestWebFetchBlockedHost(t *testing.T) {
	tool := &WebFetchTool{
		Config: WebFetchConfig{
			BlockedHosts:   []string{"blocked.example.com"},
			AllowedSchemes: []string{"https"},
		},
	}
	_, err := tool.Execute(`{"url":"https://blocked.example.com/page"}`)
	if err == nil {
		t.Fatal("expected error for blocked host")
	}
}

func TestWebFetchMaxTotalBytes(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		return &netutil.Response{
			URL:         url,
			StatusCode:  http.StatusOK,
			ContentType: "text/html",
			Body:        []byte("x"),
		}, nil
	})

	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			MaxTotalBytes:  0,
			AllowedSchemes: []string{"https"},
		},
	}

	out, err := tool.Execute(`{"url":"https://example.com"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "x") {
		t.Errorf("missing content in output: %q", out)
	}
}

func TestWebFetchSummarizeAction(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	if err := cache.Put(ctx, "https://example.com", []byte("line1\nline2\nline3"), WebFetchMeta{}); err != nil {
		t.Fatalf("Put error: %v", err)
	}

	tool := &WebFetchTool{
		Cache:    cache,
		Config:   WebFetchConfig{Summary: WebFetchSummaryConfig{Enabled: true}},
		HasModel: true,
		Summarizer: &WebSummarizer{
			Pool: &fakeAgentPool{runner: &fakeAgentRunner{summary: "short summary"}},
		},
	}

	out, err := tool.Execute(`{"url":"https://example.com","action":"summarize"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "short summary") {
		t.Errorf("missing summary in output: %q", out)
	}
}

type fakeAgentPool struct {
	runner AgentRunner
}

func (f *fakeAgentPool) GetOrCreate(role string) (AgentRunner, error) {
	return f.runner, nil
}

type fakeAgentRunner struct {
	summary string
}

func (f *fakeAgentRunner) Run(ctx context.Context, input string) error { return nil }
func (f *fakeAgentRunner) GetHistory() []agentic.Message {
	return []agentic.Message{{Role: agentic.Assistant, Content: f.summary}}
}

func TestWebFetchSummarizeNotCached(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	tool := &WebFetchTool{
		Cache:    cache,
		Config:   WebFetchConfig{Summary: WebFetchSummaryConfig{Enabled: true}},
		HasModel: true,
	}

	_, err := tool.Execute(`{"url":"https://example.com","action":"summarize"}`)
	if err == nil {
		t.Fatal("expected error for uncached summarize")
	}
}

func TestWebFetchSummarizerBuildPrompt(t *testing.T) {
	s := &WebSummarizer{
		DefaultPrompt: "Summarize:",
		MaxInputLines: 2,
	}
	got := s.buildPrompt("a\nb\nc", "focus on X")
	if !strings.Contains(got, "Summarize:") {
		t.Errorf("missing default prompt: %q", got)
	}
	if !strings.Contains(got, "focus on X") {
		t.Errorf("missing user prompt: %q", got)
	}
	if !strings.Contains(got, "a\nb\n...") {
		t.Errorf("missing truncated content: %q", got)
	}
}

func TestWebFetchSummarizerNoPool(t *testing.T) {
	s := &WebSummarizer{}
	_, err := s.Summarize(context.Background(), "", "content", "")
	if err == nil {
		t.Fatal("expected error when pool is nil")
	}
}

func TestWebFetchDocs(t *testing.T) {
	tool := &WebFetchTool{}
	if tool.ShortDoc() == "" {
		t.Error("ShortDoc should not be empty")
	}
	if tool.LongDoc() == "" {
		t.Error("LongDoc should not be empty")
	}
	if len(tool.Examples()) == 0 {
		t.Error("Examples should not be empty")
	}
}

func TestWebFetchAccessAndRetryable(t *testing.T) {
	tool := &WebFetchTool{}
	if tool.Access(`{"url":"https://example.com"}`).ReadPaths != nil {
		t.Error("Access should report no filesystem access")
	}
	if tool.IsRetryable(nil) {
		t.Error("webfetch errors should not be retryable by default")
	}
}

func TestWebFetchInvalidInput(t *testing.T) {
	tool := &WebFetchTool{}
	_, err := tool.Execute("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWebFetchUnknownAction(t *testing.T) {
	tool := &WebFetchTool{
		Config: WebFetchConfig{AllowedSchemes: []string{"https"}},
	}
	_, err := tool.Execute(`{"url":"https://example.com","action":"delete"}`)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestWebFetchFetchError(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		return nil, fmt.Errorf("network down")
	})

	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			AllowedSchemes: []string{"https"},
		},
	}

	_, err := tool.Execute(`{"url":"https://example.com"}`)
	if err == nil {
		t.Fatal("expected error for fetch failure")
	}
}

func TestWebFetchCacheError(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		return &netutil.Response{
			URL:         url,
			StatusCode:  http.StatusOK,
			ContentType: "text/html",
			Body:        []byte("hi"),
		}, nil
	})

	// Make the cache directory read-only to force a Put error.
	_ = os.Chmod(cache.Dir, 0555)
	defer os.Chmod(cache.Dir, 0755)

	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			AllowedSchemes: []string{"https"},
		},
	}

	_, err := tool.Execute(`{"url":"https://example.com"}`)
	if err == nil {
		t.Fatal("expected error for cache failure")
	}
}

func TestWebFetchDefaultCacheDir(t *testing.T) {
	got := defaultCacheDir("/project")
	want := filepath.Join("/project", ".goa", "cache", "webfetch")
	if got != want {
		t.Errorf("defaultCacheDir = %q, want %q", got, want)
	}
}

func TestWebFetchServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body><p>server response</p></body></html>"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	tool := &WebFetchTool{
		Fetcher: &netutil.Fetcher{UserAgent: "test"},
		Cache:   cache,
		Config: WebFetchConfig{
			MaxLinesDefault: 250,
			MaxLinesHard:    4096,
			MaxTotalBytes:   20 * 1024 * 1024,
			AllowedSchemes:  []string{"http"},
		},
	}

	out, err := tool.Execute(`{"url":"` + ts.URL + `"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "server response") {
		t.Errorf("missing content in output: %q", out)
	}
}

// --- STUB-05: webfetch must enforce TimeoutSeconds via context. ---

func TestWebFetch_TimeoutCancelsSlowFetcher(t *testing.T) {
	cache := NewWebFetchCache(
		filepath.Join(t.TempDir(), ".goa", "cache", "webfetch"),
		1*time.Hour, 10, 1024*1024, 1*time.Hour,
		&fakeSessionProvider{id: "s"},
	)
	defer cache.Close()

	// Stub fetcher that blocks until the context is cancelled.
	slow := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	tool := &WebFetchTool{
		Fetcher: slow,
		Cache:   cache,
		Config: WebFetchConfig{
			TimeoutSeconds: 1,
			AllowedSchemes: []string{"https"},
		},
	}

	start := time.Now()
	_, err := tool.Execute(`{"url":"https://example.com"}`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from slow fetcher")
	}
	// Must return within timeout + slack, not hang until the agent-loop timeout.
	if elapsed > 3*time.Second {
		t.Errorf("fetch did not honour TimeoutSeconds: elapsed=%v", elapsed)
	}
}

// --- BUG-11: TTL=0 must display the 24h default, not "0s". ---

func TestWebFetch_RenderEntry_TTLZeroShows24hDefault(t *testing.T) {
	dir := t.TempDir()
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		0, // TTL == 0 → should default to 24h in display
		10, 1024*1024, 1*time.Hour,
		&fakeSessionProvider{id: "s"},
	)
	defer cache.Close()

	tool := &WebFetchTool{
		Fetcher: fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
			return &netutil.Response{URL: url, StatusCode: 200, ContentType: "text/html",
				Body: []byte("<html><body>hello</body></html>")}, nil
		}),
		Cache: cache,
		Config: WebFetchConfig{
			MaxLinesDefault: 250,
			MaxLinesHard:    4096,
			AllowedSchemes:  []string{"https"},
		},
	}

	out, err := tool.Execute(`{"url":"https://example.com"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "ttl: 0s") {
		t.Errorf("TTL=0 should show 24h default, got 0s: %q", out)
	}
	if !strings.Contains(out, "24h") {
		t.Errorf("expected footer to show 24h default ttl: %q", out)
	}
}

// TestWebFetchExecuteContext_RespectsCancellation verifies that the caller's
// context propagates into the HTTP fetch. With a pre-cancelled context the
// fetch (which blocks on ctx.Done()) must return promptly instead of waiting
// for the 30s timeout — the regression was fetch using context.Background().
func TestWebFetchExecuteContext_RespectsCancellation(t *testing.T) {
	dir := t.TempDir()
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour, 10, 1024*1024, 1*time.Hour,
		&fakeSessionProvider{id: "session-cancel"},
	)
	defer cache.Close()

	fetcher := fetchFunc(func(ctx context.Context, url string) (*netutil.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	tool := &WebFetchTool{
		Fetcher: fetcher,
		Cache:   cache,
		Config: WebFetchConfig{
			MaxLinesDefault: 10,
			MaxLinesHard:    100,
			MaxTotalBytes:   1 << 20,
			AllowedSchemes:  []string{"https", "http"},
			TimeoutSeconds:  30,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := tool.ExecuteContext(ctx, `{"url":"https://example.com"}`)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when the caller context is cancelled")
		}
	case <-time.After(2 * time.Second):
		// With context.Background() this would hang until the 30s timeout.
		t.Fatal("ExecuteContext did not return promptly on cancelled context; ctx not propagated")
	}
}
