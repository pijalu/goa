// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/netutil"
	"github.com/pijalu/goa/internal/turndown"
)

// WebFetchConfig controls the webfetch tool behavior.
type WebFetchConfig struct {
	Enabled         bool                  `yaml:"enabled"`
	MaxLinesDefault int                   `yaml:"max_lines_default"`
	MaxLinesHard    int                   `yaml:"max_lines_hard"`
	MaxTotalBytes   int                   `yaml:"max_total_bytes"`
	TimeoutSeconds  int                   `yaml:"timeout_seconds"`
	UserAgent       string                `yaml:"user_agent"`
	MaxRedirects    int                   `yaml:"max_redirects"`
	AllowedSchemes  []string              `yaml:"allowed_schemes"`
	BlockedHosts    []string              `yaml:"blocked_hosts"`
	Cache           WebFetchCacheConfig   `yaml:"cache"`
	Summary         WebFetchSummaryConfig `yaml:"summary"`
}

// WebFetchCacheConfig controls the webfetch disk cache.
type WebFetchCacheConfig struct {
	Enabled              bool   `yaml:"enabled"`
	Dir                  string `yaml:"dir"`
	TTLHours             int    `yaml:"ttl_hours"`
	MaxEntries           int    `yaml:"max_entries"`
	MaxBytes             int64  `yaml:"max_bytes"`
	CleanupIntervalHours int    `yaml:"cleanup_interval_hours"`
}

// WebFetchSummaryConfig controls sub-agent summarization for webfetch.
type WebFetchSummaryConfig struct {
	Enabled       bool   `yaml:"enabled"`
	SubAgentRole  string `yaml:"sub_agent_role"`
	MaxInputLines int    `yaml:"max_input_lines"`
	DefaultPrompt string `yaml:"default_prompt"`
}

// Fetcher fetches a URL and returns the response.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*netutil.Response, error)
}

// WebFetchTool fetches URLs and converts HTML to Markdown.
type WebFetchTool struct {
	agentic.BaseTool
	WorktreeMgr *internal.WorktreeManager
	Fetcher     Fetcher
	Cache       *WebFetchCache
	Summarizer  *WebSummarizer
	Config      WebFetchConfig
	HasModel    bool
}

