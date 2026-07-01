// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import "encoding/json"

// JSONRPCRequest represents a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error.
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// JSON-RPC error codes
const (
	ParseErrorCode     = -32700
	InvalidRequestCode = -32600
	MethodNotFoundCode = -32601
	InvalidParamsCode  = -32602
	InternalErrorCode  = -32603
)

// NewRPCError creates a new RPCError with the given code and message.
func NewRPCError(code int, message string) *RPCError {
	return &RPCError{Code: code, Message: message}
}

// NewParseError creates a parse error.
func NewParseError(message string) *RPCError {
	return NewRPCError(ParseErrorCode, message)
}

// NewInvalidRequestError creates an invalid request error.
func NewInvalidRequestError(message string) *RPCError {
	return NewRPCError(InvalidRequestCode, message)
}

// NewMethodNotFoundError creates a method not found error.
func NewMethodNotFoundError(method string) *RPCError {
	return NewRPCError(MethodNotFoundCode, "Method not found: "+method)
}

// NewInvalidParamsError creates an invalid params error.
func NewInvalidParamsError(message string) *RPCError {
	return NewRPCError(InvalidParamsCode, message)
}

// NewInternalError creates an internal error.
func NewInternalError(message string) *RPCError {
	return NewRPCError(InternalErrorCode, message)
}

// ToolsListRequest represents a tools/list request.
type ToolsListRequest struct{}

// ToolsListResult represents the result of tools/list.
type ToolsListResult struct {
	Tools []ToolSchema `json:"tools"`
}

// ToolSchema represents a tool's schema for MCP.
type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ToolsCallRequest represents a tools/call request.
type ToolsCallRequest struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ToolsCallResult represents the result of tools/call.
type ToolsCallResult struct {
	Content []ContentBlock `json:"content"`
}

// ContentBlock represents a content block in MCP response.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
