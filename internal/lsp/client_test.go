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

func (f *fakeConn) LocalAddr() net.Addr  { return nil }
func (f *fakeConn) RemoteAddr() net.Addr { return nil }
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
	if !res.Capabilities.DefinitionProvider {
		t.Error("expected definitionProvider capability")
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

	err := client.DidOpen(DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        "file:///tmp/main.go",
			LanguageID: "go",
			Version:    1,
			Text:       "package main",
		},
	})
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
