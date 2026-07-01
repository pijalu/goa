// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package acp implements the Agent Client Protocol (ACP) — a JSON-RPC 2.0
// protocol over stdin/stdout that allows ACP-compatible IDEs (Zed, JetBrains,
// VS Code) to drive Goa sessions.
//
// The ACP adapter implements the server side of the protocol. It receives
// JSON-RPC requests on stdin and sends responses + notifications on stdout.
package acp

import "encoding/json"

// ---------------------------------------------------------------------------
// JSON-RPC types
// ---------------------------------------------------------------------------

// JSONRPCRequest is a JSON-RPC 2.0 request from the client.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response to the client.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// JSONRPCNotification is a JSON-RPC 2.0 notification (no ID) sent server→client.
type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPC error codes.
const (
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
	ErrAuthRequired   = -32000
)

// ---------------------------------------------------------------------------
// ACP Initialize
// ---------------------------------------------------------------------------

// InitializeParams is sent by the client on startup.
type InitializeParams struct {
	ClientVersion string `json:"clientVersion"`
}

// InitializeResult is returned by the server.
type InitializeResult struct {
	AgentInfo         AgentInfo         `json:"agentInfo"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
}

// AgentInfo identifies the ACP server.
type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// AgentCapabilities advertises what the server supports.
type AgentCapabilities struct {
	PromptCapabilities  PromptCapabilities  `json:"promptCapabilities"`
	MCapabilities       MCapabilities       `json:"mcpCapabilities"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
	FSCapabilities      FSCapabilities      `json:"fsCapabilities"`
	LoadSession         bool                `json:"loadSession"`
}

// PromptCapabilities describes prompt content block support.
type PromptCapabilities struct {
	Image   bool `json:"image"`
	Audio   bool `json:"audio"`
	Context bool `json:"embeddedContext"`
}

// MCapabilities describes MCP forwarding support.
type MCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

// SessionCapabilities describes session management support.
type SessionCapabilities struct {
	List interface{} `json:"list"` // {} = supported, omit for unsupported
}

// FSCapabilities describes file system reverse-RPC support.
type FSCapabilities struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
}

// ---------------------------------------------------------------------------
// ACP Session
// ---------------------------------------------------------------------------

// NewSessionParams is sent by the client to create a session.
type NewSessionParams struct {
	Cwd string `json:"cwd"`
}

// NewSessionResult is returned after creating a session.
type NewSessionResult struct {
	SessionID string `json:"sessionId"`
}

// PromptParams is sent by the client to send a prompt.
type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Content   []ContentBlock `json:"content"`
}

// ContentBlock is a single content block in a prompt.
type ContentBlock struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	Image    *ACPImage    `json:"image,omitempty"`
	Resource *ACPResource `json:"resource,omitempty"`
}

// ACPImage carries image content.
type ACPImage struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// ACPResource carries embedded context.
type ACPResource struct {
	URI  string `json:"uri"`
	Text string `json:"text,omitempty"`
}

// ---------------------------------------------------------------------------
// ACP Events (server → client notifications)
// ---------------------------------------------------------------------------

// SessionUpdate is sent to stream output to the client.
type SessionUpdate struct {
	SessionID string       `json:"sessionId"`
	Event     SessionEvent `json:"event"`
}

// SessionEvent is one event in a session update.
type SessionEvent struct {
	Type         string           `json:"type"`
	Content      string           `json:"content,omitempty"`
	ToolCall     *ToolCallEvent   `json:"toolCall,omitempty"`
	ToolResult   *ToolResultEvent `json:"toolResult,omitempty"`
	FinishReason string           `json:"finishReason,omitempty"`
}

// ToolCallEvent describes a tool call from the LLM.
type ToolCallEvent struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
}

// ToolResultEvent describes a tool result.
type ToolResultEvent struct {
	ToolCallID string `json:"toolCallId"`
	Content    string `json:"content"`
}
