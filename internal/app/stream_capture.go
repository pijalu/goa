// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// streamCapture writes the exact agent stream flow as JSONL (one record per
// agent output event, in arrival order) so a reported TUI repetition can be
// replayed/diffed to decide between model-origin and TUI-origin (bugs.md
// "TUI shows unexpected repetition" entry). Enabled with the
// --capture-stream <path> command-line flag (config key logging.capture_stream).
type streamCapture struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// streamCaptureRecord is one captured agent output event. Text carries the
// delta as received (IsDelta distinguishes deltas from full blocks).
type streamCaptureRecord struct {
	TS         int64             `json:"ts_ms"`
	Type       string            `json:"type"`
	State      string            `json:"state,omitempty"`
	Role       string            `json:"role,omitempty"`
	IsDelta    bool              `json:"delta,omitempty"`
	Text       string            `json:"text,omitempty"`
	ToolName   string            `json:"tool,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	ToolInput  string            `json:"tool_input,omitempty"`
	ToolResult string            `json:"tool_result,omitempty"`
	Metadata   map[string]string `json:"meta,omitempty"`
}

// newStreamCapture opens path for writing (created/truncated) and returns
// the capture; writes are unbuffered so a crash loses nothing.
func newStreamCapture(path string) (*streamCapture, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("capture stream: %w", err)
	}
	return &streamCapture{f: f, enc: json.NewEncoder(f)}, nil
}

// record appends one event to the capture file.
func (c *streamCapture) record(ev *agentic.OutputEvent) {
	rec := streamCaptureRecord{
		TS:         time.Now().UnixMilli(),
		Type:       string(ev.Type),
		State:      ev.State.String(),
		Role:       string(ev.Role),
		IsDelta:    ev.IsDelta,
		Text:       ev.Text,
		ToolName:   ev.ToolName,
		ToolCallID: ev.ToolCallID,
		ToolInput:  ev.ToolInput,
		ToolResult: ev.ToolResult,
		Metadata:   ev.Metadata,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.enc.Encode(&rec) // best-effort: capture must never break the session
}

// close closes the capture file.
func (c *streamCapture) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.f.Close()
}