// Schema returns the tool schema. The summarize action is hidden unless enabled
// and a model is configured.
func (t *WebFetchTool) Schema() agentic.ToolSchema {
	actions := []string{"fetch"}
	if t.summaryEnabled() {
		actions = append(actions, "summarize")
	}

	return agentic.ToolSchema{
		Name:        "webfetch",
		Description: "Fetch a URL, convert the page to Markdown, and cache it for the session. Subsequent calls can read line ranges" + t.summaryDescription() + ".",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Absolute URL to fetch (required).",
				},
				"action": map[string]any{
					"type":        "string",
					"description": "Action to perform.",
					"enum":        actions,
					"default":     "fetch",
				},
				"start_line": map[string]any{
					"type":        "integer",
					"description": "First line to return (1-indexed, default: 1). Only for fetch.",
				},
				"end_line": map[string]any{
					"type":        "integer",
					"description": "Last line to return (1-indexed, default: end of file). Only for fetch.",
				},
				"max_lines": map[string]any{
					"type":        "integer",
					"description": "Maximum lines to return (default from config). Only for fetch.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Optional steering prompt for summarize action. Ignored for fetch.",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (t *WebFetchTool) summaryEnabled() bool {
	return t.Config.Summary.Enabled && t.HasModel
}

func (t *WebFetchTool) summaryDescription() string {
	if t.summaryEnabled() {
		return " and optionally summarize via a sub-agent"
	}
	return ""
}

// webfetchParams holds parsed tool input.
type webfetchParams struct {
	URL       string `json:"url"`
	Action    string `json:"action"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	MaxLines  int    `json:"max_lines"`
	Prompt    string `json:"prompt"`
}

// Execute runs the webfetch tool.
func (t *WebFetchTool) Execute(input string) (string, error) {
	p, err := t.parseParams(input)
	if err != nil {
		return "", err
	}

	if err := t.validateURL(p.URL); err != nil {
		return "", err
	}

	switch p.Action {
	case "", "fetch":
		return t.fetch(p)
	case "summarize":
		return t.summarize(p)
	default:
		return "", &internal.ToolError{
			Tool: "webfetch", Type: "unknown_action",
			Detail:   fmt.Sprintf("Unknown action: %s", p.Action),
			HintText: "Use one of: fetch, summarize",
		}
	}
}

func (t *WebFetchTool) parseParams(input string) (webfetchParams, error) {
	var p webfetchParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, &internal.ToolError{
			Tool: "webfetch", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	if p.URL == "" {
		return p, &internal.ToolError{
			Tool: "webfetch", Type: "missing_url",
			Detail:   "No URL provided",
			HintText: "Provide a URL in the 'url' field.",
		}
	}
	if p.StartLine == 0 {
		p.StartLine = 1
	}
	if p.MaxLines == 0 {
		p.MaxLines = t.Config.MaxLinesDefault
	}
	if p.MaxLines > t.Config.MaxLinesHard {
		p.MaxLines = t.Config.MaxLinesHard
	}
	return p, nil
}

func (t *WebFetchTool) validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return &internal.ToolError{
			Tool: "webfetch", Type: "invalid_url",
			Detail:   fmt.Sprintf("Invalid URL: %q", raw),
			HintText: "Provide an absolute URL with a scheme, e.g., https://example.com.",
		}
	}

	allowed := t.Config.AllowedSchemes
	if len(allowed) == 0 {
		allowed = []string{"https", "http"}
	}
	found := false
	for _, s := range allowed {
		if strings.EqualFold(u.Scheme, s) {
			found = true
			break
		}
	}
	if !found {
		return &internal.ToolError{
			Tool: "webfetch", Type: "scheme_not_allowed",
			Detail:   fmt.Sprintf("URL scheme %q is not allowed", u.Scheme),
			HintText: fmt.Sprintf("Allowed schemes: %s", strings.Join(allowed, ", ")),
		}
	}

	host := strings.ToLower(u.Hostname())
	for _, blocked := range t.Config.BlockedHosts {
		if strings.Contains(host, strings.ToLower(blocked)) {
			return &internal.ToolError{
				Tool: "webfetch", Type: "blocked_host",
				Detail:   fmt.Sprintf("Host %q is blocked", u.Host),
				HintText: "Choose a different URL.",
			}
		}
	}
	return nil
}

func (t *WebFetchTool) fetch(p webfetchParams) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout())
	defer cancel()

	if entry, ok, err := t.Cache.Get(ctx, p.URL); err == nil && ok {
		return t.renderEntry(p.URL, entry.Markdown, p, entry.Meta), nil
	} else if err != nil {
		return "", t.cacheError(err)
	}

	resp, err := t.Fetcher.Fetch(ctx, p.URL)
	if err != nil {
		return "", t.fetchError(err)
	}

	if t.Config.MaxTotalBytes > 0 && int64(len(resp.Body)) > int64(t.Config.MaxTotalBytes) {
		return "", &internal.ToolError{
			Tool: "webfetch", Type: "response_too_large",
			Detail:   fmt.Sprintf("Response is %d bytes, exceeds limit of %d", len(resp.Body), t.Config.MaxTotalBytes),
			HintText: "Try a smaller page or increase tools.webfetch.max_total_bytes.",
		}
	}

	cleaned, err := turndown.ExtractMainContent(string(resp.Body))
	if err != nil {
		return "", &internal.ToolError{
			Tool: "webfetch", Type: "extract_error",
			Detail:   fmt.Sprintf("Failed to extract page content: %v", err),
			HintText: "Try again or report the URL.",
		}
	}

	converter := turndown.New()
	markdown, err := converter.Convert(cleaned)
	if err != nil {
		return "", &internal.ToolError{
			Tool: "webfetch", Type: "convert_error",
			Detail:   fmt.Sprintf("Failed to convert HTML to Markdown: %v", err),
			HintText: "Try again or report the URL.",
		}
	}

	meta := WebFetchMeta{
		ContentType: resp.ContentType,
		FetchedAt:   time.Now(),
	}
	if err := t.Cache.Put(ctx, p.URL, []byte(markdown), meta); err != nil {
		return "", t.cacheError(err)
	}

	return t.renderEntry(p.URL, []byte(markdown), p, meta), nil
}

func (t *WebFetchTool) summarize(p webfetchParams) (string, error) {
	if !t.summaryEnabled() {
		return "", &internal.ToolError{
			Tool: "webfetch", Type: "summarize_disabled",
			Detail:   "Summarization is not available",
			HintText: "Enable tools.webfetch.summary.enabled and configure a model.",
		}
	}

	ctx := context.Background()
	entry, ok, err := t.Cache.Get(ctx, p.URL)
	if err != nil {
		return "", t.cacheError(err)
	}
	if !ok {
		return "", &internal.ToolError{
			Tool: "webfetch", Type: "not_cached",
			Detail:   fmt.Sprintf("URL is not cached: %s", p.URL),
			HintText: "Call webfetch with action=fetch first.",
		}
	}

	summary, err := t.Summarizer.Summarize(ctx, p.URL, string(entry.Markdown), p.Prompt)
	if err != nil {
		return "", &internal.ToolError{
			Tool: "webfetch", Type: "summarize_error",
			Detail:   fmt.Sprintf("Summarization failed: %v", err),
			HintText: "Check that the sub-agent is available and try again.",
		}
	}

	return fmt.Sprintf("webfetch summarize %s\n%s\n(end summary — source: %d lines)", p.URL, summary, len(splitLines(string(entry.Markdown)))), nil
}

// timeout returns the fetch deadline derived from WebFetchConfig.TimeoutSeconds.
// Falls back to 30s when unset so a hung peer can never stall the agent turn.
func (t *WebFetchTool) timeout() time.Duration {
	if t.Config.TimeoutSeconds > 0 {
		return time.Duration(t.Config.TimeoutSeconds) * time.Second
	}
	return 30 * time.Second
}

func (t *WebFetchTool) renderEntry(url string, markdown []byte, p webfetchParams, meta WebFetchMeta) string {
	lines := splitLines(string(markdown))
	total := len(lines)
	start, end := clampLineRange(p.StartLine, p.EndLine, total, p.MaxLines)
	selected := lines[start-1 : end]
	if len(selected) > p.MaxLines {
		selected = selected[:p.MaxLines]
		end = start + p.MaxLines - 1
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "webfetch %s:%d:%d\n", url, start, end)
	for _, line := range selected {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	remaining := total - end
	if remaining < 0 {
		remaining = 0
	}
	ttlRemaining := time.Until(meta.FetchedAt.Add(t.Cache.EffectiveTTL(meta)))
	if ttlRemaining < 0 {
		ttlRemaining = 0
	}
	fmt.Fprintf(&buf, "(end — %d lines shown, %d remaining; ttl: %s)\n", len(selected), remaining, ttlRemaining.Round(time.Minute))
	return buf.String()
}

func (t *WebFetchTool) fetchError(err error) *internal.ToolError {
	return &internal.ToolError{
		Tool: "webfetch", Type: "fetch_error",
		Detail:   fmt.Sprintf("Failed to fetch URL: %v", err),
		HintText: "Check the URL, network connectivity, and proxy settings.",
	}
}

func (t *WebFetchTool) cacheError(err error) *internal.ToolError {
	return &internal.ToolError{
		Tool: "webfetch", Type: "cache_error",
		Detail:   fmt.Sprintf("Cache operation failed: %v", err),
		HintText: "Check disk space and permissions for .goa/cache/webfetch.",
	}
}

// ShortDoc returns the short description.
//
//go:embed webfetch.short.md webfetch.long.md
var webfetchDocs embed.FS

func (t *WebFetchTool) ShortDoc() string { return readDoc(webfetchDocs, "webfetch.short.md") }
func (t *WebFetchTool) LongDoc() string  { return readDoc(webfetchDocs, "webfetch.long.md") }
func (t *WebFetchTool) Examples() []string {
	return []string{
		`{"url": "https://example.com"}`,
		`{"url": "https://example.com", "start_line": 1, "end_line": 50}`,
		`{"url": "https://example.com", "action": "summarize"}`,
	}
}

// Access returns no filesystem access for webfetch.
func (t *WebFetchTool) Access(input string) ToolAccess {
	return ToolAccess{}
}

// defaultCacheDir returns the default cache directory under the project.
func defaultCacheDir(projectDir string) string {
	return filepath.Join(projectDir, ".goa", "cache", "webfetch")
}
