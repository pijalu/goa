// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// An upgrade-blocked connection must fall back to SSE for the rest of the page
// load, and the badge must say the transport is degraded (spec §11.2).
func TestClientJS_WebSocketFailureFallsBackToSSE(t *testing.T) {
	h := newClientHarness(t)
	if h.hasEventSource(t) {
		t.Fatal("page opened an event stream before the socket failed")
	}

	h.failHandshake(t)

	if !h.hasEventSource(t) {
		t.Fatal("failed handshake did not open the SSE stream")
	}
	es := h.vm.Get("__events").ToObject(h.vm)
	if url := es.Get("url").String(); !strings.HasPrefix(url, "/events?s=") {
		t.Errorf("EventSource url = %q, want the /events fallback", url)
	}
	if status := h.textOf(t, "status"); !strings.Contains(status, "degraded") {
		t.Errorf("status = %q, want a degraded badge", status)
	}
}

// Once the socket had opened and then dropped, the page must retry the
// WebSocket — the handshake itself worked, so SSE is the wrong answer.
func TestClientJS_LiveSocketDropRetriesWebSocketNotSSE(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__wsDropped")
	if h.hasEventSource(t) {
		t.Error("a dropped-but-established socket must not trigger the SSE fallback")
	}
	if !h.call(t, "__fireTimer").ToBoolean() {
		t.Fatal("dropping a live socket did not schedule a reconnect")
	}
	if h.vm.Get("__socket").ToObject(h.vm).Get("readyState").ToInteger() != 1 {
		t.Error("reconnect did not open a fresh WebSocket")
	}
}

// Frames delivered over the SSE stream must paint exactly like WebSocket frames:
// one wire format, one render path (spec §11.2).
func TestClientJS_SSEFramesPaintTheGrid(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)

	h.call(t, "__emitEvent", frameDoc(t, 2, "degraded line"))

	grid := h.el(t, "grid")
	if got := h.text(t, grid); !strings.Contains(got, "degraded line") {
		t.Fatalf("grid = %q, want the SSE-delivered text", got)
	}
}

// With the fallback active, a keystroke must be POSTed as a *descriptor* to
// /key, not as empty raw bytes: the server still owns the byte table, so losing
// the socket cannot silently degrade typing into nothing.
func TestClientJS_SSEKeydownGoesOutAsKeyPost(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	h.keydown(t, map[string]any{"key": "a", "code": "KeyA"})

	posts := h.posts(t)
	if len(posts) != 1 {
		t.Fatalf("posted %d times, want 1", len(posts))
	}
	if url := posts[0]["url"]; url != "/key" {
		t.Errorf("posted to %v, want /key", url)
	}
	if sent := h.sent(t); len(sent) != 0 {
		t.Errorf("page also wrote to the dead socket: %v", sent)
	}
}

// Paste over the fallback is one POSTed payload, exactly as over the socket.
func TestClientJS_SSEPasteGoesOutAsPost(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	h.paste(t, "pasted\n")

	posts := h.posts(t)
	if len(posts) != 1 || posts[0]["url"] != "/input" {
		t.Fatalf("paste produced %v, want one POST to /input", posts)
	}
}

// A resize over the fallback must go to /resize, not /input: they are different
// endpoints with different payloads. The geometry message is debounced (spec
// §14.6) so a drag-resize burst becomes one repaint, not one per pixel.
func TestClientJS_SSEResizeGoesToResizeEndpoint(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	if got := int(h.call(t, "__fireWindow", "resize").ToInteger()); got == 0 {
		t.Fatal("page registered no resize listener")
	}
	if posts := h.posts(t); len(posts) != 0 {
		t.Fatalf("resize was not debounced; posted immediately: %v", posts)
	}
	if !h.call(t, "__fireTimer").ToBoolean() {
		t.Fatal("debounced resize never scheduled a geometry message")
	}
	posts := h.posts(t)
	if len(posts) == 0 || posts[0]["url"] != "/resize" {
		t.Fatalf("resize produced %v, want a POST to /resize", posts)
	}
}

