// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

// TransformMessages applies a series of transformations to messages before
// sending them to an LLM. This handles cross-provider compatibility concerns:
//
//  1. Image downgrade — replaces image blocks with placeholders for non-vision models
//  2. Thinking→text conversion — converts thinking blocks to text for providers
//     that don't support native thinking content
//  3. Tool call ID normalization — rewrites tool call IDs for providers with
//     incompatible ID formats
//  4. Orphaned tool call synthesis — inserts synthetic tool results for tool
//     calls that lack corresponding results
//  5. Stop reason filtering — removes assistant messages with error/aborted
//     stop reasons
//
// Messages from the same model (same SourceProvider + SourceAPI + SourceModelID
// as the target model) pass through unchanged. Transformation applies only to
// cross-model replay.
//
// normalizeToolCallID is an optional function that rewrites tool call IDs for
// cross-provider compatibility. If nil, IDs are not normalized.
func TransformMessages(
	messages []Message,
	model Model,
	normalizeToolCallID func(id string, model Model, sourceMsg Message) string,
) []Message {
	// Phase 1: downgrade images for non-vision models
	transformed := downgradeImages(messages, model)

	// Phase 2: build tool call ID map and transform messages
	toolCallIDMap := make(map[string]string)

	transformed = transformMessageContent(transformed, model, toolCallIDMap, normalizeToolCallID)

	// Phase 3: normalize tool result IDs
	transformed = normalizeToolResultIDs(transformed, toolCallIDMap)

	// Phase 4: synthesize orphaned tool results
	transformed = synthesizeOrphanedToolResults(transformed)

	return transformed
}

// ---------------------------------------------------------------------------
// Phase 1: Image downgrade
// ---------------------------------------------------------------------------

const (
	nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
)

func downgradeImages(messages []Message, model Model) []Message {
	if IsVisionModel(model) {
		return messages
	}

	result := make([]Message, len(messages))
	for i, msg := range messages {
		switch msg.Role {
		case RoleUser:
			msg.Content = downgradeImagesInContent(msg.Content, nonVisionUserImagePlaceholder)
		case RoleToolResult:
			msg.Content = downgradeImagesInContent(msg.Content, nonVisionToolImagePlaceholder)
		}
		result[i] = msg
	}
	return result
}

// ---------------------------------------------------------------------------
// Phase 2: Content transformation
// ---------------------------------------------------------------------------

func transformMessageContent(
	messages []Message,
	model Model,
	toolCallIDMap map[string]string,
	normalizeToolCallID func(id string, model Model, sourceMsg Message) string,
) []Message {
	result := make([]Message, 0, len(messages))

	for _, msg := range messages {
		isSame := msg.SourceProvider == model.Provider &&
			msg.SourceAPI == model.Api &&
			msg.SourceModelID == model.ID

		switch msg.Role {
		case RoleUser:
			result = append(result, msg)

		case RoleAssistant:
			transformed := transformAssistantContent(msg, model, isSame, toolCallIDMap, normalizeToolCallID)
			result = append(result, transformed)

		case RoleToolResult:
			result = append(result, msg)

		default:
			result = append(result, msg)
		}
	}

	return result
}

func transformAssistantContent(
	msg Message,
	model Model,
	sameModel bool,
	toolCallIDMap map[string]string,
	normalizeToolCallID func(id string, model Model, sourceMsg Message) string,
) Message {
	transformed := make([]ContentBlock, 0, len(msg.Content))

	for _, block := range msg.Content {
		switch block.Type {
		case ContentBlockThinking:
			transformed = append(transformed, transformThinkingBlock(block, sameModel)...)

		case ContentBlockText:
			transformed = append(transformed, block)

		case ContentBlockToolCall:
			tc := transformToolCallBlock(block, sameModel, toolCallIDMap, normalizeToolCallID, msg)
			transformed = append(transformed, tc)

		default:
			transformed = append(transformed, block)
		}
	}

	msg.Content = transformed
	return msg
}

