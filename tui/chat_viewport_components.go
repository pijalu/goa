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
	gutter := ansi.Fg(color) + "│" + ansi.Reset
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
}

func newAssistantMessage(text string) *assistantMessage {
	return &assistantMessage{text: text}
}
func (m *assistantMessage) SetText(t string) { m.text = t; m.Invalidate() }
func (m *assistantMessage) SetFinishReason(reason string, tokens int, durMs int) {
	m.finishReason = reason
	m.tokenCount = tokens
	m.durationMs = durMs
}
func (m *assistantMessage) HandleInput(string) {}
func (m *assistantMessage) Invalidate()        {}
func (m *assistantMessage) Render(width int) []string {
	if m.text == "" {
		return nil
	}
	var lines []string

	// Markdown rendering with 1col left/right padding.
	// Render at width-2, then prepend " " (left) and padToWidth fills to width
	// (1 right space via padToWidth since " " + wrapped = width-1).
	contentW := width - 2
	renderer := NewMDStreamRenderer(contentW, TheTheme)
	rendered := renderer.Render(m.text)
	for _, line := range rendered {
		lines = append(lines, padToWidth(" "+line, width))
	}

	// Finish reason line (S9 spec: "── stop · N tok · Ns · think:N ─────")
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

		// Format: "── stop · N tok · Ns " (left) + "──────────────────" (fill)
		// Account for 1col left padding.
		left := " " + fmt.Sprintf("── %s", label) // leading space = 1col left pad
		if rightText != "" {
			left += " · " + rightText
		}
		left += " "
		leftW := ansi.Width(left)
		fill := width - leftW
		if fill < 1 {
			fill = 1
		}
		line := finishColor + left + strings.Repeat("─", fill) + ansi.Reset
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
	top := bd + "\u256d" + strings.Repeat("\u2500", width-2) + "\u256e" + reset
	bot := bd + "\u2570" + strings.Repeat("\u2500", width-2) + "\u256f" + reset

	// bodyCell is the visible width between the two side borders; it must equal
	// width-2 so the right │ aligns with the top/bottom border corners.
	bodyCell := width - 2
	lines := []string{padToWidthStyled(top, width, "")}
	for _, raw := range inner {
		body := padToWidthStyled(" "+raw, bodyCell, "")
		lines = append(lines, bd+"\u2502"+reset+body+bd+"\u2502"+reset)
	}
	lines = append(lines, padToWidthStyled(bot, width, ""))
	return lines
}

// collapsibleComponent wraps streamed content in an expandable block.
// Used for companion messages so they can be collapsed when finished.
type collapsibleComponent struct {
	title    string
	text     string
	expanded bool
	done     bool
}

func newCollapsibleComponent(title, text string) *collapsibleComponent {
	return &collapsibleComponent{title: title, text: text, expanded: true}
}

func (c *collapsibleComponent) SetText(t string) { c.text = t }
func (c *collapsibleComponent) SetDone() {
	c.done = true
	c.expanded = false
}
func (c *collapsibleComponent) HandleInput(data string) {
	if matchesKey(data, KeyEnter) {
		c.expanded = !c.expanded
	}
}

// CompanionSectionComponent wraps one companion cycle: thinking + message.
// It is expanded while running and collapses on end, showing the end message.
type CompanionSectionComponent struct {
	collapsibleComponent
	thinking *thinkingBlock
	message  string // final message text to show in collapsed header
}

func newCompanionSection(cycle int) *CompanionSectionComponent {
	c := &CompanionSectionComponent{
		collapsibleComponent: collapsibleComponent{
			title:    fmt.Sprintf("companion · cycle %d", cycle),
			expanded: true,
		},
		thinking: newCompanionThinkingBlock(""),
	}
	return c
}

func (sc *CompanionSectionComponent) SetThinking(text string) {
	sc.thinking.SetText(text)
}

func (sc *CompanionSectionComponent) SetMessage(text string) {
	sc.message = text
}

func (sc *CompanionSectionComponent) Done() bool {
	return sc.done
}

func (sc *CompanionSectionComponent) SetDone(endMessage string) {
	sc.done = true
	sc.expanded = false
	// Clear thinking and message text to prevent stale content
	sc.thinking.SetText("")
	sc.message = ""
	if endMessage != "" {
		// Truncate for collapsed view
		if len(endMessage) > 60 {
			endMessage = endMessage[:60] + "…"
		}
		sc.title = "companion · " + endMessage
	}
}

