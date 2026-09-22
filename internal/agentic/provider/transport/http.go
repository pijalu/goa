// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// HTTPLogCapacity is the maximum number of HTTP transactions to keep in the log.
const HTTPLogCapacity = 20

// HTTPLogCaptureBytes is the maximum bytes of response body to capture per entry.
const HTTPLogCaptureBytes = 4096

// HTTPLogTailBytes is the maximum bytes of the response tail (end of stream)
// to capture. SSE finish_reason/usage chunks arrive at the end of the stream,
// so capturing only the head hides them. The tail guarantees they are visible.
const HTTPLogTailBytes = 4096

// HTTPLogRequestBytes is the maximum bytes of the request body tail to capture.
// The tail (not the head) carries the most recent messages — including tool
// results — which is what diagnostics need to confirm a tool result was sent.
const HTTPLogRequestBytes = 2048

// RequestSummary is an agent-friendly, redaction-safe summary of an LLM
// request body. It captures the message-role sequence and tool-call/tool-result
// counts without echoing potentially large or sensitive conversation content,
// so a reader can immediately verify whether a tool result was included in the
// request sent to the model.
type RequestSummary struct {
	Model            string   `json:"model,omitempty"`
	Stream           bool     `json:"stream,omitempty"`
	MessageCount     int      `json:"messageCount"`
	Roles            []string `json:"roles,omitempty"`  // tail of the role sequence (last 16)
	ToolCallBlocks   int      `json:"toolCallBlocks"`   // assistant messages carrying tool_calls
	ToolResultBlocks int      `json:"toolResultBlocks"` // tool-role messages (tool results sent back)
	LastRole         string   `json:"lastRole,omitempty"`
	LastIsToolResult bool     `json:"lastIsToolResult"` // last message is a tool result being sent back
}

// HTTPLogEntry records a single HTTP request/response transaction.
type HTTPLogEntry struct {
	Timestamp       string            `json:"timestamp"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	StatusCode      int               `json:"statusCode,omitempty"`
	DurationMs      int64             `json:"durationMs"`
	Error           string            `json:"error,omitempty"`
	RequestSummary  *RequestSummary   `json:"requestSummary,omitempty"`
	RequestBody     string            `json:"requestBody,omitempty"`  // truncated tail of the request body
	ResponseBody    string            `json:"responseBody,omitempty"` // head of the response stream
	ResponseTail    string            `json:"responseTail,omitempty"` // tail of the response stream (finish_reason/usage)
	FinishReason    string            `json:"finishReason,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
}

// HTTPLog is a thread-safe ring buffer of HTTP transactions.
type HTTPLog struct {
	mu      sync.Mutex
	entries []HTTPLogEntry
	pos     int
	count   int
	cap     int
}

// NewHTTPLog creates a ring buffer for HTTP log entries.
func NewHTTPLog(capacity int) *HTTPLog {
	return &HTTPLog{
		entries: make([]HTTPLogEntry, capacity),
		cap:     capacity,
	}
}

// Add appends an entry to the log, evicting the oldest if at capacity.
func (l *HTTPLog) Add(entry HTTPLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[l.pos] = entry
	l.pos = (l.pos + 1) % l.cap
	if l.count < l.cap {
		l.count++
	}
}

// Snapshot returns all entries in chronological order (oldest first).
func (l *HTTPLog) Snapshot() []HTTPLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.count == 0 {
		return nil
	}
	out := make([]HTTPLogEntry, l.count)
	start := l.pos - l.count
	if start < 0 {
		start += l.cap
	}
	for i := 0; i < l.count; i++ {
		out[i] = l.entries[(start+i)%l.cap]
	}
	return out
}

// Clear empties the log.
func (l *HTTPLog) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pos = 0
	l.count = 0
}

// GlobalHTTPLog is the global HTTP transaction log used by the default transport.
var GlobalHTTPLog = NewHTTPLog(HTTPLogCapacity)

// captureBody wraps an io.ReadCloser to peek at the head (first N bytes) and
// tail (last M bytes) of a streaming response without consuming it. The tail
// is essential for SSE streams where finish_reason/usage arrive at EOF.
type captureBody struct {
	r         io.ReadCloser
	head      *bytes.Buffer
	tail      *rollingTail
	headLimit int
	headDone  bool
}

