// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/tui"
)

// The G5 checkpoint, in one place: with the WebSocket blocked and with
// JavaScript disabled, the session still renders and still accepts input.
//
// These are not handler tests. A real tui.TUI with a real editor runs against a
// real VirtualTerminal behind a real HTTP listener, and the only thing that
// differs from the ordinary path is *how* the browser gets the bytes out and in:
// an event stream plus POSTs, or a server-rendered form. If any layer of the
// fallback chain were wired to the wrong callback, the editor would not hold the
// typed text and these would fail.

// submitLog records what the editor submitted. The editor's submit callback
// runs on the engine's commandLoop while the test reads from another goroutine,
// so the value is guarded rather than a bare *string the race detector would
// (rightly) flag.
type submitLog struct {
	mu sync.Mutex
	s  string
}

func (l *submitLog) set(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.s = s
}

func (l *submitLog) get() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s
}

// checkpointEngine starts a server whose terminal is driven by a real engine
// with an editor, and returns the live base URL plus a submit recorder.
//
// The children are attached before Start so the engine's first paint includes
// them, and RunLoops is what starts the render loop — without it the engine
// accepts input but never repaints, which is precisely the failure this file
// exists to catch.
func checkpointEngine(t *testing.T, cols, rows int) (string, *submitLog) {
	t.Helper()
	vt := NewVirtualTerminal(cols, rows)
	engine := tui.NewTUI(vt)

	ed := tui.NewEditor()
	engine.AddChild(ed)
	engine.SetFocus(ed)
	submitted := &submitLog{}
	ed.SetOnSubmit(submitted.set)

	if err := engine.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	engine.RunLoops()
	t.Cleanup(engine.Stop)

	srv := NewServer(vt, cols, rows, ServerOptions{
		SessionID: func() string { return "sess-1" },
		Logger:    discardLogger(),
	})
	t.Cleanup(func() { _ = srv.Close() })
	return live(t, srv), submitted
}

// eventually retries cond until it holds or the deadline passes. The fallback
// paths are asynchronous by nature (a stream, a redirect), so the assertions
// wait for the state instead of racing it.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// streamFrames drains an open event stream in the background, collecting every
// payload so a test can wait for one that carries what it wants.
func streamFrames(t *testing.T, base string) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events?s=sess-1", nil)
	if err != nil {
		cancel()
		t.Fatalf("build stream request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET /events: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})
	out := make(chan string, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				out <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out
}

// postKey sends one key descriptor the way the page does in fallback mode.
func postKey(t *testing.T, base string, ev KeyEvent) {
	t.Helper()
	body, err := json.Marshal(clientMsg{T: MsgKey, Key: ev})
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}
	resp, err := http.Post(base+"/key?s=sess-1", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /key: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /key status = %d", resp.StatusCode)
	}
}

// noFollow is an HTTP client that reports redirects instead of chasing them, so
// a test can assert on the 303 the no-JS path relies on.
func noFollow() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// getPage fetches a page and returns its body, failing the test on any error.
func getPage(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(body)
}

// The checkpoint: the WebSocket is never touched. Output arrives on the event
// stream, input arrives as POSTs, and the session stays fully drivable.
func TestCheckpoint_WebSocketBlockedStillRendersAndAcceptsInput(t *testing.T) {
	base, submitted := checkpointEngine(t, 80, 24)
	frames := streamFrames(t, base)

	// Typing goes out as one POST per key — the fallback input path.
	for _, r := range "fallback" {
		postKey(t, base, KeyEvent{Key: string(r)})
	}

	// A keystroke POST must reach the editor: submitting delivers the text.
	postKey(t, base, KeyEvent{Key: "Enter"})

	eventually(t, "the editor to receive the typed line", func() bool { return submitted.get() != "" })
	if got := submitted.get(); got != "fallback" {
		t.Errorf("submitted %q, want %q", got, "fallback")
	}

	// The engine's repaint must reach the stream: a screen update is visible to
	// a browser whose socket never opened.
	if !awaitFrame(t, frames, 3*time.Second) {
		t.Error("a blocked-socket browser received no frames over the event stream")
	}
}

// awaitFrame drains payloads until one is a full-screen frame. The stream
// carries frames and control messages on separate queues, so arrival order is
// not guaranteed: scan rather than expect the next line to be a frame.
func awaitFrame(t *testing.T, frames <-chan string, within time.Duration) bool {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case payload, ok := <-frames:
			if !ok {
				return false
			}
			if strings.Contains(payload, `"t":"frame"`) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// The other half of the checkpoint: with JavaScript off there is no
// EventSource, no fetch and no socket — only a server-rendered page and a form.
func TestCheckpoint_JavaScriptDisabledStillRendersAndAcceptsInput(t *testing.T) {
	base, submitted := checkpointEngine(t, 80, 24)

	// The page a scripting-disabled browser receives.
	body := getPage(t, base+"/s/sess-1/page")
	if strings.Contains(body, "<script") {
		t.Error("the no-JS page contains a script tag")
	}
	// It must offer a form that posts back, and poll for updates.
	if !strings.Contains(body, `action="/input`) || !strings.Contains(body, "<textarea") {
		t.Error("the no-JS page has no input form")
	}
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("the no-JS page does not poll for updates")
	}

	// Clicking "Send" posts the textarea alone: a browser posts only the button
	// it was given, so no key field means "insert, do not submit". After the 303
	// the page is re-rendered with an empty textarea, exactly as a reload would
	// leave it — so the next post carries only the key.
	const typed = "no-js line"
	post := func(data, key string) *http.Response {
		t.Helper()
		form := url.Values{"data": {data}, "s": {"sess-1"}}
		if key != "" {
			form.Set(formKeyField, key)
		}
		res, err := noFollow().PostForm(base+"/input?s=sess-1", form)
		if err != nil {
			t.Fatalf("POST /input: %v", err)
		}
		_ = res.Body.Close()
		return res
	}

	res := post(typed, "")
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("form submit status = %d, want 303 back to the page", res.StatusCode)
	}
	// The redirect target is the page itself, so the browser re-renders the
	// updated screen without any script running.
	if res.Header.Get("Location") == "" {
		t.Error("form submit did not redirect back to the page")
	}

	// The re-render must show what was typed: the form's bytes reached the
	// engine, the engine painted, and the paint came back out as HTML.
	eventually(t, "the typed line to appear on the re-rendered page", func() bool {
		return strings.Contains(getPage(t, base+"/s/sess-1/page"), typed)
	})

	// The "⏎" button submits what the engine already holds: an empty textarea
	// plus the encoded Enter, which the editor must see as a real keypress.
	post("", "Enter")
	eventually(t, "the form submit to reach the editor", func() bool { return submitted.get() != "" })
	if got := submitted.get(); got != typed {
		t.Errorf("submitted %q, want %q", got, typed)
	}
}
