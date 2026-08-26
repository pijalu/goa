// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// gutteredComponent wraps a Component with a colored vertical gutter.
// The gutter is prepended to every rendered line.
type gutteredComponent struct {
	inner Component
	color string
	kind  string // "companion" or "companion_thinking"
}

func (g *gutteredComponent) Render(width int) []string {
	lines := g.inner.Render(width - 2) // subtract 1 for gutter, 1 for left padding
	if len(lines) == 0 {
		return nil
	}
	color := g.color
	if color == "" {
		color = "#a371f7"
	}
	gutter := ansi.Fg(color) + ansi.BoxVertical + ansi.Reset
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = gutter + " " + line
	}
	return result
}
func (g *gutteredComponent) HandleInput(key string) { g.inner.HandleInput(key) }
func (g *gutteredComponent) Invalidate()            { g.inner.Invalidate() }
func (g *gutteredComponent) SetText(t string) {
	if s, ok := g.inner.(interface{ SetText(string) }); ok {
		s.SetText(t)
	}
}

// ── Message component types ──

// userMessage renders a user message like pi's UserMessageComponent:
// full-width background box with bright foreground text.
type userMessage struct{ text string }

func newUserMessage(text string) *userMessage { return &userMessage{text: text} }
func (m *userMessage) SetText(t string)       { m.text = t }
func (m *userMessage) HandleInput(string)     {}
func (m *userMessage) Invalidate()            {}
func (m *userMessage) Render(width int) []string {
	bgHex := TheTheme.ColorHex("user_msg_bg")
	fgHex := TheTheme.ColorHex("user_msg")
	bg := ansi.Bg(bgHex)
	fg := ansi.Fg(fgHex)
	clean := ansi.Strip(m.text)
	var lines []string
	// Split on newlines first, then wrap each paragraph
	paragraphs := strings.Split(clean, "\n")
	for _, para := range paragraphs {
		wrapped := ansi.Wrap(para, width-2)
		for _, line := range wrapped {
			lines = append(lines, bg+fg+" "+padToWidth(line, width-1)+ansi.Reset)
		}
	}
	return withSpacers(lines, width, bgHex)
}

// assistantMessage renders like pi's AssistantMessageComponent using markdown.
// Uses the existing MDStreamRenderer for proper markdown rendering.
type assistantMessage struct {
	text         string
	finishReason string // e.g. "stop", "tool_calls", "length"
	tokenCount   int
	durationMs   int

	// renderCache memoizes the last rendered frame. Keyed implicitly by
	// (cacheText, cacheWidth, cacheFinish); invalidated on SetText/SetFinishReason.
	renderCache []string
	cacheText   string
	cacheWidth  int
	cacheFinish string

	// incr is the persistent incremental markdown renderer. It memoizes the
	// rendered stable prefix (closed blocks) so that during streaming only the
	// open tail is re-parsed each frame instead of the whole message. Recreated
	// when the render width changes. Nil until first render.
	incr      *IncrementalMDRenderer
	incrWidth int
}

func newAssistantMessage(text string) *assistantMessage {
	return &assistantMessage{text: text}
}
func (m *assistantMessage) SetText(t string) { m.text = t; m.Invalidate() }
func (m *assistantMessage) SetFinishReason(reason string, tokens int, durMs int) {
	m.finishReason = reason
	m.tokenCount = tokens
	m.durationMs = durMs
	// Finish metadata changes the footer line, so the cached frame is stale.
	m.renderCache = nil
}
func (m *assistantMessage) HandleInput(string) {}
func (m *assistantMessage) Invalidate()        {}
func (m *assistantMessage) Render(width int) []string {
	if m.text == "" {
		return nil
	}
	// A2: memoize the rendered frame keyed by (text, width, finishReason).
	// During streaming the entry is re-rendered every frame (~60fps) as text
	// grows; a full markdown re-parse each frame is O(len) per frame. When the
	// text has NOT changed since the last render (e.g. an unrelated component
	// marked the viewport dirty), reuse the cached lines instead of re-parsing.
	if m.renderCache != nil && m.cacheText == m.text && m.cacheWidth == width &&
		m.cacheFinish == m.finishReason {
		return m.renderCache
	}
	lines := m.renderFrame(width)
	m.renderCache = lines
	m.cacheText = m.text
	m.cacheWidth = width
	m.cacheFinish = m.finishReason
	return lines
}

// renderFrame performs the actual markdown render (the uncached path).
func (m *assistantMessage) renderFrame(width int) []string {
	var lines []string

	// Markdown rendering with 1col left/right padding.
	// Render at width-2, then prepend " " (left) and padToWidth fills to width
	// (1 right space via padToWidth since " " + wrapped = width-1).
	contentW := width - 2
	// Use the persistent incremental renderer so a growing streamed message
	// only re-parses the open tail; recreate it if the width changed.
	if m.incr == nil || m.incrWidth != contentW {
		m.incr = NewIncrementalMDRenderer(contentW, TheTheme)
		m.incrWidth = contentW
	}
	rendered := m.incr.Render(m.text)
	for _, line := range rendered {
		lines = append(lines, padToWidth(" "+line, width))
	}

	// Finish reason line (S9 spec: ── stop · N tok · Ns · think:N ─────)
	if m.finishReason != "" {
		finishColor := ansi.Fg(TheTheme.ColorHex("finish_" + m.finishReason))
		label := m.finishReason
		// Build the right-side summary
		var rightParts []string
		if m.tokenCount > 0 {
			rightParts = append(rightParts, fmt.Sprintf("%d tok", m.tokenCount))
		}
		if m.durationMs > 0 {
			rightParts = append(rightParts, fmt.Sprintf("%.2fs", float64(m.durationMs)/1000.0))
		}
		rightText := strings.Join(rightParts, " · ")

		// Format: ── stop · N tok · Ns ␣ (left) + horizontal-bar fill
		// Account for 1col left padding.
		left := " " + ansi.RepeatHorizontal(2) + " " + label // leading space = 1col left pad
		if rightText != "" {
			left += " · " + rightText
		}
		left += " "
		leftW := ansi.Width(left)
		fill := width - leftW
		if fill < 1 {
			fill = 1
		}
		line := finishColor + left + strings.Repeat(ansi.BoxHorizontal, fill) + ansi.Reset
		lines = append(lines, padToWidth(line, width))
	}

	return withSpacers(lines, width, "")
}