func (sc *CompanionSectionComponent) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	glyph := "▸"
	if sc.expanded {
		glyph = "▾"
	}
	suffix := ""
	if sc.done {
		suffix = ansi.Fg(TheTheme.ColorHex("tool_success")) + " [done]" + ansi.Reset
	}
	header := ansi.Fg(TheTheme.ColorHex("assistant_msg")) + "  " + glyph + " " + sc.title + ansi.Reset + suffix
	lines := []string{padToWidth(header, width)}
	if sc.expanded {
		// Render thinking block
		lines = append(lines, sc.thinking.Render(width)...)
		// Render message
		if sc.message != "" {
			renderer := NewMDStreamRenderer(width, TheTheme)
			for _, line := range renderer.Render(sc.message) {
				lines = append(lines, padToWidth(line, width))
			}
		}
	}
	return lines
}

func (sc *CompanionSectionComponent) HandleInput(data string) {
	if matchesKey(data, KeyEnter) {
		sc.expanded = !sc.expanded
	}
}

func (sc *CompanionSectionComponent) Invalidate() {}

// currentCompanionSection tracks the active companion cycle section.
var currentCompanionSection *CompanionSectionComponent

// AddCompanionCycle creates a new companion section for the given cycle.
func (cv *ChatViewport) AddCompanionCycle(cycle int) *CompanionSectionComponent {
	sc := newCompanionSection(cycle)
	currentCompanionSection = sc
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleCompanionMessage, Meta: map[string]string{"cycle": fmt.Sprintf("%d", cycle)}}, View: sc})
	return sc
}

// SetLastCompanionThinking updates the current companion cycle's thinking text.
func (cv *ChatViewport) SetLastCompanionThinking(text string) {
	if currentCompanionSection != nil {
		currentCompanionSection.SetThinking(text)
	}
}

// SetLastCompanionMessage updates the current companion cycle's message text.
func (cv *ChatViewport) SetLastCompanionMessage(text string) {
	if currentCompanionSection != nil {
		currentCompanionSection.SetMessage(text)
	}
}

// SetLastCompanionCycleEnd marks the current companion cycle as done with the
// given end message (the review forwarded to the main LLM).
func (cv *ChatViewport) SetLastCompanionCycleEnd(endMessage string) {
	if currentCompanionSection != nil {
		currentCompanionSection.SetDone(endMessage)
	}
}

// CurrentCompanionSection returns the most recently added companion section.
func (cv *ChatViewport) CurrentCompanionSection() *CompanionSectionComponent {
	return currentCompanionSection
}
func (c *collapsibleComponent) Invalidate() {}
func (c *collapsibleComponent) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	glyph := "▸"
	if c.expanded {
		glyph = "▾"
	}
	suffix := ""
	if c.done {
		suffix = ansi.Fg(TheTheme.ColorHex("tool_success")) + " [done]" + ansi.Reset
	}
	header := ansi.Fg(TheTheme.ColorHex("assistant_msg")) + "  " + glyph + " " + c.title + ansi.Reset + suffix
	lines := []string{padToWidth(header, width)}
	if c.expanded {
		renderer := NewMDStreamRenderer(width, TheTheme)
		for _, line := range renderer.Render(c.text) {
			lines = append(lines, padToWidth(line, width))
		}
	}
	return lines
}

// thinkingBlock renders thinking content: italic, dim, indented with ▏.
// Supports collapse/expand with Enter (S8). When agentLabel is set, the header
// reads "<label> thinking..." so multiple agents can be distinguished.
type thinkingBlock struct {
	text        string
	expanded    bool
	timing      string // e.g. "0.8s"
	tokenCount  int
	turnNumber  int
	textColor   string // theme token name; defaults to "thinking_text"
	headerColor string // theme token name; defaults to "thinking_header"
	agentLabel  string
}

func newThinkingBlock(text string) *thinkingBlock {
	return &thinkingBlock{text: text, expanded: true, textColor: "thinking_text", headerColor: "thinking_header"}
}

func newCompanionThinkingBlock(text string) *thinkingBlock {
	return &thinkingBlock{text: text, expanded: true, textColor: "companion_thinking_text", headerColor: "companion_thinking_header"}
}

