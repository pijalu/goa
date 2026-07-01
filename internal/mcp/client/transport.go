// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// asRPCError converts a Go error into an *rpcError suitable for delivery to
// pending waiters. context cancellation is mapped to the JSON-RPC
// "request cancelled" code (-32800) so callers can distinguish it from server
// errors.
func asRPCError(err error) *rpcError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return &rpcError{Code: -32800, Message: "request cancelled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &rpcError{Code: -32800, Message: "request timed out"}
	}
	var rerr *rpcError
	if errors.As(err, &rerr) {
		return rerr
	}
	return &rpcError{Code: -32603, Message: err.Error()}
}

type mcpResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
}
