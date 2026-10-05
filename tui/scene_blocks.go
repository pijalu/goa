// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// SceneBlock is the protocol-free description of one conversational block:
// the semantic payload a web client renders as HTML flow content, in the
// same spirit as MessageData (the Model) but carrying the few view-derived
// facts a non-terminal renderer needs (tool status, collapse state, header
// art). It is produced by components during buildScene and consumed by the
// web UI's block plane (specs/webui.md §22); the compositor itself ignores
// it.
//
// Text is width-independent source content (markdown for assistant/agent
// messages, plain text elsewhere, raw tool output for tool blocks), so a
// client can re-layout it at any width without asking the server. Lines is
// the exception: pre-styled art (the header mascot/logo) that only makes
// sense at the width it was rendered at, converted to styled spans by the
// client and re-shipped on width changes like any other dirty block.
type SceneBlock struct {
	// ID is stable for the lifetime of one conversation entry (the
	// Conversation's own message id; the header uses HeaderBlockID). A
	// client keys its DOM on it.
	ID int
	// Kind selects the client-side rendering.
	Kind SceneBlockKind
	// Text is the width-independent source content.
	Text string
	// Meta carries kind-specific facts (tool name/args/status/duration,
	// collapse state, agent labels). Optional.
	Meta map[string]string
	// Lines holds pre-styled art lines (Kind == BlockHeader). Nil for
	// text blocks.
	Lines []string
}

// SceneBlockKind classifies how a client renders a SceneBlock.
type SceneBlockKind string

const (
	// BlockHeader is the startup art (mascot + logo + info lines).
	BlockHeader SceneBlockKind = "header"
	// BlockUser is a verbatim user message.
	BlockUser SceneBlockKind = "user"
	// BlockAssistant is an assistant message whose Text is markdown.
	BlockAssistant SceneBlockKind = "assistant"
	// BlockAgent is a sub-agent message (multi-agent workflows); Meta["agent"]
	// names the agent.
	BlockAgent SceneBlockKind = "agent"
	// BlockSystem is a system notice (boxed in the TUI).
	BlockSystem SceneBlockKind = "system"
	// BlockInfo is a plain informational line.
	BlockInfo SceneBlockKind = "info"
	// BlockThinking is a reasoning block (collapsible; Meta["expanded"]).
	BlockThinking SceneBlockKind = "thinking"
	// BlockTool is a tool execution (Meta: tool, args, status, duration,
	// expanded; Text is the output so far).
	BlockTool SceneBlockKind = "tool"
	// BlockResult is a tool result echo.
	BlockResult SceneBlockKind = "result"
	// BlockCompanion is companion output (Meta may refine the gutter kind).
	BlockCompanion SceneBlockKind = "companion"
)

// HeaderBlockID is the reserved block id of the header art. Conversation
// entry ids start at 1 (Conversation.Append), so 0 can never collide.
const HeaderBlockID = 0

// BlockExporter is implemented by components that can describe their content
// as SceneBlocks. buildScene asks the transcript viewport (and only it —
// everything else in the transcript plane is chrome or header) for blocks.
type BlockExporter interface {
	ExportBlocks() []SceneBlock
}

// blockKindFor maps a conversation entry type to its block kind.
func blockKindFor(t ConsoleItemType) SceneBlockKind {
	switch t {
	case ConsoleUserMessage:
		return BlockUser
	case ConsoleAssistantMessage:
		return BlockAssistant
	case ConsoleAgentMessage:
		return BlockAgent
	case ConsoleSystemMessage:
		return BlockSystem
	case ConsoleInfoMessage:
		return BlockInfo
	case ConsoleThinkingBlock, ConsoleCompanionThinkingBlock:
		return BlockThinking
	case ConsoleToolCall:
		return BlockTool
	case ConsoleToolResult, ConsoleFinishLine:
		return BlockResult
	case ConsoleCompanionMessage:
		return BlockCompanion
	}
	return BlockInfo
}