// TestClientJS_ScrollListenerIsOnTheScrollContainer pins WHICH element owns the
// follow-tail listener: the scroll container, not the transcript list inside it.
// Listening on a non-scrollable child is exactly how "scrolling does nothing"
// shipped once already.
func TestClientJS_ScrollListenerIsOnTheScrollContainer(t *testing.T) {
	h := newClientHarness(t)
	screen := h.el(t, "screen")
	sb := h.el(t, "scrollback")
	if n := int(h.call(t, "__listenersOn", screen, "scroll").ToInteger()); n != 1 {
		t.Errorf("scroll container has %d scroll listeners, want exactly 1", n)
	}
	if n := int(h.call(t, "__listenersOn", sb, "scroll").ToInteger()); n != 0 {
		t.Errorf("transcript list has %d scroll listeners; the listener belongs on the container", n)
	}
}

// TestClientJS_ShrinkTrimsGridRows pins that a shrinking screen removes the rows
// that no longer exist. Keeping them left stale rows below the new bottom,
// inflating the scroll height and pushing the input box (and the caret) off
// screen — the visible symptom of "resizing does not work well".
func TestClientJS_ShrinkTrimsGridRows(t *testing.T) {
	h := newClientHarness(t)
	rowsBox := h.el(t, "rows")

	h.deliver(t, frameDocRows(t, 30, "top"))
	if n := h.count(t, rowsBox); n != 30 {
		t.Fatalf("rows after a 30-row frame = %d, want 30", n)
	}
	h.deliver(t, frameDocRows(t, 12, "top"))
	if n := h.count(t, rowsBox); n != 12 {
		t.Fatalf("rows after shrinking to 12 = %d, want 12 (stale rows were kept)", n)
	}
}

// The clipboard chords are the PAGE's: a non-editable grid gets neither a `copy`
// nor a `paste` event from the browser, so the page performs them itself —
// measured in Chrome with trusted chords and a real clipboard (bugs.md B5). These
// tests pin one chord each: copy, the interrupt, paste, cut.
func TestClientJS_CopyChordCopiesTheSelection(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	// Ctrl+C with a selection: claimed, copied, and nothing sent to the terminal.
	h.call(t, "__setSelection", "copied text")
	if !h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true}) {
		t.Error("page let Ctrl+C through with a live selection; the copy would be lost")
	}
	if got := h.clipboardText(t); got != "copied text" {
		t.Errorf("clipboard = %q after Ctrl+C, want the selection", got)
	}
	if got := h.clipboardReads(t); got != 0 {
		t.Errorf("Ctrl+C read the clipboard %d times", got)
	}

	// Cmd+C with a selection is the same chord on macOS.
	if !h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "metaKey": true}) {
		t.Error("page let Cmd+C through with a live selection; the copy would be lost")
	}
}

// TestClientJS_CtrlCWithoutSelectionIsTheInterrupt pins the other half of the
// copy chord: with nothing selected the page must send it to the engine (0x03),
// or a running turn can no longer be interrupted.
func TestClientJS_CtrlCWithoutSelectionIsTheInterrupt(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setSelection", "")
	if !h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true}) {
		t.Error("page let Ctrl+C through with no selection; the terminal lost its interrupt")
	}
	if !h.sentKey(t, "c", "ctrl") {
		t.Error("Ctrl+C with no selection did not reach the engine as a key event")
	}
	if got := h.clipboardText(t); got != "" {
		t.Errorf("Ctrl+C with no selection wrote %q to the clipboard", got)
	}
}

// TestClientJS_PasteChordHandsOffToTheBrowser pins the paste contract: the page
// focuses its hidden paste target and does NOT preventDefault, so the browser's
// own paste can deliver the clipboard (the only read path that needs no
// permission). The chord's keyup closes the window; when no paste event arrived —
// what a script-dispatched chord does — the page reads the clipboard itself.
func TestClientJS_PasteChordHandsOffToTheBrowser(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setClipboard", "pasted text")
	if h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true}) {
		t.Error("page claimed Ctrl+V; the browser's own paste would be suppressed")
	}
	if got := h.activeElement(t); got != "paste-target" {
		t.Errorf("focus after Ctrl+V = %q, want the paste target the browser pastes into", got)
	}
	h.keyup(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if got := h.sentInput(t); got != "pasted text" {
		t.Errorf("paste sent %q as input, want the clipboard text", got)
	}
	if n := h.countInputs(t); n != 1 {
		t.Errorf("paste inserted %d times; want exactly once", n)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after the paste = %q, want the grid back", got)
	}
}

