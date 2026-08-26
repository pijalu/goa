// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

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
	role     string // streaming agent role ("companion", "coder", …) for color
	// toolLines records sub-agent tool activity ("⚙ read" / "✓ 76 matches") so
	// the user sees what the sub-agent actually does — previously tool calls
	// were invisible and sections looked frozen (team UI bug RC-2).
	toolLines []string
	// onChange marks the owning chat entry dirty so the viewport's render
	// cache does not serve stale lines after a mutation (frozen-section bug:
	// SetDone collapsed the component but the cached expanded lines stayed on
	// screen). Wired by ChatViewport.AddCompanionCycle.
	onChange func()
}

func newCompanionSection(cycle int, role string) *CompanionSectionComponent {
	if role == "" {
		role = "companion"
	}
	c := &CompanionSectionComponent{
		collapsibleComponent: collapsibleComponent{
			title:    fmt.Sprintf("%s · cycle %d", role, cycle),
			expanded: true,
		},
		thinking: newCompanionThinkingBlock(""),
		role:     role,
	}
	return c
}

func (sc *CompanionSectionComponent) SetThinking(text string) {
	sc.thinking.SetText(text)
	sc.changed()
}

func (sc *CompanionSectionComponent) SetMessage(text string) {
	sc.message = text
	sc.changed()
}

// changed notifies the owner (chat viewport) that the component mutated.
func (sc *CompanionSectionComponent) changed() {
	if sc.onChange != nil {
		sc.onChange()
	}
}

// AddToolLine appends a tool-activity marker to the section. A non-empty
// toolName records a call ("⚙ <name>"); a non-empty result records a
// completion preview ("✓ <preview>"). The last call line is replaced by its
// result when they arrive in order, keeping the list compact.
func (sc *CompanionSectionComponent) AddToolLine(toolName, result string) {
	switch {
	case toolName != "":
		sc.toolLines = append(sc.toolLines, "⚙ "+toolName)
	case result != "":
		line := "✓ " + result
		if n := len(sc.toolLines); n > 0 && strings.HasPrefix(sc.toolLines[n-1], "⚙ ") {
			sc.toolLines[n-1] = sc.toolLines[n-1] + " → " + line
			return
		}
		sc.toolLines = append(sc.toolLines, line)
	}
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
		sc.title = sc.role + " · " + endMessage
	}
	sc.changed()
}

