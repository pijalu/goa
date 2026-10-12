// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"
	"testing"
)

// fullFrameDoc encodes a frame that carries every row, i.e. what a resync reply
// looks like on the wire.
func fullFrameDoc(t *testing.T, row int, text string, seq uint64) string {
	t.Helper()
	b, err := NewFrameCodec().EncodeFrame(&Frame{
		Seq:     seq,
		Cols:    80,
		Rows:    24,
		Cursor:  Cursor{Row: row, Col: 0, Visible: true},
		Full:    true,
		Patches: []RowPatch{{Row: row, Runs: []Run{{Text: text}}}},
	})
	if err != nil {
		t.Fatalf("encode full frame: %v", err)
	}
	return string(b)
}

// timerDelays returns the delay of every timer the page has scheduled, in the
// order it scheduled them. A cancelled timer is skipped: the page clears the
// handshake watchdog as soon as the socket opens.
func timerDelays(t *testing.T, h *clientHarness) []int64 {
	t.Helper()
	timers := h.call(t, "__timers").ToObject(h.vm)
	var out []int64
	for _, key := range timers.Keys() {
		entry := timers.Get(key).ToObject(h.vm)
		if entry.Get("cancelled").ToBoolean() {
			continue
		}
		out = append(out, int64(entry.Get("ms").ToInteger()))
	}
	return out
}

// Client-side resume (spec §8). The server answers a hello, or an EventSource
// reconnect carrying ?since=, with the authoritative screen only when the
// browser's view is behind. The browser's half of that contract is noticing it
// fell behind in the first place: both transports drop the oldest queued frame
// for a client that cannot keep up, so a delta can go missing while the socket
// is perfectly healthy. Patching that delta onto a stale grid is how the page
// silently starts lying about the session.

// helloSince returns the `since` of the last hello the page sent over the
// socket, reporting whether one was sent at all.
func helloSince(t *testing.T, h *clientHarness) (float64, bool) {
	t.Helper()
	var since float64
	found := false
	for _, m := range h.sent(t) {
		if m["t"] == "hello" {
			since = numOf(m["since"])
			found = true
		}
	}
	return since, found
}

// gridText returns what the live grid shows.
func gridText(t *testing.T, h *clientHarness) string {
	t.Helper()
	return h.text(t, h.el(t, "grid"))
}

// A delta whose seq skips one or more frames means the browser missed output.
// It must not paint that delta — the rows it carries are only meaningful on top
// of the frames in between — and must ask the server to resend the screen.
func TestClientJS_SeqGapTriggersResyncInsteadOfPainting(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.clearSent(t)

	h.deliver(t, frameDocSeq(t, 0, "base line", 10))
	if got := gridText(t, h); !strings.Contains(got, "base line") {
		t.Fatalf("grid = %q, want the first delta painted", got)
	}

	// seq 13 after 10: two frames went missing.
	h.deliver(t, frameDocSeq(t, 1, "orphaned delta", 13))

	if got := gridText(t, h); strings.Contains(got, "orphaned delta") {
		t.Errorf("grid = %q, painted a delta across a seq gap", got)
	}
	since, ok := helloSince(t, h)
	if !ok {
		t.Fatal("a seq gap produced no hello asking for a resync")
	}
	if since != 10 {
		t.Errorf("resync asked for since=%v, want the last contiguous seq 10", since)
	}
}

// The resync reply is a full frame: it paints and re-anchors the client even
// though its seq skips ahead.
func TestClientJS_FullFrameAfterGapRepaintsAndReanchors(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.clearSent(t)

	h.deliver(t, frameDocSeq(t, 0, "base line", 10))
	h.deliver(t, frameDocSeq(t, 1, "orphaned delta", 13))
	h.clearSent(t)

	// The server answers with the whole screen at the current revision.
	h.deliver(t, fullFrameDoc(t, 1, "authoritative screen", 13))

	if got := gridText(t, h); !strings.Contains(got, "authoritative screen") {
		t.Errorf("grid = %q, want the resync frame painted", got)
	}
	if _, ok := helloSince(t, h); ok {
		t.Error("the client asked to resync again after receiving the full frame")
	}
	// And the client is now in step with the server.
	h.clearSent(t)
	h.deliver(t, frameDocSeq(t, 2, "next delta", 14))
	if _, ok := helloSince(t, h); ok {
		t.Error("a contiguous delta after a resync must not trigger another one")
	}
	if got := gridText(t, h); !strings.Contains(got, "next delta") {
		t.Errorf("grid = %q, want the delta after the resync painted", got)
	}
}

