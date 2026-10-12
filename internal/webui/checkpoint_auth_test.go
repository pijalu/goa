// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// The G6 checkpoint, in one place: an authenticated web UI over a real
// listener, with a real engine behind it.
//
// The auth tests in auth_test.go exercise the gate through the handler. This
// file goes through a socket, because the interesting failures live at the
// seams: the WebSocket upgrade carries cookies the way a browser sends them,
// the CSP header has to arrive on the very response the page is read from, and
// the origin guard has to hold for a request that already carries a valid
// cookie — the actual CSRF shape.

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pijalu/goa/tui"
)

// authCheckpointServer starts an engine-backed, token-protected server and
// returns its base URL.
func authCheckpointServer(t *testing.T, token string) string {
	t.Helper()
	vt := NewVirtualTerminal(60, 12)
	engine := tui.NewTUI(vt)
	ed := tui.NewEditor()
	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	engine.RunLoops()
	t.Cleanup(engine.Stop)

	srv := NewServer(vt, 60, 12, ServerOptions{
		SessionID: func() string { return "sess-1" },
		Auth:      AuthConfig{Mode: AuthToken, Token: token},
		Logger:    discardLogger(),
	})
	return live(t, srv)
}

// signIn walks the real login flow with a cookie jar, exactly as a browser
// does, and returns the jar holding the session cookie.
func signIn(t *testing.T, base, token string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm(base+"/login", url.Values{"token": {token}, "next": {"/s/sess-1"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d, want 303 (body: %s)", resp.StatusCode, body)
	}
	return client
}

// CHECKPOINT: the auth cookie flow passes — over a socket, with a live engine.
func TestCheckpoint_AuthCookieFlowReachesTheLiveSession(t *testing.T) {
	base := authCheckpointServer(t, "s3cret")

	// Anonymous: the page is refused and pointed at the login form.
	resp, err := http.Get(base + "/s/sess-1")
	if err != nil {
		t.Fatalf("anonymous GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous page = %d, want a refusal", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp == "" {
		t.Error("the refused response carries no CSP")
	}

	// Signed in: the real page, with the real policy, over a real socket.
	client := signIn(t, base, "s3cret")
	resp, err = client.Get(base + "/s/sess-1")
	if err != nil {
		t.Fatalf("authenticated GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated page = %d, want 200", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP missing from the live page: %q", csp)
	}
	if !strings.Contains(string(body), `<style nonce="`) {
		t.Errorf("page does not carry a nonce:\n%s", body)
	}
	if strings.Contains(csp, "'unsafe-inline'") {
		t.Errorf("CSP has an escape hatch: %q", csp)
	}

	// The cookie alone opens the input path and the text reaches the editor.
	resp, err = client.Post(base+"/input", "application/json",
		strings.NewReader(`{"data":"hello from the authenticated browser"}`))
	if err != nil {
		t.Fatalf("authenticated POST /input: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("input = %d, want 200", resp.StatusCode)
	}
	eventually(t, "the editor to show the authenticated input", func() bool {
		return strings.Contains(pageText(t, client, base), "authenticated browser")
	})
}

// readFirstFrame reads and decodes the first frame on a socket.
func readFirstFrame(t *testing.T, conn *websocket.Conn) Frame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	decoded, err := NewFrameCodec().DecodeFrame(data)
	if err != nil {
		t.Fatalf("decode first frame: %v", err)
	}
	return *decoded
}

// dialWS opens a WebSocket, optionally with the cookies a browser would send.
func dialWS(t *testing.T, wsURL, cookie string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", cookie)
	}
	return websocket.DefaultDialer.Dial(wsURL, h)
}

// The WebSocket is the primary transport, so an unauthenticated upgrade has to
// be refused at the handshake — before any frame is exchanged.
func TestCheckpoint_WebSocketRefusesAnUnauthenticatedUpgrade(t *testing.T) {
	base := authCheckpointServer(t, "s3cret")
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/ws?s=sess-1"

	conn, resp, err := dialWS(t, wsURL, "")
	if err == nil {
		_ = conn.Close()
		t.Fatal("an unauthenticated socket was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upgrade = %v, want 401", resp)
	}
	resp.Body.Close()
}

// The cookie alone has to open the socket and carry a keystroke through it.
func TestCheckpoint_WebSocketHonoursTheAuthCookie(t *testing.T) {
	base := authCheckpointServer(t, "s3cret")
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/ws?s=sess-1"

	client := signIn(t, base, "s3cret")
	u, err := url.Parse(wsURL)
	if err != nil {
		t.Fatalf("parse ws url: %v", err)
	}
	authed, _ := cookieHeader(t, client, u)
	conn, resp, err := dialWS(t, wsURL, authed)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatalf("authenticated upgrade: %v", err)
	}
	defer conn.Close()

	// The first thing a joining client receives is the authoritative screen.
	frame := readFirstFrame(t, conn)
	if frame.Cols == 0 || frame.Rows == 0 || !frame.Full {
		t.Errorf("first frame is not a full screen: %+v", frame)
	}

	// A key sent over the authenticated socket reaches the live engine.
	if err := conn.WriteJSON(clientMsg{T: MsgKey, Key: KeyEvent{Key: "Z"}}); err != nil {
		t.Fatalf("write key: %v", err)
	}
	eventually(t, "the keystroke to reach the editor", func() bool {
		return strings.Contains(pageText(t, client, base), "Z")
	})
}

// The real CSRF shape: a valid cookie, an authenticated-looking browser, and a
// request the attacker's page initiates. The origin guard must refuse it.
func TestCheckpoint_CrossOriginWriteIsRefusedEvenWithAValidCookie(t *testing.T) {
	base := authCheckpointServer(t, "s3cret")
	client := signIn(t, base, "s3cret")

	req, err := http.NewRequest(http.MethodPost, base+"/input",
		strings.NewReader(`{"data":"csrf from evil.example"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin write = %d, want 403", resp.StatusCode)
	}
	if text := pageText(t, client, base); strings.Contains(text, "evil.example") {
		t.Errorf("cross-origin input reached the session: %q", text)
	}
}

// pageText reads the plain-text mirror of the screen with an authenticated
// client, which is the cheapest way to observe what the engine holds.
func pageText(t *testing.T, client *http.Client, base string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/s/sess-1/text", nil)
	if err != nil {
		t.Fatalf("build text request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET text: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read text: %v", err)
	}
	return string(body)
}

// cookieHeader renders the client's cookies for one URL as a Cookie header
// value, the way a browser would attach them to the WS handshake.
func cookieHeader(t *testing.T, client *http.Client, u *url.URL) (string, error) {
	t.Helper()
	u = &url.URL{Scheme: "http", Host: u.Host}
	var b strings.Builder
	for _, c := range client.Jar.Cookies(u) {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(c.Name + "=" + c.Value)
	}
	if b.Len() == 0 {
		return "", http.ErrNoCookie
	}
	return b.String(), nil
}

// The default policy must not hold an ordinary request hostage: a body the
// page legitimately sends is fine, a body past the cap is refused. The oversized
// body is valid JSON, so only the transport-level cap can turn it away.
func TestCheckpoint_BodyLimitOnTheLiveServer(t *testing.T) {
	base := authCheckpointServer(t, "s3cret")
	client := signIn(t, base, "s3cret")

	body := `{"data":"` + strings.Repeat("x", MaxRequestBytes+1024) + `"}`
	req, err := http.NewRequest(http.MethodPost, base+"/input", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("oversize body accepted with 200")
	}
}
