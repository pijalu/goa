// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderTrace_EmitsPerFrameJSONL verifies the GOA_DEBUG_RENDER /
// --render-log trace facility: when enabled, the Compositor writes one JSONL
// record per frame carrying the render path, viewport anchors, and the Scene's
// layer layout — the intent-side data needed to diagnose byte-level rendering
// bugs that the filmstrip (Scene-derived) cannot see.
func TestRenderTrace_EmitsPerFrameJSONL(t *testing.T) {
	term := &fakeTerminal{w: 60, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	inp.SetTUI(engine)
	inp.SetFocused(true)
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)

	tracePath := filepath.Join(t.TempDir(), "render.jsonl")
	if err := engine.SetRenderTrace(tracePath); err != nil {
		t.Fatalf("SetRenderTrace: %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Frame 1: initial render (full).
	chat.AddUserMessage("hello")
	engine.RenderNow()
	// Frames 2..N: streaming growth that forces viewport scrolls — these are
	// the frames the trace is meant to make diagnosable.
	for i := 0; i < 40; i++ {
		chat.AddSystemMessage("streaming line growing the conversation")
		engine.RenderNow()
	}
	// Force a flush of any buffered state.
	engine.Stop()
	engine = nil

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

	// Frame numbers must be contiguous starting at 1.
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

	// At least one record must carry the scene layers (intent layout), so the
	// trace can be diffed against the emitted bytes.
	hasLayers := false
	for _, r := range records {
		if len(r.Layers) > 0 {
			hasLayers = true
			break
		}
	}
	if !hasLayers {
		t.Errorf("no record carried Scene layers; the trace cannot show intent layout")
	}

	// At least one diff-path record must show a viewport scroll (Scrolled) with
	// a non-zero Scroll — the streaming-scroll case the trace targets.
	hasScroll := false
	var diffCount int
	for _, r := range records {
		if r.Path == "diff" {
			diffCount++
		}
		if r.Scrolled && r.Scroll > 0 {
			hasScroll = true
		}
	}
	if diffCount == 0 {
		t.Errorf("no diff-path records; expected the streaming growth to take the differential path")
	}
	if !hasScroll {
		t.Errorf("no record recorded a viewport scroll; expected streaming growth to scroll")
	}

	// Every record's Path must be one of the known render paths.
	valid := map[string]bool{"full": true, "resize": true, "diff": true, "cursor": true, "deleted": true}
	for _, r := range records {
		if !valid[r.Path] {
			t.Errorf("record Frame %d has unknown Path %q", r.Frame, r.Path)
		}
	}

	// Sanity: the JSON is compact (no embedded newlines per record) so it is
	// safe to line-parse.
	_ = strings.TrimSpace
}
