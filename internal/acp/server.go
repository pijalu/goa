// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// MethodHandler handles a single ACP method call.
type MethodHandler func(conn ServerConn, params json.RawMessage) (interface{}, *RPCError)

// ServerConn is the interface the server uses to communicate with the client.
type ServerConn interface {
	SendResponse(id json.RawMessage, result interface{})
	SendError(id json.RawMessage, err *RPCError)
	SendNotification(method string, params interface{})
	SessionID() string // current session ID
}

// ACPServer is the JSON-RPC 2.0 dispatcher for ACP.
type ACPServer struct {
	mu            sync.Mutex
	handlers      map[string]MethodHandler
	sessions      map[string]*ACPSession
	conn          *stdioConn
	reader        *bufio.Scanner
	done          chan struct{}
	driverFactory func(sessionID string) AgentDriver
}

// stdioConn implements ServerConn over stdin/stdout.
type stdioConn struct {
	writer  io.Writer
	encoder *json.Encoder
	mu      sync.Mutex
	sid     string
}

func newStdioConn(w io.Writer) *stdioConn {
	return &stdioConn{
		writer:  w,
		encoder: json.NewEncoder(w),
	}
}

func (c *stdioConn) SendResponse(id json.RawMessage, result interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var resultData json.RawMessage
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			c.sendRaw(JSONRPCResponse{
				JSONRPC: "2.0", ID: id,
				Error: &RPCError{Code: ErrInternal, Message: fmt.Sprintf("marshal error: %v", err)},
			})
			return
		}
		resultData = data
	}

	c.sendRaw(JSONRPCResponse{
		JSONRPC: "2.0", ID: id, Result: resultData,
	})
}

func (c *stdioConn) SendError(id json.RawMessage, err *RPCError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sendRaw(JSONRPCResponse{
		JSONRPC: "2.0", ID: id, Error: err,
	})
}

func (c *stdioConn) SendNotification(method string, params interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var paramsData json.RawMessage
	if params != nil {
		data, _ := json.Marshal(params)
		paramsData = data
	}

	c.sendRaw(JSONRPCNotification{
		JSONRPC: "2.0", Method: method, Params: paramsData,
	})
}

func (c *stdioConn) sendRaw(v interface{}) {
	c.encoder.Encode(v)
}

func (c *stdioConn) SessionID() string       { return c.sid }
func (c *stdioConn) setSessionID(sid string) { c.sid = sid }

// NewACPServer creates a new ACP server reading from r and writing to w.
func NewACPServer(r io.Reader, w io.Writer) *ACPServer {
	return NewACPServerWithDriver(r, w, nil)
}

// NewACPServerWithDriver creates an ACP server backed by a real agent driver.
func NewACPServerWithDriver(r io.Reader, w io.Writer, factory func(sessionID string) AgentDriver) *ACPServer {
	conn := newStdioConn(w)
	s := &ACPServer{
		handlers:      make(map[string]MethodHandler),
		sessions:      make(map[string]*ACPSession),
		conn:          conn,
		reader:        bufio.NewScanner(r),
		done:          make(chan struct{}),
		driverFactory: factory,
	}
	s.registerHandlers()
	return s
}

func (s *ACPServer) registerHandlers() {
	s.handlers["initialize"] = s.handleInitialize
	s.handlers["session/new"] = s.handleSessionNew
	s.handlers["session/prompt"] = s.handleSessionPrompt
	s.handlers["session/cancel"] = s.handleSessionCancel
	s.handlers["session/list"] = s.handleSessionList
	s.handlers["session/load"] = s.handleSessionLoad
}

// Done returns a channel that's closed when the server stops.
func (s *ACPServer) Done() <-chan struct{} { return s.done }

// Start reads JSON-RPC requests from stdin and dispatches them.
func (s *ACPServer) Start() {
	defer close(s.done)

	s.reader.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for s.reader.Scan() {
		line := strings.TrimSpace(s.reader.Text())
		if line == "" {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.conn.SendError(nil, &RPCError{Code: ErrParse, Message: "invalid JSON"})
			continue
		}

		s.dispatch(req)
	}

	if err := s.reader.Err(); err != nil {
		// Stdin closed — clean exit.
	}
}

