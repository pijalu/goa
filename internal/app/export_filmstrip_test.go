// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
)

// TestExportReplay_Filmstrip_NoMascotRedraw replays the session events from the
// diagnostic export and asserts that the TUI filmstrip never redraws the
// mascot/logo header after it has scrolled off the visible viewport.
func TestExportReplay_Filmstrip_NoMascotRedraw(t *testing.T) {
	events := reduceExportEvents(loadExportEvents(t, "/Users/muaddib/dev/goa/.goa/exports/goa-export-20260714-180348.zip"))
	sc := newUIScenario(t, 150, 29)

	for _, ev := range events {
		sc.apply(ev)
	}

	film := sc.filmstrip()
	frames := film.Frames()
	if len(frames) == 0 {
		t.Fatal("expected at least one filmstrip frame")
	}

	const mascotMarker = "goa coding agent"
	var lastMascotFrame int = -1
	for i, f := range frames {
		visible := strings.Join(f.Frame.Visible, "\n")
		if strings.Contains(visible, mascotMarker) {
			lastMascotFrame = i
		}
	}

	for i, f := range frames {
		visible := strings.Join(f.Frame.Visible, "\n")
		if strings.Contains(visible, mascotMarker) {
			continue
		}
		// Once the mascot has disappeared from a frame, it must never reappear
		// in any later frame. A reappearance is a redraw/flash of the header.
		for j := i + 1; j < len(frames); j++ {
			later := strings.Join(frames[j].Frame.Visible, "\n")
			if strings.Contains(later, mascotMarker) {
				t.Errorf("frame %d (%s): mascot/logo header reappears after it scrolled off at frame %d; possible flash/redraw.\nvisible:\n%s",
					j, frames[j].Label, i, later)
			}
		}
		break
	}

	if lastMascotFrame < 0 {
		t.Logf("mascot never visible in the filmstrip (content already taller than viewport)")
	}
}

// TestExportReplay_LoopDetector_NoFalsePositive replays the tool-call sequence
// from the diagnostic export through the production LoopDetector and asserts
// that reading a file after editing it is not treated as a false loop before
// the non-subsequent repeat threshold (10).
func TestExportReplay_LoopDetector_NoFalsePositive(t *testing.T) {
	calls := loadExportToolCalls(t, "/Users/muaddib/dev/goa/.goa/exports/goa-export-20260714-180348.zip")
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())

	readCount := 0
	for i, c := range calls {
		lvl := ld.RecordToolCall(c.name, c.input)
		if lvl == core.LoopInterrupt {
			t.Fatalf("call %d (%s %s) triggered LoopInterrupt; reading a file after an edit should not count as a false loop before 10 repeats",
				i, c.name, c.input)
		}
		if c.name == "read" && c.input == `{"path":"tools/python_renderer.go"}` {
			readCount++
		}
	}

	if readCount < 5 {
		t.Fatalf("expected at least 5 reads of tools/python_renderer.go in the export, got %d", readCount)
	}
	if readCount >= 10 {
		t.Logf("export contains %d reads of the same file; the test intentionally replays the real export below the new 10-repeat threshold", readCount)
	}
}

// reduceExportEvents compresses the raw export into a smaller event sequence
// that is still representative for the TUI regression. Streaming content
// deltas from the same role are merged so the test does not pay for thousands
// of individual renders while the chat viewport and tool widgets still grow
// and shrink exactly as in the real session.
func reduceExportEvents(events []*agentic.OutputEvent) []*agentic.OutputEvent {
	var out []*agentic.OutputEvent
	var pending *agentic.OutputEvent
	for _, ev := range events {
		if isUIInert(ev) {
			continue
		}
		if ev.Type == agentic.EventContent && pending != nil && pending.Type == agentic.EventContent && pending.Role == ev.Role {
			pending.Text += ev.Text
			pending.IsDelta = false
			continue
		}
		if pending != nil {
			out = append(out, pending)
			pending = nil
		}
		if ev.Type == agentic.EventContent {
			pending = ev
			continue
		}
		out = append(out, ev)
	}
	if pending != nil {
		out = append(out, pending)
	}
	return out
}

func isUIInert(ev *agentic.OutputEvent) bool {
	switch ev.Type {
	case agentic.EventProgress, agentic.EventTokenStats, agentic.EventContextStats, agentic.EventToolProgress:
		return true
	default:
		return false
	}
}

// exportEvent represents a single line from session/events.jsonl.
func loadExportEvents(t *testing.T, path string) []*agentic.OutputEvent {
	t.Helper()
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open export zip: %v", err)
	}
	defer z.Close()

	var eventsFile *zip.File
	for _, f := range z.File {
		if f.Name == "session/events.jsonl" {
			eventsFile = f
			break
		}
	}
	if eventsFile == nil {
		t.Fatal("export missing session/events.jsonl")
	}

	rc, err := eventsFile.Open()
	if err != nil {
		t.Fatalf("open events.jsonl: %v", err)
	}
	defer rc.Close()

	var events []*agentic.OutputEvent
	scanner := bufio.NewScanner(rc)
	for scanner.Scan() {
		var ev agentic.OutputEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		events = append(events, &ev)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan events.jsonl: %v", err)
	}
	return events
}

type exportToolCall struct {
	name  string
	input string
}

func loadExportToolCalls(t *testing.T, path string) []exportToolCall {
	t.Helper()
	calls := loadExportEvents(t, path)
	var out []exportToolCall
	for _, ev := range calls {
		if ev.Type == agentic.EventToolCall {
			out = append(out, exportToolCall{name: ev.ToolName, input: ev.ToolInput})
		}
	}
	return out
}
