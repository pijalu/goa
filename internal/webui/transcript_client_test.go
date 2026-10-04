// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strconv"
	"strings"
	"testing"
)

// The transcript bound. The transcript is the one part of the page that grows
// with the session, so it is capped exactly like a terminal's scrollback buffer:
// past the cap the oldest rows fall off. These tests pin the properties that
// make that safe — the DOM cannot grow with the session, and a reader who is
// scrolled up does not have the text jump under them when rows fall off.

// transcriptCap mirrors app.js's bound so a test fails loudly if the script's
// bound is changed without the tests following.
const (
	transcriptCap   = 2000
	transcriptBatch = 100
)

// scrollbackBatch encodes a transcript batch of n rows, oldest first.
func scrollbackBatch(t *testing.T, n int, prefix string) string {
	t.Helper()
	return scrollbackDoc(t, n, prefix, false)
}

// replaceScrollbackBatch encodes a batch that REPLACES the transcript the client
// holds: the server wiped its scrollback and re-emitted the whole history (the
// compositor does that at a new width), so the rows are the transcript as it
// stands, not an addition to it.
func replaceScrollbackBatch(t *testing.T, n int, prefix string) string {
	t.Helper()
	return scrollbackDoc(t, n, prefix, true)
}

func scrollbackDoc(t *testing.T, n int, prefix string, replace bool) string {
	t.Helper()
	rows := make([]RowPatch, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, RowPatch{Row: i, Runs: []Run{{Text: prefix + " row"}}})
	}
	b, err := NewFrameCodec().EncodeScrollback(1, TranscriptBatch{Rows: rows, Replace: replace})
	if err != nil {
		t.Fatalf("encode scrollback: %v", err)
	}
	return string(b)
}

// transcriptRowCount counts the rows in the transcript list.
func transcriptRowCount(t *testing.T, h *clientHarness) int {
	t.Helper()
	return int(h.transcript(t).Get("children").ToObject(h.vm).Get("length").ToInteger())
}

// TestClientJS_TranscriptIsBounded is the regression test for the unbounded
// transcript: however long the session runs, the DOM must not grow with it.
func TestClientJS_TranscriptIsBounded(t *testing.T) {
	h := newClientHarness(t)

	// Far more rows than the cap allows.
	const batches, perBatch = 40, 200
	for i := 0; i < batches; i++ {
		h.deliver(t, scrollbackBatch(t, perBatch, "row"))
	}

	got := transcriptRowCount(t, h)
	if got != transcriptCap {
		t.Errorf("transcript holds %d rows, want the bound's %d", got, transcriptCap)
	}
	if total := batches * perBatch; got >= total {
		t.Errorf("transcript holds %d of %d rows: the bound did not drop anything", got, total)
	}
}

// TestClientJS_TranscriptDropsOldestRows pins WHICH rows fall off: the oldest,
// so the newest output — the part a terminal keeps — is what survives.
func TestClientJS_TranscriptDropsOldestRows(t *testing.T) {
	h := newClientHarness(t)

	h.deliver(t, scrollbackBatch(t, 1000, "oldest"))
	h.deliver(t, scrollbackBatch(t, 1000, "middle"))
	h.deliver(t, scrollbackBatch(t, 1000, "newest"))

	text := h.text(t, h.transcript(t))
	if strings.Contains(text, "oldest row") {
		t.Error("the oldest batch survived: the bound must drop from the front")
	}
	if !strings.Contains(text, "newest row") {
		t.Error("the newest batch was dropped: the bound must keep the newest rows")
	}
	if got := transcriptRowCount(t, h); got != transcriptCap {
		t.Errorf("transcript holds %d rows, want %d", got, transcriptCap)
	}
}

// TestClientJS_TranscriptTrimCompensatesScroll pins the one piece of script that
// touches scrolling: when the oldest rows fall off, the scroll offset moves with
// them, so a reader who is scrolled up keeps looking at the same text instead of
// having it jump by the height of the dropped rows.
func TestClientJS_TranscriptTrimCompensatesScroll(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "screen")

	// Fill to just under the trim threshold, then park the view mid-transcript:
	// a large scroll range keeps the parked offset well away from both ends.
	h.deliver(t, scrollbackBatch(t, transcriptCap, "row"))
	h.setScrollHeight(t, scroll, 100000)
	h.userScrollTo(t, scroll, 50000)
	if got := h.scrollTop(scroll); got != 50000 {
		t.Fatalf("scrollTop = %v, want the parked 50000", got)
	}

	// Enough rows to trip a trim: the oldest batch falls off and the offset must
	// drop by exactly its height (101 rows × 16px in the stub).
	dropped := float64(transcriptBatch+1) * 16
	h.deliver(t, scrollbackBatch(t, transcriptBatch+1, "row"))
	if got := h.scrollTop(scroll); got != 50000-dropped {
		t.Errorf("scrollTop = %v after the trim, want %v (compensated by the dropped height)",
			got, 50000-dropped)
	}
}

// TestClientJS_TranscriptTrimDoesNotGoNegative pins the clamp: at the very top
// there is nothing left to compensate, so the offset floors at 0 rather than
// going negative (which would be an invalid scroll offset).
func TestClientJS_TranscriptTrimDoesNotGoNegative(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "screen")

	h.deliver(t, scrollbackBatch(t, transcriptCap, "row"))
	h.userScrollTo(t, scroll, 0)
	h.deliver(t, scrollbackBatch(t, transcriptBatch+1, "row"))

	if got := h.scrollTop(scroll); got < 0 {
		t.Errorf("scrollTop = %v after a trim at the top, want 0 or more", got)
	}
}