func (s *ACPServer) dispatch(req JSONRPCRequest) {
	handler, ok := s.handlers[req.Method]
	if !ok {
		s.conn.SendError(req.ID, &RPCError{
			Code: ErrMethodNotFound, Message: fmt.Sprintf("unknown method: %s", req.Method),
		})
		return
	}

	result, rpcErr := handler(s.conn, req.Params)
	if rpcErr != nil {
		s.conn.SendError(req.ID, rpcErr)
		return
	}
	s.conn.SendResponse(req.ID, result)
}

// ── method handlers ───────────────────────────────────────────────

func (s *ACPServer) handleInitialize(conn ServerConn, params json.RawMessage) (interface{}, *RPCError) {
	return InitializeResult{
		AgentInfo: AgentInfo{Name: "goa", Version: "1.0.0"},
		AgentCapabilities: AgentCapabilities{
			PromptCapabilities: PromptCapabilities{
				Image:   true,
				Audio:   false,
				Context: true,
			},
			MCapabilities: MCapabilities{HTTP: true, SSE: true},
			SessionCapabilities: SessionCapabilities{
				List: struct{}{},
			},
			FSCapabilities: FSCapabilities{Read: false, Write: false},
			LoadSession:    true,
		},
	}, nil
}

func (s *ACPServer) handleSessionNew(conn ServerConn, params json.RawMessage) (interface{}, *RPCError) {
	s.mu.Lock()
	id := fmt.Sprintf("acp-%d", len(s.sessions)+1)
	var driver AgentDriver
	if s.driverFactory != nil {
		driver = s.driverFactory(id)
	}
	session := NewACPSession(id, conn, driver)
	s.sessions[id] = session
	s.mu.Unlock()

	if err := session.Start(); err != nil {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		return nil, &RPCError{Code: ErrInternal, Message: fmt.Sprintf("failed to start session: %v", err)}
	}

	conn.(*stdioConn).setSessionID(id)
	return NewSessionResult{SessionID: id}, nil
}

func (s *ACPServer) handleSessionPrompt(conn ServerConn, params json.RawMessage) (interface{}, *RPCError) {
	var p PromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("invalid prompt params: %v", err)}
	}

	s.mu.Lock()
	session := s.sessions[p.SessionID]
	s.mu.Unlock()

	if session == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("session not found: %s", p.SessionID)}
	}

	text := extractText(p.Content)
	session.ProcessPrompt(text)

	return struct{}{}, nil
}

func (s *ACPServer) handleSessionCancel(conn ServerConn, params json.RawMessage) (interface{}, *RPCError) {
	// Extract session ID from params.
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "sessionId required"}
	}

	s.mu.Lock()
	session := s.sessions[p.SessionID]
	s.mu.Unlock()

	if session != nil {
		session.Cancel()
	}
	return struct{}{}, nil
}

func (s *ACPServer) handleSessionList(conn ServerConn, _ json.RawMessage) (interface{}, *RPCError) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	return map[string]any{
		"sessions": ids,
	}, nil
}

func (s *ACPServer) handleSessionLoad(conn ServerConn, params json.RawMessage) (interface{}, *RPCError) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "sessionId required"}
	}

	s.mu.Lock()
	session := s.sessions[p.SessionID]
	s.mu.Unlock()

	if session == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("session not found: %s", p.SessionID)}
	}

	return map[string]any{"sessionId": session.ID}, nil
}

// extractText extracts text from ACP content blocks.
func extractText(content []ContentBlock) string {
	var parts []string
	for _, block := range content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "resource":
			if block.Resource != nil && block.Resource.Text != "" {
				parts = append(parts, block.Resource.Text)
			} else if block.Resource != nil {
				parts = append(parts, fmt.Sprintf("<resource uri=%q>", block.Resource.URI))
			}
		}
	}
	return strings.Join(parts, "\n")
}