func (c *captureBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		if !c.headDone {
			remaining := c.headLimit - c.head.Len()
			if remaining > 0 {
				if n <= remaining {
					c.head.Write(p[:n])
				} else {
					c.head.Write(p[:remaining])
				}
			}
			if c.head.Len() >= c.headLimit {
				c.headDone = true
			}
		}
		c.tail.write(p[:n])
	}
	return n, err
}

func (c *captureBody) Close() error {
	return c.r.Close()
}

// rollingTail keeps the last N bytes written to it, bounding memory to 2*N.
type rollingTail struct {
	buf bytes.Buffer
	cap int
}

func (t *rollingTail) write(p []byte) {
	t.buf.Write(p)
	if t.buf.Len() > t.cap*2 {
		data := t.buf.Bytes()
		keep := make([]byte, t.cap)
		copy(keep, data[len(data)-t.cap:])
		t.buf.Reset()
		t.buf.Write(keep)
	}
}

func (t *rollingTail) bytes() []byte {
	data := t.buf.Bytes()
	if len(data) > t.cap {
		return data[len(data)-t.cap:]
	}
	return data
}

// analyzeRequestBody parses an LLM request body once and returns a
// redaction-safe summary plus the raw JSON of the conversation region:
// raw["messages"] (OpenAI chat shape) or raw["input"] (codex /responses
// shape). Unknown shapes yield a zero summary and a nil region. Decoding into
// json.RawMessage keeps nested content unparsed until item classification.
func analyzeRequestBody(body []byte) (RequestSummary, json.RawMessage) {
	s := RequestSummary{}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return s, nil
	}
	s.Model = rawJSONString(raw["model"])
	s.Stream = rawJSONBool(raw["stream"])
	region := raw["messages"]
	if len(region) == 0 || string(region) == "null" {
		region = raw["input"]
	}
	s.summarizeItems(region)
	return s, region
}

// rawJSONString decodes a single string scalar; missing or malformed keys
// yield the zero value.
func rawJSONString(raw json.RawMessage) string {
	var out string
	_ = json.Unmarshal(raw, &out)
	return out
}

// rawJSONBool decodes a single bool scalar; missing or malformed keys yield
// the zero value.
func rawJSONBool(raw json.RawMessage) bool {
	var out bool
	_ = json.Unmarshal(raw, &out)
	return out
}

// summarizeItems fills the message counters and role tail from a conversation
// region (array of chat messages or codex input items).
func (s *RequestSummary) summarizeItems(region json.RawMessage) {
	var items []json.RawMessage
	if err := json.Unmarshal(region, &items); err != nil {
		return
	}
	s.MessageCount = len(items)
	roles := make([]string, 0, len(items))
	for _, item := range items {
		var m map[string]any
		if err := json.Unmarshal(item, &m); err != nil {
			continue
		}
		role, toolCall, toolResult := classifyRequestItem(m)
		roles = append(roles, role)
		if toolResult {
			s.ToolResultBlocks++
		}
		if toolCall {
			s.ToolCallBlocks++
		}
	}
	s.finishRoles(roles)
}

// classifyRequestItem maps a chat-style message or codex /responses input item
// to the role label and tool-call/tool-result counters used by the anomaly
// detectors. Codex items carry no role: function_call is model output and
// function_call_output is the executed tool result.
func classifyRequestItem(m map[string]any) (role string, toolCall, toolResult bool) {
	typ, _ := m["type"].(string)
	switch typ {
	case "function_call":
		return "assistant", true, false
	case "function_call_output":
		return "tool", false, true
	}
	role, _ = m["role"].(string)
	if role == "" {
		if typ != "" {
			return typ, false, false // e.g. codex "reasoning" items
		}
		return "unknown", false, false
	}
	if role == "tool" {
		return role, false, true
	}
	if role == "assistant" {
		if tcs, ok := m["tool_calls"].([]any); ok && len(tcs) > 0 {
			return role, true, false
		}
	}
	return role, false, false
}

// finishRoles records the tail of the role sequence on the summary.
func (s *RequestSummary) finishRoles(roles []string) {
	if len(roles) == 0 {
		return
	}
	s.LastRole = roles[len(roles)-1]
	s.LastIsToolResult = s.LastRole == "tool"
	start := 0
	if len(roles) > 16 {
		start = len(roles) - 16
	}
	s.Roles = roles[start:]
}