// A frame the client has already seen (a replay, or a duplicate after a
// reconnect) must not repaint or re-trigger a resync.
func TestClientJS_StaleFrameIsIgnored(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.clearSent(t)

	h.deliver(t, frameDocSeq(t, 0, "current", 10))
	h.clearSent(t)

	h.deliver(t, frameDocSeq(t, 2, "stale row", 10))
	if got := gridText(t, h); strings.Contains(got, "stale row") {
		t.Errorf("grid = %q, painted a frame it already had", got)
	}
	if _, ok := helloSince(t, h); ok {
		t.Error("an already-seen frame triggered a resync request")
	}
}

// Frames without a seq are still honoured: a server that stops numbering them
// must not freeze the page.
func TestClientJS_UnnumberedFramesStillPaint(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.deliver(t, frameDoc(t, 0, "no seq one"))
	h.deliver(t, frameDoc(t, 1, "no seq two"))

	got := gridText(t, h)
	if !strings.Contains(got, "no seq one") || !strings.Contains(got, "no seq two") {
		t.Errorf("grid = %q, want both unnumbered frames painted", got)
	}
}

// Over the fallback there is no socket to send a hello on, so a resync means
// reopening the stream at the revision the page holds — the EventSource URL is
// where the seq travels.
func TestClientJS_SSEReconnectResumesFromLastSeq(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.call(t, "__emitEvent", frameDocSeq(t, 0, "degraded line", 7))

	h.call(t, "__streamError")

	if !h.call(t, "__fireTimer").ToBoolean() {
		t.Fatal("a dropped event stream scheduled no resume")
	}
	if got := int(h.call(t, "__streamCount").ToInteger()); got != 2 {
		t.Fatalf("opened %d streams, want the original plus one resume", got)
	}
	resumed := h.vm.Get("__events").ToObject(h.vm)
	if !strings.Contains(resumed.Get("url").String(), "since=7") {
		t.Errorf("resumed stream url = %q, want the revision the page held", resumed.Get("url").String())
	}
	if !h.vm.Get("__streams").ToObject(h.vm).Get("0").ToObject(h.vm).Get("closed").ToBoolean() {
		t.Error("the dropped stream was left open alongside its resume")
	}
}

// A first SSE connection has nothing to resume from, so it must not claim a
// revision it does not hold.
func TestClientJS_SSEFirstConnectHasNoSince(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)

	url := h.vm.Get("__events").ToObject(h.vm).Get("url").String()
	if strings.Contains(url, "since=") {
		t.Errorf("first stream url = %q, want no resume hint", url)
	}
}

// A seq gap while the stream is up is the same problem as a dropped stream: the
// page must reopen at the last contiguous revision rather than paint over a gap.
func TestClientJS_SSEGapReopensAtLastContiguousSeq(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.call(t, "__emitEvent", frameDocSeq(t, 0, "base", 4))
	h.call(t, "__emitEvent", frameDocSeq(t, 1, "gapped", 8))

	if !h.call(t, "__fireTimer").ToBoolean() {
		t.Fatal("a seq gap over SSE scheduled no resume")
	}
	resumed := h.vm.Get("__events").ToObject(h.vm)
	if !strings.Contains(resumed.Get("url").String(), "since=4") {
		t.Errorf("resumed url = %q, want the last contiguous seq 4", resumed.Get("url").String())
	}
}

// The resume must not become a hot loop: a stream that keeps failing backs off
// instead of reopening immediately forever.
func TestClientJS_SSEResumeBacksOff(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)

	var delays []int64
	for i := 0; i < 3; i++ {
		h.call(t, "__streamError")
		// The wait is visible before the timer runs; running it consumes the
		// record, so each round samples its own delay.
		pending := timerDelays(t, h)
		if len(pending) == 0 {
			t.Fatalf("stream error %d scheduled no resume", i)
		}
		delays = append(delays, pending[len(pending)-1])
		if !h.call(t, "__fireTimer").ToBoolean() {
			t.Fatalf("resume %d did not reopen the stream", i)
		}
	}
	for i := 1; i < len(delays); i++ {
		if delays[i] <= delays[i-1] {
			t.Errorf("resume delays %v did not grow; the page would spin", delays)
			break
		}
	}
}