func (m *thinkingBlock) SetText(t string) { m.text = t; m.Invalidate() }
func (m *thinkingBlock) SetTiming(t string, tokens int, turn int) {
	m.timing = t
	m.tokenCount = tokens
	m.turnNumber = turn
}
func (m *thinkingBlock) HandleInput(data string) {
	// Toggle expand/collapse on Enter
	if matchesKey(data, KeyEnter) {
		m.expanded = !m.expanded
	}
}
func (m *thinkingBlock) Invalidate() {}
func (m *thinkingBlock) Render(width int) []string {
	clean := strings.TrimSpace(m.text)
	header := m.buildHeader()
	if !m.expanded || clean == "" {
		return []string{"", padToWidth(header, width), ""}
	}

	gutterPrefix, cw := m.computeGutter(width)
	content := m.renderThinkingContent(clean, gutterPrefix, cw, width)

	lines := []string{"", padToWidth(header, width)}
	lines = append(lines, content...)
	if len(lines) == 1 {
		lines = append(lines, padToWidth(gutterPrefix+ansi.Reset, width))
	}
	lines = append(lines, "")
	return lines
}

func (m *thinkingBlock) buildHeader() string {
	tokenStr := ""
	if m.tokenCount > 0 {
		tokenStr = fmt.Sprintf(" %d tok", m.tokenCount)
	}
	timingStr := ""
	if m.timing != "" {
		timingStr = " " + m.timing
	}
	glyph := "▸"
	if m.expanded {
		glyph = "▾"
	}
	headerColor := ansi.Fg(TheTheme.ColorHex(m.headerColor))
	label := ""
	if m.agentLabel != "" {
		label = " " + m.agentLabel
	}
	return fmt.Sprintf("  %s%s%s thinking...%s%s%s", headerColor, glyph, label, timingStr, tokenStr, ansi.Reset)
}

func (m *thinkingBlock) computeGutter(width int) (string, int) {
	color := ansi.Fg(TheTheme.ColorHex(m.textColor))
	gutterPrefix := "  " + color + "▏"
	gutterW := visibleWidth(gutterPrefix)
	if strings.Contains(gutterPrefix, "▏") {
		gutterW++ // some terminals render block elements at 2 columns
	}
	cw := width - gutterW
	if cw >= 10 {
		return gutterPrefix, cw
	}
	// Very narrow terminal: fall back to minimal gutter.
	return "  ▏", width - 2
}

func (m *thinkingBlock) renderThinkingContent(clean, gutterPrefix string, cw, width int) []string {
	if looksLikeMarkdown(clean) {
		return m.renderMarkdownContent(clean, gutterPrefix, cw, width)
	}
	return m.renderPlainContent(clean, gutterPrefix, cw, width)
}

func (m *thinkingBlock) renderMarkdownContent(clean, gutterPrefix string, cw, width int) []string {
	renderer := NewMDStreamRenderer(cw, TheTheme)
	rendered := renderer.Render(clean)
	var lines []string
	reset := ansi.Reset
	for _, mdLine := range rendered {
		if ansi.Strip(mdLine) == "" {
			lines = append(lines, padToWidth(gutterPrefix+reset, width))
			continue
		}
		lines = append(lines, padToWidth(gutterPrefix+mdLine+reset, width))
	}
	return lines
}

func (m *thinkingBlock) renderPlainContent(clean, gutterPrefix string, cw, width int) []string {
	var lines []string
	reset := ansi.Reset
	for _, rawLine := range strings.Split(clean, "\n") {
		wrapped := ansi.Wrap(strings.TrimRight(rawLine, " \r\t"), cw)
		if len(wrapped) == 0 {
			lines = append(lines, padToWidth(gutterPrefix+reset, width))
			continue
		}
		for _, line := range wrapped {
			lines = append(lines, padToWidth(gutterPrefix+line+reset, width))
		}
	}
	return lines
}

// toolCall renders in amber/tool_running color like pi's tool execution header.
type toolCall struct{ text string }

func newToolCall(text string) *toolCall { return &toolCall{text: text} }
func (m *toolCall) HandleInput(string)  {}
func (m *toolCall) Invalidate()         {}
func (m *toolCall) Render(width int) []string {
	color := ansi.Fg(TheTheme.ColorHex("tool_running"))
	clean := ansi.Strip(m.text)
	padded := " " + padToWidth(clean, width-2)
	return withSpacers([]string{padToWidth(color+padded+ansi.Reset, width)}, width, "")
}