// colorBar returns the color-coded first-column glyph ("▏") for this section's
// role, so concurrent roles are visually distinguishable in the shared chat.
func (sc *CompanionSectionComponent) colorBar() string {
	return ansi.Fg(hashColor(sc.role)) + "▏" + ansi.Reset
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
	bar := sc.colorBar()
	header := bar + ansi.Fg(TheTheme.ColorHex("assistant_msg")) + " " + glyph + " " + sc.title + ansi.Reset + suffix
	lines := []string{padToWidth(header, width)}
	if sc.expanded {
		// Render thinking block, prefixed with the role-colored first column.
		for _, line := range sc.thinking.Render(width) {
			lines = append(lines, bar+line)
		}
		// Render tool-activity markers between thinking and message.
		toolColor := ansi.Fg(TheTheme.ColorHex("tool_running"))
		for _, line := range sc.toolLines {
			lines = append(lines, padToWidth(bar+"  "+toolColor+line+ansi.Reset, width))
		}
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
func (cv *ChatViewport) AddCompanionCycle(cycle int, role ...string) *CompanionSectionComponent {
	sc := newCompanionSection(cycle, strings.Join(role, ""))
	currentCompanionSection = sc
	id := cv.Append(MessageEntry{Data: MessageData{Type: ConsoleCompanionMessage, Meta: map[string]string{"cycle": fmt.Sprintf("%d", cycle), "agent": sc.role}}, View: sc})
	// Mutations (thinking/message/SetDone) must invalidate the entry's cached
	// render, or the viewport keeps showing the stale expanded section after
	// the stream ends (frozen-section bug).
	sc.onChange = func() { cv.MarkEntryDirty(id) }
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

// toolEcho renders the offscreen completion echo (CompletionEcho in
// tui/tool_execution.go): a compact BOXED one-liner styled with the finished
// tool's success/error color so it reads as a continuation of the tool call
// block it belongs to (bugs.md 2026-08-26). The ← prefix is kept as the
// continuation-of-message marker; content never replays raw output.
type toolEcho struct {
	text string // CompletionEcho text (already one line, ANSI-stripped on render)
	ok   bool   // true = tool succeeded (green), false = failed (red)
}

func newToolEcho(text string, ok bool) *toolEcho { return &toolEcho{text: text, ok: ok} }
func (m *toolEcho) HandleInput(string)           {}
func (m *toolEcho) Invalidate()                  {}

// Render draws a single bordered row: │ ← <echo> │ with the vertical borders
// and text in the status color. Truncates with an ellipsis when the echo
// exceeds the available width.
func (m *toolEcho) Render(width int) []string {
	color := ansi.Fg(TheTheme.ColorHex("tool_success"))
	if !m.ok {
		color = ansi.Fg(TheTheme.ColorHex("tool_error"))
	}
	clean := ansi.Strip(m.text)
	inner := "← " + clean
	// Budget: two border cells + one space of padding on each side.
	if maxLen := width - 4; maxLen > 8 {
		if runes := []rune(inner); len(runes) > maxLen {
			inner = string(runes[:maxLen-1]) + "…"
		}
	}
	border := color + ansi.BoxVertical + ansi.Reset
	line := border + " " + color + inner + ansi.Reset + " " + border
	return []string{padToWidth(line, width)}
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

// steeringPending renders the pending steering queue as a compact bordered
// bubble pinned at the bottom of the chat until the queued messages are
// consumed by the model. The bubble shows exactly one preview line; when the
// merged content spans more lines a "+N lines" stat appears in the footer,
// and when more than one message is queued a "(N messages)" stat appears.
// It uses the terminal default background (no bg fill) with │ side borders.
type steeringPending struct{ messages []string }

func newSteeringPending(text string) *steeringPending {
	return &steeringPending{messages: []string{text}}
}

// SetMessages replaces the queued messages (merged display + stats).
func (m *steeringPending) SetMessages(msgs []string) { m.messages = msgs }

// Messages returns the queued steering messages in order.
func (m *steeringPending) Messages() []string { return m.messages }

// SetText replaces the queue with a single message (session-restore path).
func (m *steeringPending) SetText(t string)   { m.messages = []string{t} }
func (m *steeringPending) HandleInput(string) {}
func (m *steeringPending) Invalidate()        {}

func (m *steeringPending) Render(width int) []string {
	if width <= 0 || len(m.messages) == 0 {
		return nil
	}
	fg := ansi.Fg(TheTheme.ColorHex("system_msg"))
	bd := ansi.Fg(TheTheme.ColorHex("border_default"))
	reset := ansi.Reset

	// Sanitize before Strip: pasted text is untrusted — raw ESC bytes must
	// become visible `\e` text, not reach the terminal. Strip afterwards is a
	// no-op on real sequences (Sanitize already escaped their ESC) but keeps
	// defense-in-depth for anything produced by goa itself.
	merged := strings.Join(m.messages, "\n\n")
	clean := ansi.Strip(ansi.Sanitize(merged))
	innerWidth := max(width-4, 1) // side border + space on each side of content
	wrapped := wrapSteeringText(clean, innerWidth)
	// box draws one bordered row with the terminal default background:
	// vertical bar, padded content, vertical bar — exactly width visible columns.
	box := func(content string) string {
		return bd + ansi.BoxVertical + fg + " " + padToWidth(content, innerWidth) + " " + bd + ansi.BoxVertical + reset
	}
	hline := func(l, r string) string {
		return bd + l + strings.Repeat(ansi.BoxHorizontal, width-2) + r + reset
	}

	lines := []string{hline(ansi.BoxTopLeft, ansi.BoxTopRight)}

	// One-line preview: the first non-blank wrapped line. Leading blanks are
	// skipped so a message starting with blank lines shows real content.
	if preview := firstNonBlank(wrapped); preview != "" {
		lines = append(lines, box(preview))
	}

	lines = append(lines, box(steeringFooter(len(m.messages), hiddenLines(wrapped))))
	lines = append(lines, hline(ansi.BoxBottomLeft, ansi.BoxBottomRight))
	return lines
}

// wrapSteeringText wraps multi-line steering text paragraph-by-paragraph.
// ansi.Wrap takes a single paragraph: feeding embedded newlines to Wrap
// would return "lines" containing '\n', which paint as several terminal rows
// and desync the compositor's line accounting (overlapping redraw).
func wrapSteeringText(clean string, innerWidth int) []string {
	var wrapped []string
	for i, para := range strings.Split(clean, "\n") {
		if strings.TrimSpace(para) == "" {
			wrapped = append(wrapped, "")
			continue
		}
		prefix := "  "
		if i == 0 {
			prefix = "✎ "
		}
		wrapped = append(wrapped, ansi.Wrap(prefix+para, innerWidth)...)
	}
	return wrapped
}

// firstNonBlank returns the first non-empty line (leading blanks skipped so
// a message starting with blank lines still shows real content).
func firstNonBlank(lines []string) string {
	for _, raw := range lines {
		if raw != "" {
			return raw
		}
	}
	return ""
}

// hiddenLines counts every wrapped visual line except the single preview row.
func hiddenLines(wrapped []string) int {
	if firstNonBlank(wrapped) == "" {
		return len(wrapped)
	}
	return len(wrapped) - 1
}

// steeringFooter renders the footer stats: message count, hidden line count,
// edit affordance.
func steeringFooter(numMessages, hidden int) string {
	var parts []string
	if numMessages > 1 {
		parts = append(parts, fmt.Sprintf("(%d messages)", numMessages))
	}
	if hidden > 0 {
		parts = append(parts, fmt.Sprintf("+%d lines", hidden))
	}
	parts = append(parts, "(alt+e to edit)")
	return strings.Join(parts, " ")
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