// TestClientJS_CutChordCopiesAndDeletes pins the cut: the selection goes to the
// clipboard and is removed from the input line with the engine's own keys. With
// the selection ending at the cursor that is one Backspace per character.
func TestClientJS_CutChordCopiesAndDeletes(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.deliver(t, frameDocCursor(t, 6, 2, 6, "abcdef"))
	h.call(t, "__setSelectionRange", "def", 3*8, 6*8)
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Error("page let Ctrl+X through with a live selection; the cut would be lost")
	}
	if got := h.clipboardText(t); got != "def" {
		t.Errorf("clipboard = %q after Ctrl+X, want the selection", got)
	}
	if got := h.sentKeys(t); got != "Backspace,Backspace,Backspace" {
		t.Errorf("cut sent %q, want three Backspaces for three characters", got)
	}
}

// TestClientJS_NativePasteStopsTheFallback pins the double-insert guard: when
// the browser's own paste arrives (the primary path — no permission involved),
// its text is inserted once and the clipboard fallback must not insert it again.
func TestClientJS_NativePasteStopsTheFallback(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setClipboard", "clipboard text")
	h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if got := h.activeElement(t); got != "paste-target" {
		t.Fatalf("focus after Ctrl+V = %q, want the paste target", got)
	}
	if !h.paste(t, "clipboard text") {
		t.Error("the browser's paste was not prevented; the text would land in the textarea")
	}
	if got := h.sentInput(t); got != "clipboard text" {
		t.Errorf("native paste inserted %q, want the clipboard text", got)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after the native paste = %q, want the grid back", got)
	}
	// The keyup closes the paste window with the browser's paste already in: it
	// must stay silent, or the clipboard would be inserted twice.
	h.keyup(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if n := h.countInputs(t); n != 1 {
		t.Errorf("paste inserted %d times; the fallback duplicated the browser's paste", n)
	}
}

// TestClientJS_PasteWindowBackstopReadsWithoutAKeyup pins the backstop: a chord
// delivered without its keyup still gets its clipboard read, so a remote-input or
// synthetic chord cannot stall the paste.
func TestClientJS_PasteWindowBackstopReadsWithoutAKeyup(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setClipboard", "backstop text")
	h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if !h.fireTimerOf(t, 500) {
		t.Fatal("paste scheduled no backstop timer")
	}
	if got := h.sentInput(t); got != "backstop text" {
		t.Errorf("backstop paste sent %q, want the clipboard text", got)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after the backstop paste = %q, want the grid back", got)
	}
}

// TestClientJS_PasteStaysWithTheBrowserWithoutAClipboardAPI pins the fallback:
// on an origin with no async clipboard (plain http on a LAN address) the page
// cannot read the clipboard itself, so it must LEAVE the chord to the browser —
// claim it and a browser that does fire a `paste` event would be swallowed.
func TestClientJS_PasteStaysWithTheBrowserWithoutAClipboardAPI(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.call(t, "__dropClipboardAPI")
	if h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true}) {
		t.Error("page claimed Ctrl+V; the browser's own paste would be suppressed")
	}
	// The paste window is always opened (the browser may still deliver a paste);
	// with no clipboard API the fallback can only give up and put the focus back.
	h.keyup(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if n := h.countInputs(t); n != 0 {
		t.Errorf("a paste the page could not read still sent %d input messages", n)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after an unreadable paste = %q, want the grid back", got)
	}
}

