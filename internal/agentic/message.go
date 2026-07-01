// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// Role identifies the sender of a message in a conversation.
type Role string

const (
	// System is the system prompt role.
	System Role = "system"
	// User is the human user role.
	User Role = "user"
	// Assistant is the LLM role.
	Assistant Role = "assistant"
	// ToolRole is the role for tool result messages.
	ToolRole Role = "tool"
)

// MessageType categorizes messages flowing through the system.
type MessageType string

const (
	// Content indicates a text content message.
	Content MessageType = "content"
	// ToolCall indicates the LLM is requesting a tool execution.
	ToolCall MessageType = "tool_call"
	// End signals the completion of a conversation turn.
	End MessageType = "end"
)

// ToolCallInfo represents a single tool call within an assistant message.
// This is used when constructing the conversation history for the LLM.
type ToolCallInfo struct {
	ID        string `json:"id"`
	Type      string `json:"type"`      // Usually "function"
	Name      string `json:"name"`      // Function name
	Arguments string `json:"arguments"` // JSON-encoded arguments
}

// Message is the core unit of communication in the agentic system.
// Messages flow from the LLM through the Agent to observers, and from
// tools back to the LLM via the Session.
type Message struct {
	Type           MessageType     // Categorizes the message (content, tool_call, or end)
	Role           Role            // Identifies the sender (system, user, assistant, or tool)
	Content        string          // Text content of the message
	Thinking       string          // Captures thinking tokens from LLMs that support them
	Delta          bool            // Marks partial content/thinking chunks (true = delta, false = final)
	ToolName       string          // For ToolCall messages received from LLM
	ToolInput      string          // For ToolCall messages received from LLM
	ToolCallID     string          // ID to correlate tool calls with responses
	ToolCallIndex  int             // Index of the tool call within the tool_calls array (streaming)
	ToolCalls      []ToolCallInfo  // For assistant messages that contain tool calls (history only)
	Timings        *TokenTimings   `json:"timings,omitempty"`         // Token generation performance metrics
	PromptProgress *PromptProgress `json:"prompt_progress,omitempty"` // Prompt processing progress information

	// Images holds image attachments for multimodal user messages.
	// Each entry is a file path; the provider layer loads and encodes the
	// image when building the request.
	Images []string

	// Metadata is a set of opaque key/value strings attached to the message.
	// It is NOT sent to the LLM (json:"-"), but is propagated through the
	// agent's Output channel and to all observers (including the XML stream).
	// Clients use this to store application-level tags such as category or
	// visibility flags without affecting the model context.
	Metadata map[string]string `json:"-"`
}
