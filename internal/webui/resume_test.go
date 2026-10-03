// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Reconnect resumes by sequence number (spec §8/§11.2). A frame carries the
// revision it was built from, so a client can say "I have revision N" and the
// server can answer exactly one of two things:
//
//   - the client's view is already current (its seq equals the grid's), so
//     nothing is sent and the screen it already has stays correct;
//   - the client is behind (or impossibly ahead, which means the session was
//     reset underneath it), so it gets one authoritative full frame and its
//     grid stops disagreeing with the server's.
//
// The second case is not hypothetical. Both transports drop the oldest queued
// frame for a client that cannot keep up, so a live client can miss a delta and
// silently drift out of sync; the seq is what lets it notice and ask again.

// newSSERequest builds an open GET /events request for a raw URL, so a test can
// attach to a stream at an exact query string.
func newSSERequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build stream request: %v", err)
	}
	return req
}

// doSSE issues a stream request and requires it to have opened.
func doSSE(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", req.URL, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("GET %s status = %d", req.URL, resp.StatusCode)
	}
	return resp
}

// sseReader drains one live stream in the background. Frames and controls share
// the wire but not the queue, and "the server correctly said nothing" is an
// outcome these assertions require, so a single reader goroutine feeds a channel
// that callers poll with their own deadlines — reading one body twice would
// otherwise hand the socket to two competing scanners.
type sseReader struct {
	payloads chan string
}

// newSSEReader opens a stream at an exact URL and starts draining it.
func newSSEReader(t *testing.T, url string) (*sseReader, func()) {
	t.Helper()
	resp := doSSE(t, newSSERequest(t, url))
	r := &sseReader{payloads: make(chan string, 64)}
	go func() {
		defer close(r.payloads)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				r.payloads <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return r, func() { _ = resp.Body.Close() }
}

// next returns the next payload, or "" when the stream was silent for longer
// than within.
func (r *sseReader) next(within time.Duration) string {
	select {
	case p, ok := <-r.payloads:
		if !ok {
			return ""
		}
		return p
	case <-time.After(within):
		return ""
	}
}

func itoa(v uint64) string { return strconv.FormatUint(v, 10) }

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func wsSend(t *testing.T, conn *websocket.Conn, m clientMsg) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode client message: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write client message: %v", err)
	}
}

// readFrameWithin reads frames until one satisfies pred or the deadline passes.
// It reports whether the predicate was satisfied. Frames and controls travel on
// separate queues, so arrival order is not guaranteed.
func readFrameWithin(t *testing.T, conn *websocket.Conn, within time.Duration, pred func(*Frame) bool) *Frame {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if time.Now().After(deadline) {
			return nil
		}
		_ = conn.SetReadDeadline(deadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			return nil
		}
		f, err := NewFrameCodec().DecodeFrame(data)
		if err != nil {
			continue // a control message, or something we do not model here
		}
		if pred(f) {
			return f
		}
	}
}

// A client that missed output asks for the current screen with the seq it last
// saw, and must get one full frame carrying what it missed.
func TestWS_HelloWithStaleSeqResendsFullFrame(t *testing.T) {
	conn, vt, _ := dialTestServer(t)
	joined := readFrame(t, conn)

	// The session moves on and the client misses those frames.
	vt.WriteString("\x1b[1;1Hmissed me")

	wsSend(t, conn, clientMsg{T: MsgHello, Since: joined.Seq})
	got := readFrameWithin(t, conn, 3*time.Second, func(f *Frame) bool { return f.Full })
	if got == nil {
		t.Fatal("a stale hello did not produce a full frame")
	}
	if !patchTextContains(got, "missed me") {
		t.Errorf("resync frame does not carry the missed output: %q", patchText(got))
	}
	if got.Seq < vt.Seq() {
		t.Errorf("resync frame seq = %d, want the current revision %d", got.Seq, vt.Seq())
	}
}

// A client whose view is already current must not be resent the screen: the
// hello it sends on every (re)connect is a resume check, not a resync request.
func TestWS_HelloWithCurrentSeqResendsNothing(t *testing.T) {
	conn, vt, _ := dialTestServer(t)
	joined := readFrame(t, conn)

	wsSend(t, conn, clientMsg{T: MsgHello, Since: joined.Seq})
	if extra := readFrameWithin(t, conn, 300*time.Millisecond, func(*Frame) bool { return true }); extra != nil {
		t.Errorf("an in-sync client was sent frame seq %d; no resync was due", extra.Seq)
	}
	_ = vt
}