// TestClientJS_CopyFallsBackToExecCommand pins the legacy write path: an
// insecure origin has no async clipboard, and a refused write must not lose the
// copy while execCommand can still put the selection on the real clipboard.
func TestClientJS_CopyFallsBackToExecCommand(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	// No clipboard API at all: the legacy path is the only one.
	h.call(t, "__dropClipboardAPI")
	h.call(t, "__setSelection", "legacy copy")
	h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true})
	if cmds := h.execCommands(t); len(cmds) != 1 || cmds[0] != "copy" {
		t.Errorf("execCommand calls = %v, want one copy", cmds)
	}

	// Async write refused: the page must fall back rather than give up.
	h2 := newClientHarness(t)
	h2.open(t)
	h2.call(t, "__denyClipboardWrites", true)
	h2.call(t, "__setSelection", "fallback")
	h2.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true})
	if cmds := h2.execCommands(t); len(cmds) != 1 || cmds[0] != "copy" {
		t.Errorf("execCommand calls after a refused write = %v, want one copy", cmds)
	}
}

// TestClientJS_ReadOnlyViewerStillCopiesButNeverDrives pins the viewer's half of
// the contract: reading the screen is not driving it, so a copy works; paste and
// cut never reach the session.
func TestClientJS_ReadOnlyViewerStillCopiesButNeverDrives(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.deliver(t, frameDocCursor(t, 6, 2, 6, "abcdef"))
	h.call(t, "__deliver", h.vm.ToValue(map[string]any{"t": "read_only", "text": "viewer"}))
	h.call(t, "__setSelection", "shared line")

	h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true})
	if got := h.clipboardText(t); got != "shared line" {
		t.Errorf("read-only viewer copied %q, want the selection", got)
	}

	h.reset(t)
	h.call(t, "__setClipboard", "drives the session")
	h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	h.call(t, "__setSelectionRange", "def", 3*8, 6*8)
	h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true})
	if n := h.countInputs(t); n != 0 {
		t.Errorf("a read-only viewer sent %d input messages", n)
	}
	if got := h.sentKeys(t); got != "" {
		t.Errorf("a read-only viewer sent keys to the session: %q", got)
	}
}

// TestClientJS_CutWalksTheCursorToTheSelection pins the general cut: when the
// selection is neither at the cursor nor reaching it, the cursor is walked to the
// selection's end (column arithmetic over the row's pixels) and the selected
// characters are deleted behind it.
func TestClientJS_CutWalksTheCursorToTheSelection(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	// Cursor at column 1, selection covers columns 3..5 of a single-width row, so
	// the cursor walks to the selection's end (column 5, four steps right) before
	// the two selected characters are deleted behind it.
	h.deliver(t, frameDocCursor(t, 6, 2, 1, "abcdef"))
	h.call(t, "__setSelectionRange", "de", 3*8, 5*8)
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Fatal("page let Ctrl+X through with a live selection")
	}
	if got := h.clipboardText(t); got != "de" {
		t.Errorf("clipboard = %q after the cut, want the selection", got)
	}
	if got := h.sentKeys(t); got != "ArrowRight,ArrowRight,ArrowRight,ArrowRight,Backspace,Backspace" {
		t.Errorf("cut sent %q, want four rights (cursor col 1 → selection end col 5) then two backspaces", got)
	}
}

// TestClientJS_CutLeavesOutputAlone pins that a selection reaching outside the
// cursor's row (the transcript above the input line) is copied, never deleted:
// output is not the editor's buffer.
func TestClientJS_CutLeavesOutputAlone(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.deliver(t, frameDocCursor(t, 6, 2, 1, "abcdef"))
	// Rect outside the cursor row's box (top 0..16): the transcript.
	h.call(t, "__setSelectionRange", "output line", 0, 8)
	h.vm.RunString("window.__range.getClientRects = function () { return [{ left: 0, right: 80, top: -32, bottom: -16 }]; }")
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Fatal("page let Ctrl+X through with a live selection")
	}
	if got := h.clipboardText(t); got != "output line" {
		t.Errorf("clipboard = %q after the cut, want the selection", got)
	}
	if got := h.sentKeys(t); got != "" {
		t.Errorf("cut deleted %q from the session although the selection is output", got)
	}
}

