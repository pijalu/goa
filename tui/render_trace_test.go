// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRenderTrace_EmitsPerFrameJSONL verifies the GOA_DEBUG_RENDER /
// --render-log trace facility: when enabled, the Compositor writes one JSONL
// record per frame carrying the render path, viewport anchors, and the Scene's
// layer layout — the intent-side data needed to diagnose byte-level rendering
// bugs that the filmstrip (Scene-derived) cannot see.
func TestRenderTrace_EmitsPerFrameJSONL(t *testing.T) {
	term := &fakeTerminal{w: 60, h: 10}
	engine, chat := setupRenderTraceTest(t, term)
	tracePath := filepath.Join(t.TempDir(), "render.jsonl")
	if err := engine.SetRenderTrace(tracePath); err != nil {
		t.Fatalf("SetRenderTrace: %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	emitTraceFrames(chat, engine, 40)
	engine.Stop()

	records := readTraceRecords(t, tracePath)
	validateTraceRecords(t, records)
}

// setupRenderTraceTest builds a TUI engine with a chat viewport and editor.
func setupRenderTraceTest(t *testing.T, term *fakeTerminal) (*TUI, *ChatViewport) {
	t.Helper()
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	inp.SetTUI(engine)
	inp.SetFocused(true)
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	return engine, chat
}

// emitTraceFrames renders a full initial frame then a series of streaming
// frames that force viewport scrolls.
func emitTraceFrames(chat *ChatViewport, engine *TUI, n int) {
	chat.AddUserMessage("hello")
	engine.RenderNow()
	for i := 0; i < n; i++ {
		chat.AddSystemMessage("streaming line growing the conversation")
		engine.RenderNow()
	}
}

// readTraceRecords reads and decodes JSONL trace records.
func readTraceRecords(t *testing.T, tracePath string) []frameTrace {
	t.Helper()
	f, err := os.Open(tracePath)
	if err != nil {
		t.Fatalf("trace file not written: %v", err)
	}
	defer f.Close()

	var records []frameTrace
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ft frameTrace
		if err := json.Unmarshal(sc.Bytes(), &ft); err != nil {
			t.Fatalf("decode trace line: %v\nline: %s", err, sc.Text())
		}
		records = append(records, ft)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan trace: %v", err)
	}
	if len(records) < 10 {
		t.Fatalf("expected at least 10 trace records, got %d", len(records))
	}
	return records
}

// validateTraceRecords checks frame numbering, paths, layers, scrolls, and
// known path values.
func validateTraceRecords(t *testing.T, records []frameTrace) {
	t.Helper()
	validateFrameSequence(t, records)
	validatePathValues(t, records)
	validateTraceContent(t, records)
}

// validateFrameSequence checks frame numbers, path presence, and terminal size.
func validateFrameSequence(t *testing.T, records []frameTrace) {
	t.Helper()
	for i, r := range records {
		if r.Frame != int64(i+1) {
			t.Fatalf("record %d has Frame=%d, want %d", i, r.Frame, i+1)
		}
		if r.Path == "" {
			t.Fatalf("record %d (Frame %d) has empty Path", i, r.Frame)
		}
		if r.TermH != 10 || r.TermW != 60 {
			t.Fatalf("record %d term size=%dx%d, want 60x10", i, r.TermW, r.TermH)
		}
	}
}

// validatePathValues ensures every record has a known render path.
func validatePathValues(t *testing.T, records []frameTrace) {
	t.Helper()
	valid := map[string]bool{"full": true, "resize": true, "diff": true, "cursor": true, "deleted": true}
	for _, r := range records {
		if !valid[r.Path] {
			t.Errorf("record Frame %d has unknown Path %q", r.Frame, r.Path)
		}
	}
}

// validateTraceContent checks that the trace includes layers, a scroll record,
// and differential-path records.
func validateTraceContent(t *testing.T, records []frameTrace) {
	t.Helper()
	hasLayers := false
	hasScroll := false
	diffCount := 0
	for _, r := range records {
		if len(r.Layers) > 0 {
			hasLayers = true
		}
		if r.Path == "diff" {
			diffCount++
		}
		if r.Scrolled && r.Scroll > 0 {
			hasScroll = true
		}
	}
	if !hasLayers {
		t.Errorf("no record carried Scene layers; the trace cannot show intent layout")
	}
	if diffCount == 0 {
		t.Errorf("no diff-path records; expected the streaming growth to take the differential path")
	}
	if !hasScroll {
		t.Errorf("no record recorded a viewport scroll; expected streaming growth to scroll")
	}
}
