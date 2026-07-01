// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// NotificationHandler receives JSON-RPC notifications (responses without an id)
// from the MCP server. It is invoked from the client's reader goroutine, so
// implementations must be non-blocking and thread-safe.
type NotificationHandler func(method string, params json.RawMessage)

// StdioClient connects to an MCP server over stdio.
//
// Reader model: a single long-lived reader goroutine, started in Initialize
// and stopped in Close, owns c.stdout. It demultiplexes incoming JSON-RPC
// frames: responses are routed to the per-id waiter channel created by call(),
// and notifications are dispatched to the configured NotificationHandler.
// This avoids the previous bug where each call() spun up a fresh
// bufio.Scanner racing on the same stdout (corrupting the protocol and losing
// notifications buffered between requests).
type StdioClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// readerWg tracks the single reader goroutine started in Initialize.
	readerWg sync.WaitGroup

	mu       sync.Mutex
	pending  map[int]chan rpcResponse // id -> waiter, created by call(), fulfilled by reader
	notifier NotificationHandler

	// sendMu serializes writes to stdin so concurrent Call() requests do not
	// interleave frames on the wire.
	sendMu sync.Mutex

	// readerCancel stops the reader goroutine's internal context (used to
	// release pending waiters on Close without interrupting the pipe read,
	// which cannot be safely interrupted portably).
	readerCancel context.CancelFunc

	id     atomic.Int32
	closed atomic.Bool
}

// NewStdioClient creates a stdio MCP client from a command and arguments.
func NewStdioClient(command string, args []string) *StdioClient {
	cmd := exec.Command(command, args...)
	return &StdioClient{cmd: cmd, pending: make(map[int]chan rpcResponse)}
}

// SetNotificationHandler registers a handler invoked for server notifications.
// Must be called before Initialize. It is safe to leave it unset.
func (c *StdioClient) SetNotificationHandler(h NotificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifier = h
}

// Initialize starts the server and performs the MCP handshake.
func (c *StdioClient) Initialize(ctx context.Context) error {
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	c.stdin = stdin
	c.stdout = stdout
	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	if err := c.startReaderLoop(); err != nil {
		return err
	}

	_, err = c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "goa", "version": "0.1"},
	})
	if err != nil {
		// Best-effort teardown of the reader on failed init.
		_ = c.Close()
		return err
	}
	return nil
}

// startReaderLoop launches the single long-lived reader goroutine that owns
// c.stdout. It is invoked by Initialize (and by tests injecting in-memory
// streams). Calling it more than once is a programming error.
func (c *StdioClient) startReaderLoop() error {
	readerCtx, readerCancel := context.WithCancel(context.Background())
	c.readerCancel = readerCancel
	c.readerWg.Add(1)
	go func() {
		defer c.readerWg.Done()
		c.readLoop(readerCtx)
	}()
	return nil
}

// ListTools returns tools from the server.
func (c *StdioClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	res, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return out.Tools, nil
}

// CallTool invokes a tool.
func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	res, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	var out mcpResult
	if err := json.Unmarshal(res, &out); err != nil {
		return "", fmt.Errorf("parse tool result: %w", err)
	}
	if out.IsError {
		return "", fmt.Errorf("tool error: %s", concatContent(out.Content))
	}
	return concatContent(out.Content), nil
}

func concatContent(contents []mcpContent) string {
	var s string
	for _, c := range contents {
		if c.Type == "text" {
			s += c.Text
		}
	}
	return s
}

// Close shuts down the client. It cancels pending waiters, closes stdin and
// stdout (closing stdout forces the reader goroutine to exit promptly rather
// than waiting for the server process to disappear), and kills the process.
func (c *StdioClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Cancel pending waiters first so Call() callers unblock promptly.
	if c.readerCancel != nil {
		c.readerCancel()
	}
	c.failAllPending(context.Canceled)
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	// Closing stdout forces the blocked bufio.Scanner read to return so the
	// reader goroutine exits without depending on the server process closing
	// its end of the pipe.
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	c.readerWg.Wait()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return nil
}

// call sends a request and waits for its response, demultiplexed by the
// reader goroutine. It holds the send lock for the duration of the request to
// preserve request/response ordering on the write side; the read side is
// fully owned by the reader goroutine.
func (c *StdioClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("client closed")
	}

	id := int(c.id.Add(1))
	wait := make(chan rpcResponse, 1)

	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return nil, fmt.Errorf("client closed")
	}
	c.pending[id] = wait
	c.mu.Unlock()

	// Ensure the waiter slot is always cleaned up.
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.sendRequest(id, method, params); err != nil {
		return nil, err
	}

	// Cancel the wait when ctx fires. context.AfterFunc returns a stop func
	// we must call to avoid keeping the waiter slot referenced after
	// delivery. We intentionally do NOT try to interrupt the pipe read.
	stop := context.AfterFunc(ctx, func() {
		c.failOne(id, rpcResponse{Error: asRPCError(ctx.Err())})
	})

	select {
	case res := <-wait:
		stop()
		if res.Error != nil {
			return nil, res.Error
		}
		return res.Result, nil
	case <-ctx.Done():
		stop()
		return nil, ctx.Err()
	}
}

func (c *StdioClient) sendRequest(id int, method string, params any) error {
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	return nil
}

// readLoop is the single owner of c.stdout for the lifetime of the client.
// It scans newline-delimited JSON-RPC frames, routes responses to their
// waiter channels, and dispatches notifications to the handler.
func (c *StdioClient) readLoop(ctx context.Context) {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Not valid JSON-RPC; skip. (Could log via handler if needed.)
			continue
		}
		// Notifications carry no id (zero value). The JSON-RPC spec uses
		// absent id; since our requests always use positive ids, treat id==0
		// as a notification.
		if resp.ID == 0 {
			c.dispatchNotification(line)
			continue
		}
		c.deliver(resp.ID, resp)
	}
	// EOF or read error: fail all pending waiters so Call() callers unblock.
	if err := scanner.Err(); err != nil && !c.closed.Load() {
		c.failAllPending(fmt.Errorf("mcp read error: %w", err))
		return
	}
	c.failAllPending(io.EOF)
}

func (c *StdioClient) deliver(id int, res rpcResponse) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- res:
	default:
		// Waiter already gone (cancelled); drop.
	}
}

func (c *StdioClient) failOne(id int, res rpcResponse) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		select {
		case ch <- res:
		default:
		}
	}
}

func (c *StdioClient) failAllPending(err error) {
	res := rpcResponse{Error: asRPCError(err)}
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int]chan rpcResponse)
	c.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- res:
		default:
		}
	}
}

// dispatchNotification parses a notification frame and forwards it to the
// configured NotificationHandler (if any).
func (c *StdioClient) dispatchNotification(frame []byte) {
	c.mu.Lock()
	h := c.notifier
	c.mu.Unlock()
	if h == nil {
		return
	}
	var notif struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(frame, &notif); err != nil {
		return
	}
	h(notif.Method, notif.Params)
}