// TestClientJS_CutSkipsTheWalkOnAWideGlyphLine pins the width guard: a line
// holding a two-cell glyph makes cell columns and buffer characters disagree, so
// a cut that does not touch the cursor copies and leaves the line alone instead
// of deleting the wrong characters.
func TestClientJS_CutSkipsTheWalkOnAWideGlyphLine(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.deliver(t, frameDocCursor(t, 6, 2, 1, "\u4f60\u597dabcdef"))
	h.call(t, "__setSelectionRange", "de", 3*8, 5*8)
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Fatal("page let Ctrl+X through with a live selection")
	}
	if got := h.clipboardText(t); got != "de" {
		t.Errorf("clipboard = %q after the cut, want the selection", got)
	}
	if got := h.sentKeys(t); got != "" {
		t.Errorf("cut walked a wide-glyph line and sent %q", got)
	}
}

// The reconnect handshake must tell the server the last frame seq it saw, so a
// dropped socket resumes where it left off (spec §8).
func TestClientJS_ReconnectSendsHelloWithLastSeq(t *testing.T) {
	h := newClientHarness(t)
	h.deliver(t, frameDocSeq(t, 1, "before drop", 42))
	h.call(t, "__opened")

	sent := h.sent(t)
	var hello map[string]any
	for _, m := range sent {
		if m["t"] == "hello" {
			hello = m
		}
	}
	if hello == nil {
		t.Fatalf("reconnect sent no hello: %v", sent)
	}
	if since := numOf(hello["since"]); since != 42 {
		t.Errorf("hello reported since=%v, want the last frame's seq 42", hello["since"])
	}
}

// The first connect must also send a hello (since 0 on a fresh page) so the
// handshake is uniform from the very first socket.
func TestClientJS_FirstConnectSendsHello(t *testing.T) {
	h := newClientHarness(t)
	h.call(t, "__opened")

	sent := h.sent(t)
	if len(sent) == 0 || sent[0]["t"] != "hello" {
		t.Fatalf("first connect sent %v, want a hello first", sent)
	}
	if since := numOf(sent[0]["since"]); since != 0 {
		t.Errorf("fresh page reported since=%v, want 0", sent[0]["since"])
	}
}

// numOf reads a JSON number out of an exported message. goja hands back a
// whole number as int64 and a fractional one as float64, so a test must not
// assume either.
func numOf(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case float64:
		return n
	}
	return -1
}

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

// ---------------------------------------------------------------- input helpers

// open fires the socket onopen handler, which is where the page sends its first
// resize.
func (h *clientHarness) open(t *testing.T) {
	t.Helper()
	h.call(t, "__opened")
	h.clearSent(t)
}

// clearSent drops the messages recorded so far, so a test can assert on what a
// single action produced.
func (h *clientHarness) clearSent(t *testing.T) {
	t.Helper()
	socket := h.vm.Get("__socket").ToObject(h.vm)
	_ = socket.Set("sent", h.vm.NewArray())
}

