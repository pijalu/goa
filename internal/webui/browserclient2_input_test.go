// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// A read-only notice arriving over the fallback must still stop the page from
// typing — the badge is the only cue the viewer gets.
func TestClientJS_SSEReadOnlyStopsInput(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	h.call(t, "__emitEvent", `{"t":"read_only","text":"server is read-only"}`)
	h.keydown(t, map[string]any{"key": "a", "code": "KeyA"})

	if posts := h.posts(t); len(posts) != 0 {
		t.Errorf("read-only viewer still posted %v", posts)
	}
	if status := h.textOf(t, "status"); !strings.Contains(status, "read-only") {
		t.Errorf("status = %q, want the viewer mode announced", status)
	}
}

// The POST bodies the page emits over the fallback must be exactly what the
// server's handlers parse — the wire contract is shared, not duplicated.
func TestClientJS_SSEPostBodiesMatchTheServerSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		act  func(h *clientHarness)
		// check asserts the decoded clientMsg the server would see.
		check func(t *testing.T, msg clientMsg)
	}{
		{
			name: "key",
			want: "/key",
			act:  func(h *clientHarness) { h.keydown(t, map[string]any{"key": "a", "code": "KeyA", "ctrlKey": true}) },
			check: func(t *testing.T, msg clientMsg) {
				if msg.Key.Code != "KeyA" {
					t.Errorf("key descriptor lost its code: %+v", msg.Key)
				}
			},
		},
		{
			name: "paste",
			want: "/input",
			act:  func(h *clientHarness) { h.paste(t, "pasted bytes") },
			check: func(t *testing.T, msg clientMsg) {
				if msg.Data != "pasted bytes" {
					t.Errorf("paste payload = %q", msg.Data)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newClientHarness(t)
			h.failHandshake(t)
			h.stubPosts(t)
			tc.act(h)

			posts := h.posts(t)
			if len(posts) != 1 {
				t.Fatalf("posted %d times, want 1", len(posts))
			}
			if posts[0]["url"] != tc.want {
				t.Errorf("posted to %v, want %v", posts[0]["url"], tc.want)
			}
			raw, _ := posts[0]["body"].(string)
			var msg clientMsg
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				t.Fatalf("server could not parse %q: %v", raw, err)
			}
			tc.check(t, msg)
		})
	}
}

// TestClientJS_InputTypedWhileConnectingIsHeldAndFlushed pins the page half of
// B7: the socket can be CONNECTING (the listener answered, the session is still
// wiring itself up) and a keystroke typed in that window is the user's input —
// it must be held and delivered when the socket opens, not dropped on the floor.
func TestClientJS_InputTypedWhileConnectingIsHeldAndFlushed(t *testing.T) {
	h := newClientHarness(t)
	// CONNECTING must be visible to the page's comparison, and the socket has to
	// report that state (the stub opens instantly, which hides the window).
	if _, err := h.vm.RunString(`WebSocket.CONNECTING = 0; 1`); err != nil {
		t.Fatalf("define CONNECTING: %v", err)
	}
	sock := h.vm.Get("__socket").ToObject(h.vm)
	if err := sock.Set("readyState", int64(0)); err != nil {
		t.Fatalf("set readyState: %v", err)
	}

	// A key typed while the socket is still connecting.
	h.keydown(t, map[string]any{"key": "a", "code": "KeyA"})
	if got := sentOf(t, sock); len(got) != 0 {
		t.Fatalf("page sent %d messages before the socket opened, want 0 (held): %v", len(got), got)
	}

	// The socket opens. A browser sets readyState=OPEN before invoking onopen, so
	// the stub must too — otherwise the page would (correctly) refuse to flush.
	if err := sock.Set("readyState", int64(1)); err != nil {
		t.Fatalf("set readyState=OPEN: %v", err)
	}
	h.call(t, "__opened")
	got := sentOf(t, sock)
	var held int
	for _, msg := range got {
		if strings.Contains(msg, `"key"`) {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("messages after open = %v, want the held key event exactly once", got)
	}
	if len(got) < 3 {
		t.Fatalf("open must also send hello + resize, got %v", got)
	}
}

// sentOf returns the raw messages the stub socket has been given.
func sentOf(t *testing.T, sock *goja.Object) []string {
	t.Helper()
	raw := sock.Get("sent").Export()
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("socket.sent is %T, want a slice", raw)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

// An image has no textual form: the page uploads it and inserts the stored path,
// which is what the terminal editor inserts for a clipboard image.
func TestClientJS_ImagePasteUploadsThenInsertsPath(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.stubUpload(t, "/tmp/goa-upload-123.png")

	prevented := h.pasteImage(t, "image/png")
	if !prevented {
		t.Error("image paste did not preventDefault")
	}
	if got := h.uploadCalls(t); got != 1 {
		t.Fatalf("upload calls = %d, want 1", got)
	}
	sent := h.sent(t)
	if len(sent) != 1 || sent[0]["t"] != "input" || sent[0]["data"] != "/tmp/goa-upload-123.png" {
		t.Fatalf("image paste produced %v, want the uploaded path as input", sent)
	}
}

// A failed upload must be reported, not silently swallowed: the user pasted an
// image and nothing arrived at the agent.
func TestClientJS_ImagePasteFailureIsReported(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.stubUploadFailure(t, "input rejected: read-only mode")

	h.pasteImage(t, "image/png")
	if got := h.uploadCalls(t); got != 1 {
		t.Fatalf("upload calls = %d, want 1", got)
	}
	if status := h.textOf(t, "status"); !strings.Contains(status, "image upload failed") {
		t.Errorf("status = %q, want the upload failure", status)
	}
	if sent := h.sent(t); len(sent) != 0 {
		t.Errorf("failed upload still sent %v", sent)
	}
}