// summarizeRequestBody parses an LLM request body into a redaction-safe
// summary. Unknown body shapes yield a partial summary.
func summarizeRequestBody(body []byte) RequestSummary {
	s, _ := analyzeRequestBody(body)
	return s
}

// truncateTail returns the last n bytes of s, with an ellipsis marker when
// truncated. Used to keep the request-body tail small for diagnostics.
func truncateTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// extractFinishReason scans captured SSE text for the last finish_reason value.
// It is intentionally tolerant: it operates on raw captured bytes rather than
// a full SSE parse, since the transport must not depend on provider semantics.
var finishReasonRe = regexp.MustCompile(`"finish_reason"\s*:\s*"([^"]+)"`)

func extractFinishReason(captured string) string {
	matches := finishReasonRe.FindAllStringSubmatch(captured, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

// HTTPTransport executes HTTP requests.
type HTTPTransport struct {
	Client *http.Client
	Log    *HTTPLog // if nil, uses GlobalHTTPLog
}

// Do executes an HTTP request and records the transaction in the HTTP log.
func (t *HTTPTransport) Do(ctx context.Context, req *TransportRequest) (*TransportResponse, error) {
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	start := time.Now()

	body := bytes.NewReader(req.Body)
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	// Disable automatic gzip acceptance for streaming SSE; some local
	// servers (e.g. LM Studio) return an empty body when Accept-Encoding:
	// gzip is present on SSE endpoints. Setting "identity" prevents both
	// the Transport and the server from using compression.
	httpReq.Header.Set("Accept-Encoding", "identity")

	// The timeout bounds only the connection phase (up to the first response
	// header). Wrapping the request context with a deadline would also kill
	// in-flight body reads, aborting long-but-healthy streams from slow local
	// models; ResponseHeaderTimeout leaves body reads to the idle guard.
	client = clientWithHeaderTimeout(client, req.Timeout)

	// One parse serves both outcomes: summary + conversation-region capture.
	reqSummary, reqBodyCapture := requestAnalysis(req.Body)

	httpResp, err := client.Do(httpReq)
	if err != nil {
		entry := HTTPLogEntry{
			Timestamp:      start.Format(time.RFC3339Nano),
			Method:         req.Method,
			URL:            req.URL,
			DurationMs:     time.Since(start).Milliseconds(),
			Error:          err.Error(),
			RequestSummary: reqSummary,
			RequestBody:    reqBodyCapture,
		}
		log := t.Log
		if log == nil {
			log = GlobalHTTPLog
		}
		log.Add(entry)
		return nil, err
	}

	headers := make(map[string]string, len(httpResp.Header))
	for k, v := range httpResp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	entry := HTTPLogEntry{
		Timestamp:       start.Format(time.RFC3339Nano),
		Method:          req.Method,
		URL:             req.URL,
		StatusCode:      httpResp.StatusCode,
		ResponseHeaders: headers,
		RequestSummary:  reqSummary,
		RequestBody:     reqBodyCapture,
	}

	// Wrap the response body to capture the head and tail as bytes are read
	// (lazy capture — does not consume the stream eagerly). The tail preserves
	// finish_reason/usage which arrive at the end of the SSE stream.
	capBody := &captureBody{
		r:         httpResp.Body,
		head:      new(bytes.Buffer),
		tail:      &rollingTail{cap: HTTPLogTailBytes},
		headLimit: HTTPLogCaptureBytes,
	}

	// Return a wrapper that finalizes the log entry when the body is closed.
	// DurationMs and any mid-stream read error are recorded at finalize time
	// (not here) so a stalled/failed body is visible in the log rather than
	// looking like a fast, clean completion.
	return &TransportResponse{
		StatusCode: httpResp.StatusCode,
		Headers:    headers,
		Body: &logOnCloseBody{
			ReadCloser: capBody,
			entry:      &entry,
			capture:    capBody,
			log:        t.Log,
			start:      start,
		},
	}, nil
}

// logOnCloseBody wraps the response body and finalizes the HTTP log entry
// when the body is fully consumed (reaches EOF) or closed.
type logOnCloseBody struct {
	io.ReadCloser
	entry   *HTTPLogEntry
	capture *captureBody
	log     *HTTPLog
	start   time.Time
	once    sync.Once
}

func (b *logOnCloseBody) Close() error {
	b.finalize(nil)
	return b.ReadCloser.Close()
}

func (b *logOnCloseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.finalize(err)
	}
	return n, err
}

func (b *logOnCloseBody) finalize(readErr error) {
	b.once.Do(func() {
		b.applyCapture()
		// DurationMs is measured when the stream terminates (EOF, error, or
		// close), not at header-arrival time, so a long-lived or stalled
		// stream reports its true wall-clock duration.
		if !b.start.IsZero() {
			b.entry.DurationMs = time.Since(b.start).Milliseconds()
		}
		// Record a mid-stream read failure so a dropped/stalled connection is
		// visible in the log instead of looking like a clean completion. A
		// clean EOF (io.EOF) is the normal terminator, not an error.
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			b.entry.Error = readErr.Error()
		}
		l := b.log
		if l == nil {
			l = GlobalHTTPLog
		}
		l.Add(*b.entry)
	})
}