// sent returns the parsed messages the page pushed to the socket.
func (h *clientHarness) sent(t *testing.T) []map[string]any {
	t.Helper()
	raw := h.call(t, "__sent").ToObject(h.vm)
	n := len(raw.Keys())
	out := make([]map[string]any, 0, n)
	for _, k := range raw.Keys() {
		var m map[string]any
		if err := h.vm.ExportTo(raw.Get(k), &m); err != nil {
			t.Fatalf("export sent message: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// reset drops every message sent so far, so a test can assert on one gesture
// (the paste that inserted, the keys a cut sent) without the earlier ones.
func (h *clientHarness) reset(t *testing.T) {
	t.Helper()
	h.vm.RunString("window.__socket.sent.length = 0")
}

// activeElement is the stub's focused element id, or "" when nothing is focused.
func (h *clientHarness) activeElement(t *testing.T) string {
	t.Helper()
	return h.call(t, "__activeElement").String()
}

// fireTimerOf runs the first live timer scheduled with this delay. The paste
// fallback is a timer, and firing it by delay keeps the test independent of
// which other timers the page has parked.
func (h *clientHarness) fireTimerOf(t *testing.T, ms int) bool {
	t.Helper()
	return h.call(t, "__fireTimerOf", ms).ToBoolean()
}

// sentInputs returns the payload of every {t:"input"} message the page sent.
func (h *clientHarness) sentInputs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range h.sent(t) {
		if m["t"] == "input" {
			data, _ := m["data"].(string)
			out = append(out, data)
		}
	}
	return out
}

// sentInput is the single input payload the page sent, or "" when it sent none.
func (h *clientHarness) sentInput(t *testing.T) string {
	t.Helper()
	all := h.sentInputs(t)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// countInputs is how many input messages the page sent.
func (h *clientHarness) countInputs(t *testing.T) int {
	t.Helper()
	return len(h.sentInputs(t))
}

// sentKeys renders the named keys the page sent, in order ("Backspace,Delete").
// A synthesized cut is exactly this list, so it is what the tests assert on.
func (h *clientHarness) sentKeys(t *testing.T) string {
	t.Helper()
	var names []string
	for _, m := range h.sent(t) {
		if m["t"] != "key" {
			continue
		}
		kk, _ := m["key"].(map[string]any)
		key, _ := kk["key"].(string)
		names = append(names, key)
	}
	return strings.Join(names, ",")
}

// sentKey reports whether a key event for key with the given modifier was sent.
func (h *clientHarness) sentKey(t *testing.T, key, modifier string) bool {
	t.Helper()
	for _, m := range h.sent(t) {
		if m["t"] != "key" {
			continue
		}
		kk, _ := m["key"].(map[string]any)
		if kk["key"] != key {
			continue
		}
		if flag, ok := kk[modifier].(bool); ok && flag {
			return true
		}
	}
	return false
}

// clipboardText is what the page's clipboard holds after a copy or cut.
func (h *clientHarness) clipboardText(t *testing.T) string {
	t.Helper()
	return h.call(t, "__clipboardText").String()
}

// clipboardReads counts the page's clipboard reads; a copy must not read.
func (h *clientHarness) clipboardReads(t *testing.T) int {
	t.Helper()
	return int(h.call(t, "__clipboardReads").ToInteger())
}

// execCommands lists the legacy execCommand calls the page made.
func (h *clientHarness) execCommands(t *testing.T) []string {
	t.Helper()
	v := h.vm.Get("__execCommands")
	var out []string
	if err := h.vm.ExportTo(v, &out); err != nil {
		t.Fatalf("export execCommands: %v", err)
	}
	return out
}

// keydown dispatches a synthetic keydown and reports whether the page claimed
// it (preventDefault).
func (h *clientHarness) keydown(t *testing.T, ev map[string]any) bool {
	t.Helper()
	return h.call(t, "__fire", "keydown", h.toValue(ev)).ToBoolean()
}

// keyup dispatches the keyup that closes a paste chord's window — the point the
// page reads the clipboard from when the browser delivered no paste.
func (h *clientHarness) keyup(t *testing.T, ev map[string]any) {
	t.Helper()
	h.call(t, "__fire", "keyup", h.toValue(ev))
}

// paste dispatches a synthetic text paste.
func (h *clientHarness) paste(t *testing.T, text string) bool {
	t.Helper()
	ev := map[string]any{
		"clipboardData": map[string]any{
			"getData": func(kind string) string {
				if kind == "text" {
					return text
				}
				return ""
			},
			"items": []any{},
		},
	}
	return h.call(t, "__fire", "paste", h.toValue(ev)).ToBoolean()
}

// pasteImage dispatches a synthetic image paste (no text flavour).
func (h *clientHarness) pasteImage(t *testing.T, mime string) bool {
	t.Helper()
	file := map[string]any{"name": "pasted", "type": mime, "size": 3}
	ev := map[string]any{
		"clipboardData": map[string]any{
			"getData": func(string) string { return "" },
			"items": []any{map[string]any{
				"kind":      "file",
				"type":      mime,
				"getAsFile": func() any { return file },
			}},
		},
	}
	return h.call(t, "__fire", "paste", h.toValue(ev)).ToBoolean()
}

// textOf returns the rendered text of a stub element by id.
func (h *clientHarness) textOf(t *testing.T, id string) string {
	t.Helper()
	return h.text(t, h.el(t, id))
}

// stubUpload replaces fetch with a resolver returning this path, and records the
// call count.
func (h *clientHarness) stubUpload(t *testing.T, path string) {
	t.Helper()
	h.stubFetch(t, map[string]any{"ok": true, "path": path})
}

// stubUploadFailure replaces fetch with a failing response.
func (h *clientHarness) stubUploadFailure(t *testing.T, msg string) {
	t.Helper()
	h.stubFetch(t, map[string]any{"ok": false, "body": msg})
}

// stubFetch installs a JS fetch stub that counts calls and resolves to outcome.
// It is written in JS because the promise chain the page builds must run in the
// same VM — a Go-side stub cannot hand back thenables.
func (h *clientHarness) stubFetch(t *testing.T, outcome map[string]any) {
	t.Helper()
	src := `
(function (ok, body) {
  var path = body || "";
  window.__uploads = 0;
  window.fetch = function (url, opts) {
    window.__uploads++;
    return window.Promise.resolve({
      ok: ok,
      json: function () {
        return window.Promise.resolve(ok ? { path: path } : {});
      },
      text: function () { return window.Promise.resolve(body || ""); }
    });
  };
  window.__uploadCalls = function () { return window.__uploads; };
})(%v, %v);
`
	ok, _ := outcome["ok"].(bool)
	msg, _ := outcome["body"].(string)
	path, _ := outcome["path"].(string)
	if _, err := h.vm.RunString(fmt.Sprintf(src, ok, strconv.Quote(msg+path))); err != nil {
		t.Fatalf("install fetch stub: %v", err)
	}
}

// uploadCalls reports how many times the stubbed fetch was invoked.
func (h *clientHarness) uploadCalls(t *testing.T) int {
	t.Helper()
	return int(h.call(t, "__uploadCalls").ToInteger())
}

// toValue converts a Go value into a JS value for the stub call.
func (h *clientHarness) toValue(v any) goja.Value { return h.vm.ToValue(v) }

// ------------------------------------------------------- transport fallback

// stubPosts installs a fetch stub that records every POST the page makes while
// the SSE fallback is active. It is distinct from stubFetch (which serves the
// upload flow) because these tests need to inspect url + body, not a resolved
// payload.
func (h *clientHarness) stubPosts(t *testing.T) {
	t.Helper()
	src := `
  (function () {
      window.__posts = [];
      window.fetch = function (url, opts) {
        window.__posts.push({ url: url, body: (opts && opts.body) || "" });
        return window.Promise.resolve({ ok: true, json: function () { return window.Promise.resolve({}); } });
      };
      window.__postedPosts = function () { return window.__posts; };
    })();
    `
	if _, err := h.vm.RunString(src); err != nil {
		t.Fatalf("install post stub: %v", err)
	}
}

// posts returns the POSTs the page made, decoded.
func (h *clientHarness) posts(t *testing.T) []map[string]any {
	t.Helper()
	raw := h.call(t, "__postedPosts").ToObject(h.vm)
	out := make([]map[string]any, 0, len(raw.Keys()))
	for _, k := range raw.Keys() {
		var m map[string]any
		if err := h.vm.ExportTo(raw.Get(k), &m); err != nil {
			t.Fatalf("export post: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// hasEventSource reports whether the page opened an SSE stream.
func (h *clientHarness) hasEventSource(t *testing.T) bool {
	t.Helper()
	return h.call(t, "__hasEventSource").ToBoolean()
}

// failHandshake drives the stub socket through a failed upgrade (error + close
// before open) — what an upgrade-blocking proxy looks like to the page.
func (h *clientHarness) failHandshake(t *testing.T) {
	t.Helper()
	h.call(t, "__wsFailed")
}
