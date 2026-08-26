// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestClient_WriteMessage(t *testing.T) {
	w := &bytes.Buffer{}
	client := NewClient(&fakeConn{Reader: &bytes.Buffer{}, Writer: w})
	defer client.Close()

	body := []byte(`{"jsonrpc":"2.0","method":"initialized"}`)
	if err := client.writeMessage(body); err != nil {
		t.Fatalf("writeMessage failed: %v", err)
	}
	want := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)) + string(body)
	if got := w.String(); got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

func TestClient_DispatchResponse(t *testing.T) {
	client := NewClient(&fakeConn{})
	defer client.Close()

	ch := make(chan *rpcResponse, 1)
	client.mu.Lock()
	client.pending[1] = ch
	client.mu.Unlock()

	client.dispatch(&rpcMessage{JSONRPC: "2.0", ID: 1, Result: json.RawMessage(`"ok"`)})
	resp := <-ch
	if resp == nil {
		t.Fatal("expected response")
	}
	if string(resp.Result) != `"ok"` {
		t.Errorf("unexpected result %s", resp.Result)
	}
}

func TestClient_DispatchNotification(t *testing.T) {
	client := NewClient(&fakeConn{})
	defer client.Close()

	called := false
	client.OnNotification("$/test", func(params json.RawMessage) {
		called = true
	})

	client.dispatch(&rpcMessage{JSONRPC: "2.0", Method: "$/test"})
	if !called {
		t.Error("expected notification handler to be called")
	}
}

func TestClient_IsClosed(t *testing.T) {
	client := NewClient(&fakeConn{})
	if client.IsClosed() {
		t.Error("new client should not be closed")
	}
	client.Close()
	if !client.IsClosed() {
		t.Error("closed client should report closed")
	}
}

type fakeConn struct {
	Reader io.Reader
	Writer io.Writer
	closed bool
}

func (f *fakeConn) Read(p []byte) (int, error) {
	if f.Reader == nil {
		return 0, io.EOF
	}
	return f.Reader.Read(p)
}

func (f *fakeConn) Write(p []byte) (int, error) {
	if f.Writer == nil {
		return len(p), nil
	}
	return f.Writer.Write(p)
}

