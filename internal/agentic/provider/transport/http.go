// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPLogCapacity is the maximum number of HTTP transactions to keep in the log.
const HTTPLogCapacity = 20

// HTTPLogCaptureBytes is the maximum bytes of response body to capture per entry.
const HTTPLogCaptureBytes = 4096

// HTTPLogEntry records a single HTTP request/response transaction.
type HTTPLogEntry struct {
	Timestamp       string            `json:"timestamp"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	StatusCode      int               `json:"statusCode,omitempty"`
	DurationMs      int64             `json:"durationMs"`
	Error           string            `json:"error,omitempty"`
	RequestBody     string            `json:"requestBody,omitempty"`
	ResponseBody    string            `json:"responseBody,omitempty"`
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

// captureBody wraps an io.ReadCloser to peek at the first N bytes without
// consuming the underlying stream.
type captureBody struct {
	r     io.ReadCloser
	buf   *bytes.Buffer
	limit int
	done  bool
}

func (c *captureBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if !c.done && n > 0 {
		remaining := c.limit - c.buf.Len()
		if remaining > 0 {
			if n <= remaining {
				c.buf.Write(p[:n])
			} else {
				c.buf.Write(p[:remaining])
			}
		}
		if c.buf.Len() >= c.limit {
			c.done = true
		}
	}
	return n, err
}

func (c *captureBody) Close() error {
	return c.r.Close()
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

	var cancelTimeout context.CancelFunc
	if req.Timeout > 0 {
		ctx, cancelTimeout = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Millisecond)
		httpReq = httpReq.WithContext(ctx)
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		entry := HTTPLogEntry{
			Timestamp:  start.Format(time.RFC3339Nano),
			Method:     req.Method,
			URL:        req.URL,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      err.Error(),
		}
		log := t.Log
		if log == nil {
			log = GlobalHTTPLog
		}
		log.Add(entry)
		if cancelTimeout != nil {
			cancelTimeout()
		}
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
		DurationMs:      time.Since(start).Milliseconds(),
		ResponseHeaders: headers,
	}

	// Wrap the response body to capture the first N bytes as they are read
	// (lazy capture — does not consume the stream eagerly).
	capBody := &captureBody{
		r:     httpResp.Body,
		buf:   new(bytes.Buffer),
		limit: HTTPLogCaptureBytes,
	}

	respBody := io.ReadCloser(capBody)
	if cancelTimeout != nil {
		respBody = &cancelOnCloseReader{r: respBody, cancel: cancelTimeout}
	}

	// Return a wrapper that finalizes the log entry when the body is closed.
	return &TransportResponse{
		StatusCode: httpResp.StatusCode,
		Headers:    headers,
		Body: &logOnCloseBody{
			ReadCloser: respBody,
			entry:      &entry,
			capture:    capBody,
			log:        t.Log,
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
	once    sync.Once
}

func (b *logOnCloseBody) Close() error {
	b.finalize()
	return b.ReadCloser.Close()
}

func (b *logOnCloseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.finalize()
	}
	return n, err
}

func (b *logOnCloseBody) finalize() {
	b.once.Do(func() {
		if b.capture != nil && b.capture.buf.Len() > 0 {
			b.entry.ResponseBody = strings.TrimSuffix(b.capture.buf.String(), "\n")
			if len(b.entry.ResponseBody) > HTTPLogCaptureBytes {
				b.entry.ResponseBody = b.entry.ResponseBody[:HTTPLogCaptureBytes] + "..."
			}
		}
		l := b.log
		if l == nil {
			l = GlobalHTTPLog
		}
		l.Add(*b.entry)
	})
}

// cancelOnCloseReader wraps an io.ReadCloser and cancels the provided context
// when the body is closed or reaches EOF. This keeps the timeout context alive
// for the duration of the streaming response instead of cancelling it when the
// HTTP round trip returns.
type cancelOnCloseReader struct {
	r      io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelOnCloseReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err != nil {
		c.close()
	}
	return n, err
}

func (c *cancelOnCloseReader) Close() error {
	c.close()
	return c.r.Close()
}

func (c *cancelOnCloseReader) close() {
	c.once.Do(c.cancel)
}

// ReadAll drains and closes a transport response body.
func ReadAll(resp *TransportResponse) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
