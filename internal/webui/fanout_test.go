// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import "testing"

// The fan-out contract. Both rules here exist because the server must not pay
// per client for work that is the same for every client, and must not pay at all
// for a screen nobody is watching.

// TestHubEncodesFrameOncePerFanOut pins the shared-payload design: with several
// browsers attached, one engine frame is encoded once, and every client is
// handed the SAME payload — not a copy, not a re-encode. Encoding per client
// meant N JSON encodings and N allocations of every run for one frame.
func TestHubEncodesFrameOncePerFanOut(t *testing.T) {
	hub := NewHub(4)
	vt := NewVirtualTerminal(20, 3)
	vt.SetSink(hub)

	first, second := &fakeClient{}, &fakeClient{}
	hub.Attach(first, PlaneCells)
	hub.Attach(second, PlaneCells)

	vt.WriteString("hello")

	if len(first.frames) != 1 || len(second.frames) != 1 {
		t.Fatalf("frames delivered: first=%d second=%d, want 1 each",
			len(first.frames), len(second.frames))
	}
	if first.frames[0] != second.frames[0] {
		t.Error("clients received different payloads: the frame was encoded more than once")
	}
	// The payload must actually be the encoded document, or "shared" would just
	// mean the work moved somewhere else.
	if _, err := NewFrameCodec().DecodeFrame(first.frames[0].Data); err != nil {
		t.Errorf("shared payload is not a decodable frame: %v", err)
	}
}

// TestHubPublishWithNoClientsDoesNoWork pins the other half: an unattached hub
// must not encode anything, because there is nobody to send it to.
func TestHubPublishWithNoClientsDoesNoWork(t *testing.T) {
	hub := NewHub(4)
	vt := NewVirtualTerminal(20, 3)
	vt.SetSink(hub)

	// A frame built for an empty hub would still advance the revision; the
	// screen state must be tracked without publishing anything.
	before := vt.Seq()
	vt.WriteString("nobody is watching")
	if vt.Seq() != before {
		t.Errorf("revision advanced to %d with no clients attached, want %d", vt.Seq(), before)
	}
}

// TestPublishWithNoClientsStillServesTheNextAttach is the correctness half of
// the skip: dropping a frame must never cost the next client its screen. It
// attaches AFTER a burst of output and must receive the full current screen,
// including everything written while nobody was attached.
func TestPublishWithNoClientsStillServesTheNextAttach(t *testing.T) {
	hub := NewHub(4)
	vt := NewVirtualTerminal(20, 4)
	vt.SetSink(hub)

	vt.WriteString("\x1b[1;1Halpha")
	vt.WriteString("\x1b[2;1Hbravo")
	vt.WriteString("\x1b[3;1Hcharlie")

	client := &fakeClient{}
	hub.Attach(client, PlaneCells)
	sendFrame(client, vt.FullFrame())

	if len(client.frames) != 1 {
		t.Fatalf("attaching client got %d payloads, want 1", len(client.frames))
	}
	frame := client.lastFrame()
	if !frame.Full {
		t.Error("the first frame of an attach must be full")
	}
	screen := ""
	for _, p := range frame.Patches {
		screen += RunsText(p.Runs)
	}
	for _, want := range []string{"alpha", "bravo", "charlie"} {
		if !contains(screen, want) {
			t.Errorf("screen shipped to the late client is missing %q: %q", want, screen)
		}
	}
}

// TestDiscardChangesKeepsTrackingDirtyRows pins that skipping a frame does not
// break change tracking: after a discarded frame, the next published frame must
// still carry the rows that moved.
func TestDiscardChangesKeepsTrackingDirtyRows(t *testing.T) {
	g := NewCellGrid(20, 3)
	g.Process("\x1b[1;1Halpha")
	g.Patches() // baseline the row

	g.Process("\x1b[1;1HALPHA")
	g.DiscardChanges() // the frame nobody received

	published := g.Patches()
	if len(published) != 1 {
		t.Fatalf("published %d rows after a discard, want 1", len(published))
	}
	if got := RunsText(published[0].Runs); !contains(got, "ALPHA") {
		t.Errorf("published row = %q, want the text written before the discard", got)
	}
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