func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func (f *fakeConn) LocalAddr() net.Addr                { return nil }
func (f *fakeConn) RemoteAddr() net.Addr               { return nil }
func (f *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

func TestClient_ReadMessage(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":"ok"}`
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	client := NewClient(&fakeConn{Reader: bytes.NewBufferString(msg), Writer: &bytes.Buffer{}})
	defer client.Close()

	got, err := client.readMessage()
	if err != nil {
		t.Fatalf("readMessage failed: %v", err)
	}
	if got.ID != 1 {
		t.Errorf("id = %d, want 1", got.ID)
	}
	if string(got.Result) != `"ok"` {
		t.Errorf("result = %s, want ok", got.Result)
	}
}

func TestClient_ReadMessage_NoHeader(t *testing.T) {
	client := NewClient(&fakeConn{Reader: bytes.NewBufferString("not-a-header"), Writer: &bytes.Buffer{}})
	defer client.Close()
	_, err := client.readMessage()
	if err == nil {
		t.Error("expected error for invalid header")
	}
}

func TestClient_Initialize(t *testing.T) {
	// client reads from serverOut, writes to serverIn
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	client := NewClient(&fakeConn{Reader: clientIn, Writer: clientOut})
	defer client.Close()

	go func() {
		_ = client.ReadNotifications(context.Background())
	}()

	go func() {
		defer serverOut.Close()
		// Read the initialize request header + body.
		tp := textproto.NewReader(bufio.NewReader(serverIn))
		headers, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		length := 0
		if vals := headers.Values("Content-Length"); len(vals) > 0 {
			fmt.Sscanf(vals[0], "%d", &length)
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(serverIn, body); err != nil {
			return
		}
		// Respond to the request.
		resp := `{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"definitionProvider":true}}}`
		fmt.Fprintf(serverOut, "Content-Length: %d\r\n\r\n%s", len(resp), resp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := client.Initialize(ctx, InitializeParams{RootURI: "file:///tmp"})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if enabled, _ := res.Capabilities.DefinitionProvider.(bool); !enabled {
		t.Errorf("expected definitionProvider capability, got %v", res.Capabilities.DefinitionProvider)
	}
}

// serveOneInitializeResponse answers the next initialize request on the pipe
// pair with the given JSON result payload. Used by the capability-shape tests.
func serveOneInitializeResponse(serverIn io.Reader, serverOut io.Writer, resultJSON string) {
	reader := bufio.NewReader(serverIn)
	var length int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if strings.HasPrefix(line, "Content-Length:") {
			fmt.Sscanf(line, "Content-Length: %d", &length)
		}
		if line == "\r\n" {
			break
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(serverIn, body); err != nil {
		return
	}
	fmt.Fprintf(serverOut, "Content-Length: %d\r\n\r\n%s", len(resultJSON), resultJSON)
}

// newPipedClient wires a client to a fake server through paired pipes. The
// returned reader/writer are the server side of the channel; closing is bound
// to the test lifetime.
func newPipedClient(t *testing.T) (*Client, io.Reader, io.Writer) {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	client := NewClient(&fakeConn{Reader: clientIn, Writer: clientOut})
	t.Cleanup(func() { _ = client.Close() })
	go client.ReadNotifications(context.Background())
	return client, serverIn, serverOut
}

// TestClient_Initialize_ObjectProviderCapabilities reproduces pyright's
// initialize response: provider flags arrive as OBJECTS
// ({"workDoneProgress":true}), which the LSP spec allows
// (boolean | ProviderOptions). The strict-bool unmarshal used to fail the
// whole handshake, breaking pyright spawns (Issue LSP).
func TestClient_Initialize_ObjectProviderCapabilities(t *testing.T) {
	client, serverIn, serverOut := newPipedClient(t)

	go serveOneInitializeResponse(serverIn, serverOut,
		`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"definitionProvider":{"workDoneProgress":true},"hoverProvider":true}}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := client.Initialize(ctx, InitializeParams{RootURI: "file:///tmp"})
	if err != nil {
		t.Fatalf("initialize with object provider capabilities failed: %v", err)
	}
	if res.Capabilities.DefinitionProvider == nil || res.Capabilities.HoverProvider == nil {
		t.Errorf("expected provider capabilities, got %+v", res.Capabilities)
	}
}

func TestClient_NavigationRequests(t *testing.T) {
	client, serverIn, serverOut := newPipedClient(t)
	go serveNavigationResponses(serverIn, serverOut)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pos := navigationPosition()
	checkNavigationBasics(t, ctx, client, pos)
	checkCallHierarchyRoundtrip(t, ctx, client, pos)
}

// navigationPosition is the shared cursor position used by the navigation
// round-trips against file:///main.js.
func navigationPosition() TextDocumentPositionParams {
	return TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.js"},
		Position:     Position{Line: 1, Character: 1},
	}
}

// checkNavigationBasics exercises implementation lookup and workspace symbol
// queries against the canned single-item responses.
func checkNavigationBasics(t *testing.T, ctx context.Context, client *Client, pos TextDocumentPositionParams) {
	t.Helper()
	if got, err := client.Implementation(ctx, pos); err != nil || len(got) != 1 {
		t.Fatalf("implementation: %v %#v", err, got)
	}
	if got, err := client.WorkspaceSymbol(ctx, WorkspaceSymbolParams{Query: "Thing"}); err != nil || len(got) != 1 || got[0].Name != "Thing" {
		t.Fatalf("workspace symbol: %v %#v", err, got)
	}
}

// checkCallHierarchyRoundtrip prepares a hierarchy item from pos, then
// resolves its incoming ("caller") and outgoing ("callee") calls.
func checkCallHierarchyRoundtrip(t *testing.T, ctx context.Context, client *Client, pos TextDocumentPositionParams) {
	t.Helper()
	items, err := client.PrepareCallHierarchy(ctx, CallHierarchyPrepareParams{TextDocumentPositionParams: pos})
	if err != nil || len(items) != 1 {
		t.Fatalf("prepare hierarchy: %v %#v", err, items)
	}
	if got, err := client.IncomingCalls(ctx, items[0]); err != nil || len(got) != 1 || got[0].From.Name != "caller" {
		t.Fatalf("incoming calls: %v %#v", err, got)
	}
	if got, err := client.OutgoingCalls(ctx, items[0]); err != nil || len(got) != 1 || got[0].To.Name != "callee" {
		t.Fatalf("outgoing calls: %v %#v", err, got)
	}
}

func serveNavigationResponses(serverIn io.Reader, serverOut io.Writer) {
	responses := map[string]string{
		"textDocument/implementation":       `[{"uri":"file:///impl.js","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":3}}}]`,
		"workspace/symbol":                  `[{"name":"Thing","kind":12,"location":{"uri":"file:///thing.js","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":5}}}}]`,
		"textDocument/prepareCallHierarchy": `[{"name":"main","kind":12,"uri":"file:///main.js","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":4}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":4}}}]`,
		"callHierarchy/incomingCalls":       `[{"from":{"name":"caller","kind":12,"uri":"file:///caller.js","range":{"start":{"line":3,"character":0},"end":{"line":3,"character":4}},"selectionRange":{"start":{"line":3,"character":0},"end":{"line":3,"character":4}}},"fromRanges":[]}]`,
		"callHierarchy/outgoingCalls":       `[{"to":{"name":"callee","kind":12,"uri":"file:///callee.js","range":{"start":{"line":4,"character":0},"end":{"line":4,"character":4}},"selectionRange":{"start":{"line":4,"character":0},"end":{"line":4,"character":4}}},"fromRanges":[]}]`,
	}
	reader := bufio.NewReader(serverIn)
	for id := int64(1); id <= 5; id++ {
		headers, err := textproto.NewReader(reader).ReadMIMEHeader()
		if err != nil {
			return
		}
		var n int
		fmt.Sscanf(headers.Get("Content-Length"), "%d", &n)
		body := make([]byte, n)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}
		var req rpcMessage
		if json.Unmarshal(body, &req) != nil {
			return
		}
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, responses[req.Method])
		fmt.Fprintf(serverOut, "Content-Length: %d\r\n\r\n%s", len(resp), resp)
	}
}