// applyCapture transfers the buffered head/tail (and extracted finish_reason)
// into the log entry. Called exactly once when the stream is fully consumed.
func (b *logOnCloseBody) applyCapture() {
	if b.capture == nil {
		return
	}
	if b.capture.head.Len() > 0 {
		head := strings.TrimSuffix(b.capture.head.String(), "\n")
		if len(head) > HTTPLogCaptureBytes {
			head = head[:HTTPLogCaptureBytes] + "..."
		}
		b.entry.ResponseBody = head
	}
	if b.capture.tail == nil {
		return
	}
	tail := strings.TrimSuffix(string(b.capture.tail.bytes()), "\n")
	if len(tail) > HTTPLogTailBytes {
		tail = "..." + tail[len(tail)-HTTPLogTailBytes:]
	}
	b.entry.ResponseTail = tail
	// finish_reason lives at the end of the stream; scan the tail.
	if fr := extractFinishReason(tail); fr != "" {
		b.entry.FinishReason = fr
	}
}

// requestAnalysis computes the summary pointer (nil when the shape is not
// understood, preserving the trace contract) and the captured request-body
// diagnostics text in one parse.
func requestAnalysis(body []byte) (*RequestSummary, string) {
	s, region := analyzeRequestBody(body)
	var ps *RequestSummary
	if len(body) > 0 && s.MessageCount > 0 {
		ps = &s
	}
	return ps, requestBodyCapture(body, region)
}

// requestBodyCapture returns the tail of the conversation region so recent
// tool results stay visible regardless of where the provider places tool
// schemas in the body (codex puts them last, crowding out the raw tail).
// Unknown shapes fall back to the raw body tail.
func requestBodyCapture(body []byte, region json.RawMessage) string {
	if len(region) > 0 {
		return truncateTail(string(region), HTTPLogRequestBytes)
	}
	return truncateTail(string(body), HTTPLogRequestBytes)
}

// clientWithHeaderTimeout returns an *http.Client whose round trip fails when
// the response headers do not arrive within timeoutMs. The timeout covers the
// full connection phase — dial, TLS handshake, request send, and the server's
// time to first header — which is where an unresponsive provider hangs. Body
// reads are intentionally left unbounded so slow-but-streaming local models
// are never killed by a wall clock; a stalled body is caught by the
// idle-timeout reader in the provider runtime instead.
//
// A timeout of zero (or a client whose Transport is not an *http.Transport,
// e.g. a custom test RoundTripper) returns the client unchanged.
func clientWithHeaderTimeout(client *http.Client, timeoutMs int64) *http.Client {
	if timeoutMs <= 0 {
		return client
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	var tr *http.Transport
	switch base := client.Transport.(type) {
	case *http.Transport:
		tr = base.Clone()
	case nil:
		if d, ok := http.DefaultTransport.(*http.Transport); ok {
			tr = d.Clone()
		}
	}
	if tr == nil {
		return client
	}
	tr.ResponseHeaderTimeout = timeout
	clone := *client
	clone.Transport = tr
	// http.Client.Timeout would re-introduce a whole-request deadline (it
	// covers body reads); it must stay zero for the connection-phase semantic.
	clone.Timeout = 0
	return &clone
}

// ReadAll drains and closes a transport response body.
func ReadAll(resp *TransportResponse) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