// agentMessage renders a message from a specific agent with colored prefix (S10).
type agentMessage struct {
	text  string
	agent string // agent name for prefix
}

func newAgentMessage(text, agent string) *agentMessage {
	return &agentMessage{text: text, agent: agent}
}
func (m *agentMessage) SetText(t string)   { m.text = t; m.Invalidate() }
func (m *agentMessage) HandleInput(string) {}
func (m *agentMessage) Invalidate()        {}
func (m *agentMessage) Render(width int) []string {
	if m.text == "" {
		return nil
	}
	// Color based on agent name hash for consistency
	hue := hashColor(m.agent)
	prefix := ansi.Fg(hue) + "[" + m.agent + "]" + ansi.Reset + " "
	prefixW := ansi.Width(prefix)
	contentW := width - prefixW
	if contentW < 10 {
		contentW = width
		prefix = ""
	}
	renderer := NewMDStreamRenderer(contentW, TheTheme)
	rendered := renderer.Render(m.text)
	var lines []string
	for _, line := range rendered {
		lines = append(lines, prefix+padToWidth(line, contentW))
	}
	return withSpacers(lines, width, "")
}

// hashColor generates a deterministic color from a string (for agent prefixes).
func hashColor(s string) string {
	// Use an unsigned accumulator so the modulo is always non-negative. A signed
	// int hash overflows on longer names and Go's % preserves the sign, yielding
	// a negative palette index and an index-out-of-range panic (e.g. "fix login
	// bug" → palette[-4]).
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	palette := []string{"#58a6ff", "#3fb950", "#d29922", "#f85149", "#8957e5", "#bc8cff"}
	return palette[h%uint32(len(palette))]
}

// systemMessage renders like pi's system messages: dim, markdown-rendered.
// Multi-line command output (like /docs, /help) uses the MDStreamRenderer
// for proper formatting of headings, lists, code blocks, and tables.
// For pre-formatted text (like /commands output), it renders line-by-line
// without markdown parsing to preserve intentional newlines.
type systemMessage struct {
	text         string
	preformatted bool // if true, render line-by-line without markdown parsing
}

func newSystemMessage(text string) *systemMessage {
	return &systemMessage{text: text, preformatted: isPreformatted(text)}
}

func newSystemMessagePreformatted(text string) *systemMessage {
	return &systemMessage{text: text, preformatted: true}
}

func (m *systemMessage) SetText(t string)   { m.text = t; m.preformatted = isPreformatted(t) }
func (m *systemMessage) HandleInput(string) {}
func (m *systemMessage) Invalidate()        {}
func (m *systemMessage) Render(width int) []string {
	if m.text == "" {
		return nil
	}
	return renderGoaPanel(m.text, m.preformatted, width)
}

// renderGoaPanel renders goa-originated text (command output) inside a bordered
// panel. The box borders (╭─╮, │, ╰─╯) provide enough visual boundary so no
// dedicated background is needed. Content is markdown- or line-rendered on a
// narrowed inner width to leave room for the side borders (│) and padding.
func renderGoaPanel(text string, preformatted bool, width int) []string {
	if width < 8 {
		width = 8
	}
	borderHex := TheTheme.ColorHex("goa_panel_border")
	if borderHex == "" {
		borderHex = TheTheme.ColorHex("border_default")
	}
	bd := ansi.Fg(borderHex)

	innerWidth := width - 4 // markdown content width (excludes borders + padding)
	if innerWidth < 1 {
		innerWidth = 1
	}
	var inner []string
	if preformatted {
		inner = append(inner, strings.Split(text, "\n")...)
	} else {
		renderer := NewMDStreamRenderer(innerWidth, TheTheme)
		inner = renderer.Render(text)
	}
	if len(inner) == 0 {
		return nil
	}

	reset := ansi.Reset
	top := bd + ansi.BoxRoundedTopLeft + strings.Repeat(ansi.BoxHorizontal, width-2) + ansi.BoxRoundedTopRight + reset
	bot := bd + ansi.BoxRoundedBottomLeft + strings.Repeat(ansi.BoxHorizontal, width-2) + ansi.BoxRoundedBottomRight + reset

	// bodyCell is the visible width between the two side borders; it must equal
	// width-2 so the right │ aligns with the top/bottom border corners.
	bodyCell := width - 2
	lines := []string{padToWidthStyled(top, width, "")}
	for _, raw := range inner {
		body := padToWidthStyled(" "+raw, bodyCell, "")
		lines = append(lines, bd+ansi.BoxVertical+reset+body+bd+ansi.BoxVertical+reset)
	}
	lines = append(lines, padToWidthStyled(bot, width, ""))
	return lines
}