// TestClientJS_TranscriptTrimKeepsFollowTailArmed is the regression test for a
// bug found in a real browser: compensating the scroll offset while the view is
// pinned to the tail leaves it a batch short of the bottom, and the scroll event
// that follows reads that as "the user scrolled away" — so follow-tail detached
// and, after a few trims, the view had drifted to the top of the transcript.
//
// While following, the offset must be left alone and the browser's own clamp
// must be what pins the view to the new bottom.
func TestClientJS_TranscriptTrimKeepsFollowTailArmed(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "screen")

	// A transcript at the bound, with a scroll range that describes it: the
	// transcript's own height plus the live grid's.
	h.deliver(t, scrollbackBatch(t, transcriptCap, "row"))
	const gridHeight = 100
	h.setScrollHeight(t, scroll, transcriptCap*16+gridHeight)

	// Park the view at the bottom, which is what arms follow-tail.
	bottom := float64(transcriptCap*16+gridHeight) - 100 // scrollHeight − clientHeight
	h.userScrollTo(t, scroll, bottom)
	if got := h.scrollTop(scroll); got != bottom {
		t.Fatalf("scrollTop = %v, want the bottom %v", got, bottom)
	}

	// Stream past the bound: rows fall off while the view is following. The trim
	// must not move the view — compensating here is what used to detach
	// follow-tail and let the view drift to the top of the transcript.
	h.deliver(t, scrollbackBatch(t, transcriptBatch+1, "row"))
	if got := h.scrollTop(scroll); got != bottom {
		t.Fatalf("scrollTop = %v after a trim while following, want the bottom %v "+
			"(the trim must not move a following view)", got, bottom)
	}

	// The proof it is still armed: the queued frame pins the view to the bottom.
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != bottom {
		t.Errorf("scrollTop = %v after the follow-tail frame, want the bottom %v: "+
			"follow-tail detached", got, bottom)
	}

	// And it stays armed across further frames.
	h.deliver(t, scrollbackBatch(t, 1, "row"))
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != bottom {
		t.Errorf("scrollTop = %v after the next frame, want the bottom %v: follow-tail detached", got, bottom)
	}
}

// TestClientJS_TranscriptBoundSurvivesRepeatedScrolling pins that the trim
// cannot ratchet: streaming while the user scrolls around must keep the row list
// bounded.
func TestClientJS_TranscriptBoundSurvivesRepeatedScrolling(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "screen")

	for i := 0; i < 12; i++ {
		h.deliver(t, scrollbackBatch(t, 400, "row"))
		h.userScrollTo(t, scroll, 0)
	}
	if got := transcriptRowCount(t, h); got != transcriptCap {
		t.Errorf("transcript holds %d rows after repeated scrolling, want %d", got, transcriptCap)
	}
}

// TestClientJS_TranscriptReplaceDropsWhatTheClientAlreadyHad is B2's client
// half: the server wipes its scrollback and re-emits the whole transcript (the
// compositor does it at a new width), and the rows that follow describe the
// transcript as it stands now. Appending them — which is what the page used to
// do — paints every already-scrolled line a second time, the corruption the
// reported screenshot shows.
func TestClientJS_TranscriptReplaceDropsWhatTheClientAlreadyHad(t *testing.T) {
	h := newClientHarness(t)

	h.deliver(t, scrollbackBatch(t, 5, "history"))
	if got := transcriptRowCount(t, h); got != 5 {
		t.Fatalf("transcript holds %d rows, want the 5 shipped", got)
	}

	// The wipe + re-emit: the same 5 rows plus one the client had not seen.
	h.deliver(t, replaceScrollbackBatch(t, 6, "history"))

	if got := transcriptRowCount(t, h); got != 6 {
		t.Errorf("transcript holds %d rows after a replacing batch, want the batch's 6 "+
			"(the rows it already had must be dropped, not kept alongside the re-emit)", got)
	}
	if n := strings.Count(h.text(t, h.transcript(t)), "history row"); n != 6 {
		t.Errorf("transcript paints %d rows of the replacing batch, want 6: ", n)
	}
}

// TestClientJS_TranscriptReplaceWithoutRowsClearsTheList pins the empty case: a
// replacement carrying no rows is the server saying the transcript is gone (the
// screen was cleared), so the list must be emptied rather than left as it was.
func TestClientJS_TranscriptReplaceWithoutRowsClearsTheList(t *testing.T) {
	h := newClientHarness(t)

	h.deliver(t, scrollbackBatch(t, 4, "stale"))
	h.deliver(t, replaceScrollbackBatch(t, 0, ""))

	if got := transcriptRowCount(t, h); got != 0 {
		t.Errorf("transcript holds %d rows after an empty replacement, want none", got)
	}
}

// TestClientJS_TranscriptScrollsWithoutASpacer pins the native-scrolling
// contract: the transcript is a plain list of real rows with nothing standing in
// for missing content, so the browser's own scroll range and scrollbar describe
// exactly what can be shown.
func TestClientJS_TranscriptScrollsWithoutASpacer(t *testing.T) {
	h := newClientHarness(t)

	h.deliver(t, scrollbackBatch(t, 3, "row"))
	kids := h.transcript(t).Get("children").ToObject(h.vm)
	if got := int(kids.Get("length").ToInteger()); got != 3 {
		t.Fatalf("transcript has %d children, want exactly the 3 rows", got)
	}
	for i := 0; i < 3; i++ {
		child := kids.Get(strconv.Itoa(i)).ToObject(h.vm)
		if cls := child.Get("className").String(); cls != "row" {
			t.Errorf("transcript child %d has class %q, want a row", i, cls)
		}
	}
}