// A client claiming a revision the grid never reached has lost track of the
// session (a reset, or a session rotation). The only safe answer is the whole
// screen.
func TestWS_HelloFromTheFutureResendsFullFrame(t *testing.T) {
	conn, vt, _ := dialTestServer(t)
	readFrame(t, conn)

	wsSend(t, conn, clientMsg{T: MsgHello, Since: vt.Seq() + 1000})
	if got := readFrameWithin(t, conn, 3*time.Second, func(f *Frame) bool { return f.Full }); got == nil {
		t.Fatal("a hello from a revision the grid never reached produced no full frame")
	}
}

// The event stream resumes the same way: a client that reconnects with the seq
// it holds gets a frame only when its view is behind.
func TestEvents_SinceInSyncSkipsTheFullFrame(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)
	vt.WriteString("\x1b[1;1Hfirst")

	s := attachSSE(t, base)
	first := decodeFrame(t, s.next(t))
	if !first.Full {
		t.Fatalf("a joining stream did not open with a full frame: %+v", first)
	}

	// Reconnect announcing the revision it already holds. Nothing changed in
	// between, so the stream must open silently.
	resumed, closeResumed := newSSEReader(t, base+"/events?s=sess-1&since="+itoa(first.Seq))
	defer closeResumed()
	if got := resumed.next(300 * time.Millisecond); got != "" {
		t.Errorf("an in-sync resume opened with a frame: %q", got)
	}

	// Anything that moves the grid afterwards still arrives.
	vt.WriteString("\x1b[1;1Hsecond")
	if got := resumed.next(3 * time.Second); got == "" {
		t.Fatal("the resumed stream delivered no live frame")
	}
}

// A stream that reconnects behind gets the authoritative screen, same as a
// joining one.
func TestEvents_StaleSinceGetsFullFrame(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)
	vt.WriteString("\x1b[1;1Hbefore")

	s := attachSSE(t, base)
	stale := decodeFrame(t, s.next(t))
	if stale.Seq == 0 {
		t.Fatal("first frame carried no seq")
	}

	vt.WriteString("\x1b[1;1Hwhile away")
	resumed, closeResumed := newSSEReader(t, base+"/events?s=sess-1&since=1")
	defer closeResumed()

	payload := resumed.next(3 * time.Second)
	if payload == "" {
		t.Fatal("a stale resume opened with no frame")
	}
	msg := decodeFrame(t, payload)
	if !msg.Full {
		t.Errorf("stale resume frame not marked full: %+v", msg)
	}
	if !containsAll(payload, "while away") {
		t.Errorf("stale resume does not carry the missed output: %q", payload)
	}
}

// A resume from a revision the grid never reached is a broken client; send it
// the screen rather than let it paint deltas against nothing.
func TestEvents_ResyncSinceBeyondCurrentIsFull(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)

	reader, closeStream := newSSEReader(t, base+"/events?s=sess-1&since=99999")
	defer closeStream()

	payload := reader.next(3 * time.Second)
	if payload == "" {
		t.Fatal("resuming from an unknown revision produced no frame")
	}
	if msg := decodeFrame(t, payload); !msg.Full {
		t.Errorf("resume from an unknown revision not marked full: %+v", msg)
	}
}

// An in-sync resume must not cost a byte — and the stream it opens must still be
// attached to the hub, or the "nothing to send" answer would be indistinguishable
// from a dead stream.
func TestEvents_ResumeKeepsStreamAttached(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)
	vt.WriteString("\x1b[1;1Hfirst")

	reader, closeStream := newSSEReader(t, base+"/events?s=sess-1&since="+itoa(vt.Seq()))
	defer closeStream()
	if got := reader.next(300 * time.Millisecond); got != "" {
		t.Fatalf("an in-sync resume opened with a frame: %q", got)
	}

	vt.WriteString("\x1b[1;1Hsecond")
	if got := reader.next(3 * time.Second); !containsAll(got, "second") {
		t.Errorf("the resumed stream is not attached to the hub: %q", got)
	}
}
