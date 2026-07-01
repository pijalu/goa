// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"encoding/json"
	"fmt"
)

// ToolHandler is a function that handles tool execution.
type ToolHandler func(args json.RawMessage) (string, error)

// MCPServer handles JSON-RPC 2.0 requests for MCP protocol.
type MCPServer struct {
	tools    map[string]ToolSchema
	handlers map[string]ToolHandler
}

// NewMCPServer creates a new MCPServer.
func NewMCPServer(registry interface{}) *MCPServer {
	return &MCPServer{
		tools:    make(map[string]ToolSchema),
		handlers: make(map[string]ToolHandler),
	}
}

// RegisterTool registers a tool with its schema and handler.
func (s *MCPServer) RegisterTool(name string, schema ToolSchema, handler ToolHandler) {
	s.tools[name] = schema
	s.handlers[name] = handler
}

// HandleRequest processes a JSON-RPC 2.0 request.
func (s *MCPServer) HandleRequest(req JSONRPCRequest) JSONRPCResponse {
	// Validate JSON-RPC version
	if req.JSONRPC != "2.0" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   NewParseError("Invalid JSON-RPC version"),
			ID:      req.ID,
		}
	}

	// Route to handler
	switch req.Method {
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   NewMethodNotFoundError(req.Method),
			ID:      req.ID,
		}
	}
}

func (s *MCPServer) handleToolsList(req JSONRPCRequest) JSONRPCResponse {
	tools := make([]ToolSchema, 0, len(s.tools))
	for _, schema := range s.tools {
		tools = append(tools, schema)
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		Result: ToolsListResult{
			Tools: tools,
		},
		ID: req.ID,
	}
}

func (s *MCPServer) handleToolsCall(req JSONRPCRequest) JSONRPCResponse {
	// Parse params
	var params ToolsCallRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError(err.Error()),
			ID:      req.ID,
		}
	}

	// Validate params
	if params.Name == "" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("missing 'name' parameter"),
			ID:      req.ID,
		}
	}

	// Get handler
	handler, ok := s.handlers[params.Name]
	if !ok {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   NewRPCError(-32602, fmt.Sprintf("tool not found: %s", params.Name)),
			ID:      req.ID,
		}
	}

	// Execute tool
	result, err := handler(params.Args)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		Result: ToolsCallResult{
			Content: []ContentBlock{
				{Type: "text", Text: result},
			},
		},
		ID: req.ID,
	}
}

// HandleHTTP processes an HTTP request containing JSON-RPC.
func (s *MCPServer) HandleHTTP(body []byte) ([]byte, error) {
	// Try to parse as array of requests
	var reqs []JSONRPCRequest
	if err := json.Unmarshal(body, &reqs); err != nil {
		// Try single request
		var req JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   NewParseError(err.Error()),
			}
			return json.Marshal(resp)
		}
		resp := s.HandleRequest(req)
		return json.Marshal(resp)
	}

	// Handle batch
	resps := make([]JSONRPCResponse, len(reqs))
	for i, req := range reqs {
		resps[i] = s.HandleRequest(req)
	}

	return json.Marshal(resps)
}

// toolsMap is a simple getter for testing
func (s *MCPServer) toolsMap() map[string]ToolSchema {
	return s.tools
}