func TestClient_Initialized(t *testing.T) {
	writer := &bytes.Buffer{}
	client := NewClient(&fakeConn{Reader: &bytes.Buffer{}, Writer: writer})
	defer client.Close()
	if err := client.Initialized(InitializedParams{}); err != nil {
		t.Fatalf("initialized failed: %v", err)
	}
	if !bytes.Contains(writer.Bytes(), []byte(`"method":"initialized"`)) {
		t.Errorf("expected initialized notification, got %q", writer.String())
	}
}

func TestClient_DidOpen(t *testing.T) {
	writer := &bytes.Buffer{}
	client := NewClient(&fakeConn{Reader: &bytes.Buffer{}, Writer: writer})
	defer client.Close()
	err := client.DidOpen(DidOpenTextDocumentParams{TextDocument: TextDocumentItem{URI: "file:///tmp/main.go", LanguageID: "go", Version: 1, Text: "package main"}})
	if err != nil {
		t.Fatalf("didOpen failed: %v", err)
	}
	if !bytes.Contains(writer.Bytes(), []byte(`"method":"textDocument/didOpen"`)) {
		t.Errorf("expected didOpen notification, got %q", writer.String())
	}
}

type errWriter struct{ err error }

func (e errWriter) Write(p []byte) (int, error) { return 0, e.err }

func TestClient_writeMessageError(t *testing.T) {
	client := NewClient(&fakeConn{Reader: &bytes.Buffer{}, Writer: errWriter{err: fmt.Errorf("boom")}})
	defer client.Close()
	if err := client.writeMessage([]byte("{}")); err == nil {
		t.Error("expected write error")
	}
}

func TestClient_notifyMarshalError(t *testing.T) {
	client := NewClient(&fakeConn{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}})
	defer client.Close()
	if err := client.notify("bad", make(chan int)); err == nil {
		t.Error("expected marshal error")
	}
}

func TestClient_requestMarshalError(t *testing.T) {
	client := NewClient(&fakeConn{Reader: &bytes.Buffer{}, Writer: &bytes.Buffer{}})
	defer client.Close()
	if err := client.request(context.Background(), "bad", make(chan int), nil); err == nil {
		t.Error("expected marshal error")
	}
}

func TestPipeConn_NetMethods(t *testing.T) {
	conn := &pipeConn{}
	if conn.LocalAddr() != nil || conn.RemoteAddr() != nil {
		t.Error("expected nil addresses")
	}
	if err := conn.SetDeadline(time.Now()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_requestErrorResponse(t *testing.T) {
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	client := NewClient(&fakeConn{Reader: clientIn, Writer: clientOut})
	defer client.Close()

	go func() {
		_ = client.ReadNotifications(context.Background())
	}()

	go func() {
		defer serverOut.Close()
		tp := textproto.NewReader(bufio.NewReader(serverIn))
		headers, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		length := 0
		if vals := headers.Values("Content-Length"); len(vals) > 0 {
			fmt.Sscanf(vals[0], "%d", &length)
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(serverIn, body); err != nil {
			return
		}
		resp := `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"fail"}}`
		fmt.Fprintf(serverOut, "Content-Length: %d\r\n\r\n%s", len(resp), resp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result string
	err := client.request(ctx, "test", nil, &result)
	if err == nil {
		t.Fatal("expected error response")
	}
	if err.Error() != "lsp: fail (code -1)" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_ReadMessage_InvalidLength(t *testing.T) {
	msg := "Content-Length: abc\r\n\r\n{}"
	client := NewClient(&fakeConn{Reader: bytes.NewBufferString(msg), Writer: &bytes.Buffer{}})
	defer client.Close()
	_, err := client.readMessage()
	if err == nil {
		t.Error("expected error for invalid Content-Length")
	}
}

func TestPipeConn_SetReadWriteDeadline(t *testing.T) {
	conn := &pipeConn{}
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := conn.SetWriteDeadline(time.Now()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