// toolResult renders dim with optional markdown rendering.
// If the text looks like markdown, it is routed through MDStreamRenderer.
// ANSI codes from terminal tools (like ls --color=auto) are always stripped.
type toolResult struct{ text string }

func newToolResult(text string) *toolResult { return &toolResult{text: text} }
func (m *toolResult) HandleInput(string)    {}
func (m *toolResult) Invalidate()           {}
func (m *toolResult) Render(width int) []string {
	color := ansi.Fg(TheTheme.ColorHex("system_msg"))
	// Strip ANSI so tool-originated escape codes don't corrupt the TUI
	clean := ansi.Strip(m.text)

	// Route through MDStreamRenderer if text looks like markdown
	if looksLikeMarkdown(clean) && len(clean) > 80 {
		renderer := NewMDStreamRenderer(width-2, TheTheme)
		rendered := renderer.Render(clean)
		var lines []string
		for _, line := range rendered {
			lines = append(lines, " "+padToWidth(color+line+ansi.Reset, width-1))
		}
		return withSpacers(lines, width, "")
	}

	maxLen := width - 7
	if maxLen < 10 {
		maxLen = 10
	}
	short := clean
	if len(short) > maxLen {
		short = short[:maxLen-1] + "…"
	}
	padded := " " + "  ← " + short
	return withSpacers([]string{padToWidth(color+padded+ansi.Reset, width)}, width, "")
}

// infoMessage renders simple informational text without box or background.
// Unlike systemMessage which uses renderGoaPanel (bordered panel with dark
// background), this is for plain status notices like "Connected to Model X.".
type infoMessage struct{ text string }

func newInfoMessage(text string) *infoMessage { return &infoMessage{text: text} }
func (m *infoMessage) SetText(t string)       { m.text = t }
func (m *infoMessage) HandleInput(string)     {}
func (m *infoMessage) Invalidate()            {}
func (m *infoMessage) Render(width int) []string {
	if m.text == "" {
		return nil
	}
	fg := ansi.Fg(TheTheme.ColorHex("system_msg"))
	// Simple one-line info: "  message" with no background, no box
	content := fg + "⟡ " + ansi.Strip(m.text) + ansi.Reset
	return []string{padToWidth(content, width)}
}

// steeringPending renders a pending steering message that stays pinned at the
// bottom of the chat until it is consumed by the model.
type steeringPending struct{ text string }

func newSteeringPending(text string) *steeringPending { return &steeringPending{text: text} }
func (m *steeringPending) SetText(t string)            { m.text = t }
func (m *steeringPending) HandleInput(string)          {}
func (m *steeringPending) Invalidate()                 {}
func (m *steeringPending) Render(width int) []string {
	if width <= 0 || m.text == "" {
		return nil
	}
	bg := ansi.Bg(TheTheme.ColorHex("input_bg"))
	fg := ansi.Fg(TheTheme.ColorHex("system_msg"))
	border := ansi.Fg(TheTheme.ColorHex("border_default"))
	reset := ansi.Reset

	clean := ansi.Strip(m.text)
	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	wrapped := ansi.Wrap(fmt.Sprintf("✎ steering: %s", clean), innerWidth)

	var lines []string
	top := border + "┌" + strings.Repeat("─", width-2) + "┐" + reset
	bot := border + "└" + strings.Repeat("─", width-2) + "┘" + reset
	lines = append(lines, padToWidth(top, width))
	for _, raw := range wrapped {
		body := " " + padToWidth(raw, innerWidth) + " "
		lines = append(lines, padToWidth(bg+fg+body+reset, width))
	}
	lines = append(lines, padToWidth(bot, width))
	return lines
}

// LastToolComponent returns the last ToolExecutionComponent in the
// conversation, if any. Uses the Model's generic LastWhere primitive.
func (cv *ChatViewport) LastToolComponent() *ToolExecutionComponent {
	e, ok := cv.Conversation.LastWhere(func(e MessageEntry) bool {
		_, is := e.View.(*ToolExecutionComponent)
		return is
	})
	if !ok {
		return nil
	}
	return e.View.(*ToolExecutionComponent)
}