func transformThinkingBlock(block ContentBlock, sameModel bool) []ContentBlock {
	// Redacted thinking (encrypted) is opaque — only valid for the same model.
	if block.Redacted {
		if sameModel {
			return []ContentBlock{block}
		}
		return nil // drop for cross-model
	}

	// For same model: keep thinking blocks with signatures (needed for replay).
	if sameModel && block.ThinkingSignature != "" {
		return []ContentBlock{block}
	}

	// Skip empty thinking blocks.
	if block.Thinking == "" || isBlank(block.Thinking) {
		return nil
	}

	// For same model: keep as-is.
	if sameModel {
		return []ContentBlock{block}
	}

	// For cross-model: convert thinking to text block.
	return []ContentBlock{
		{
			Type: ContentBlockText,
			Text: block.Thinking,
		},
	}
}

func transformToolCallBlock(
	block ContentBlock,
	sameModel bool,
	toolCallIDMap map[string]string,
	normalizeToolCallID func(id string, model Model, sourceMsg Message) string,
	_ Message,
) ContentBlock {
	result := block

	// For cross-model: normalize tool call ID if a normalizer is provided.
	if !sameModel && normalizeToolCallID != nil {
		newID := normalizeToolCallID(block.ToolCallID, Model{}, Message{})
		if newID != block.ToolCallID {
			toolCallIDMap[block.ToolCallID] = newID
			result.ToolCallID = newID
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Phase 3: Tool result ID normalization
// ---------------------------------------------------------------------------

func normalizeToolResultIDs(messages []Message, toolCallIDMap map[string]string) []Message {
	if len(toolCallIDMap) == 0 {
		return messages
	}

	result := make([]Message, len(messages))
	for i, msg := range messages {
		if msg.Role == RoleToolResult {
			if newID, ok := toolCallIDMap[msg.Content[0].ToolCallID]; ok && newID != msg.Content[0].ToolCallID {
				msg.Content[0].ToolCallID = newID
			}
		}
		result[i] = msg
	}
	return result
}

// ---------------------------------------------------------------------------
// Phase 4: Orphaned tool result synthesis
// ---------------------------------------------------------------------------

// toolCallTracker tracks pending tool calls and their results during message
// transformation. When a tool call has no corresponding result, a synthetic
// error result is inserted.
type toolCallTracker struct {
	pending  []ContentBlock
	existing map[string]bool
	result   []Message
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{
		existing: make(map[string]bool),
	}
}

func (t *toolCallTracker) flushOrphans() {
	if len(t.pending) == 0 {
		return
	}
	for _, tc := range t.pending {
		if !t.existing[tc.ToolCallID] {
			t.result = append(t.result, Message{
				Role: RoleToolResult,
				Content: []ContentBlock{
					{
						Type:       ContentBlockToolResult,
						ToolCallID: tc.ToolCallID,
						ToolName:   tc.ToolName,
						Text:       "No result provided",
						IsError:    true,
					},
				},
			})
		}
	}
	t.pending = nil
	t.existing = make(map[string]bool)
}

func (t *toolCallTracker) noteAssistant(msg Message) {
	t.flushOrphans()

	// Skip errored/aborted/incomplete assistant messages — these are incomplete
	// turns with partial content that shouldn't be replayed.
	if shouldFilterStopReason(msg.StopReason) {
		return
	}

	for _, block := range msg.Content {
		if block.Type == ContentBlockToolCall {
			t.pending = append(t.pending, block)
		}
	}

	t.result = append(t.result, msg)
}

func (t *toolCallTracker) noteToolResult(msg Message) {
	if len(msg.Content) > 0 {
		t.existing[msg.Content[0].ToolCallID] = true
	}
	t.result = append(t.result, msg)
}

func shouldFilterStopReason(reason StopReason) bool {
	switch reason {
	case StopReasonError, StopReasonContentFiltered, StopReasonMaxTokens:
		return true
	default:
		return false
	}
}

func synthesizeOrphanedToolResults(messages []Message) []Message {
	tracker := newToolCallTracker()

	for _, msg := range messages {
		switch msg.Role {
		case RoleAssistant:
			tracker.noteAssistant(msg)
		case RoleToolResult:
			tracker.noteToolResult(msg)
		case RoleUser:
			tracker.flushOrphans()
			tracker.result = append(tracker.result, msg)
		default:
			tracker.result = append(tracker.result, msg)
		}
	}

	tracker.flushOrphans()
	return tracker.result
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func isBlank(s string) bool {
	for i := range len(s) {
		if s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' {
			return false
		}
	}
	return true
}
