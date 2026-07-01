// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package xmlstream

import "strings"

// BlockType represents the type of content block in a message
type BlockType string

const (
	BlockThinking  BlockType = "thinking"
	BlockSkillCall BlockType = "skillcall"
	BlockToolCall  BlockType = "toolcall"
	BlockContent   BlockType = "content"
	BlockStats     BlockType = "stats"
)

// MessageState tracks the current nesting level within a message
type MessageState struct {
	// Current block tracking
	CurrentBlock BlockType
	InBlocks     bool
	InMessage    bool
	PendingRole  bool

	// Skill call nesting
	SkillCallDepth int
	InConversation bool

	// Tool call state for pairing
	LastToolName   string
	LastToolCallID string
	HasToolResult  bool
}

// NewMessageState creates a fresh message state
func NewMessageState() *MessageState {
	return &MessageState{}
}

// Reset clears state for the next message
func (ms *MessageState) Reset() {
	ms.CurrentBlock = ""
	ms.InBlocks = false
	ms.InMessage = false
	ms.PendingRole = false
	ms.SkillCallDepth = 0
	ms.InConversation = false
	ms.LastToolName = ""
	ms.LastToolCallID = ""
	ms.HasToolResult = false
}

// WriteOpenConversation writes the opening conversation tag and metadata (does NOT include <messages>)
func WriteOpenConversation(id, model, startTime string) string {
	return `<conversation>
  <metadata>
    <id>` + EscapeXMLAttr(id) + `</id>
    <model>` + EscapeXMLAttr(model) + `</model>
    <start>` + EscapeXMLAttr(startTime) + `</start>
  </metadata>
  <messages>
`
}

// WriteOpenMessages writes the opening messages tag
func WriteOpenMessages() string {
	return ""
}

// WriteCloseMessages writes the closing messages tag
func WriteCloseMessages() string {
	return "  </messages>\n"
}

// WriteCloseConversation writes the closing conversation tag
func WriteCloseConversation() string {
	return "</conversation>\n"
}

// WriteMessageStart writes the opening message tag
func WriteMessageStart() string {
	return `    <message>
`
}

// WriteMessageEnd writes the closing message tag
func WriteMessageEnd() string {
	return "    </message>\n"
}

// WriteRoleStart writes the opening role tag with role name
func WriteRoleStart(role string) string {
	return "      <role>" + EscapeXML(role) + "</role>\n      <blocks>\n"
}

// WriteBlocksStart writes the opening blocks tag
func WriteBlocksStart() string {
	return ""
}

// WriteBlockOpen writes the opening tag for a block type
func WriteBlockOpen(blockType BlockType) string {
	return "        <" + string(blockType) + ">"
}

// WriteBlockClose writes the closing tag for a block type
func WriteBlockClose(blockType BlockType) string {
	return "</" + string(blockType) + ">\n"
}

// WriteSkillCallStart writes the opening skillcall tag with name
func WriteSkillCallStart(name string) string {
	return "        <skillcall>\n          <name>" + EscapeXML(name) + "</name>\n"
}

// WriteSkillCallInput writes the input section for a skill call
func WriteSkillCallInput(input string) string {
	return "          <input><![CDATA[" + input + "]]></input>\n"
}

// WriteSkillCallConvStart writes the opening nested conversation tag
func WriteSkillCallConvStart() string {
	return "          <conversation>\n"
}

// WriteSkillCallConvEnd writes the closing nested conversation tag
func WriteSkillCallConvEnd() string {
	return "          </conversation>\n"
}

// WriteSkillCallOutput writes the output section for a skill call
func WriteSkillCallOutput(output string) string {
	return "          <output><![CDATA[" + output + "]]></output>\n"
}

// WriteSkillCallEnd writes the closing skillcall tag
func WriteSkillCallEnd() string {
	return "        </skillcall>\n"
}

// WriteToolCallStart writes the opening toolcall tag
func WriteToolCallStart() string {
	return "        <toolcall>\n"
}

// WriteToolName writes the tool name element
func WriteToolName(name string) string {
	return "          <name>" + EscapeXML(name) + "</name>\n"
}

// WriteToolInput writes the tool input element
func WriteToolInput(input string) string {
	return "          <input><![CDATA[" + input + "]]></input>\n"
}

// WriteToolOutput writes the tool output element
func WriteToolOutput(output string) string {
	return "          <output><![CDATA[" + output + "]]></output>\n"
}

// WriteToolCallEnd writes the closing toolcall tag
func WriteToolCallEnd() string {
	return "        </toolcall>\n"
}

// WriteContent writes content element with escaped content
func WriteContent(content string) string {
	return "        <content>" + EscapeXML(content) + "</content>\n"
}

// WriteStats writes stats element with timing information
func WriteStats(promptN, predictedN int, promptMs, predictedMs float64) string {
	var sb strings.Builder
	sb.WriteString("        <stats>\n")
	sb.WriteString("          <tokens>\n")
	sb.WriteString("            <prompt>" + itoa(promptN) + "</prompt>\n")
	sb.WriteString("            <predicted>" + itoa(predictedN) + "</predicted>\n")
	sb.WriteString("          </tokens>\n")
	sb.WriteString("          <timing_ms>\n")
	sb.WriteString("            <prompt>" + ftoa(promptMs) + "</prompt>\n")
	sb.WriteString("            <predicted>" + ftoa(predictedMs) + "</predicted>\n")
	sb.WriteString("          </timing_ms>\n")
	sb.WriteString("        </stats>\n")
	return sb.String()
}

// WriteBlocksEnd writes the closing blocks tag
func WriteBlocksEnd() string {
	return "      </blocks>\n"
}

// itoa converts int to string without importing strconv
func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var sb strings.Builder
	neg := n < 0
	if neg {
		n = -n
	}

	for n > 0 {
		sb.WriteRune(rune('0' + n%10))
		n /= 10
	}

	if neg {
		sb.WriteRune('-')
	}

	// Reverse
	runes := []rune(sb.String())
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}

	return string(runes)
}

// ftoa converts float64 to string (simple implementation, 2 decimal places)
func ftoa(f float64) string {
	var sb strings.Builder

	// Handle negative
	neg := f < 0
	if neg {
		sb.WriteString("-")
		f = -f
	}

	// Get integer and decimal parts
	intPart := int(f)
	decPart := int((f - float64(intPart)) * 100)

	// Write integer part
	sb.WriteString(itoa(intPart))
	sb.WriteString(".")

	// Write decimal part (always 2 digits)
	if decPart < 10 {
		sb.WriteString("0")
	}
	sb.WriteString(itoa(decPart))

	return sb.String()
}
