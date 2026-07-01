// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// ClarifyCard is a non-interactive modal rendered INSIDE the conversation
// viewport by the ask_user_question tool. It displays a clarification request
// (Title / Summary / Question / Options) so the user has rich context, while
// the actual answer is typed in the MAIN input line (which carries the editor
// title). The card never captures keyboard input — see "Input discipline" in
// docs/TUI.md.
type ClarifyCard struct {
	title    string
	summary  string
	question string
	options  []string
}

// NewClarifyCard builds a clarification card. title and question are required;
// summary and options are optional (rendered only when non-empty).
func NewClarifyCard(title, summary, question string, options []string) *ClarifyCard {
	return &ClarifyCard{
		title:    strings.TrimSpace(title),
		summary:  strings.TrimSpace(summary),
		question: strings.TrimSpace(question),
		options:  options,
	}
}

// Title returns the card title (used by the host to set the main editor title).
func (c *ClarifyCard) Title() string { return c.title }

// Question returns the question text.
func (c *ClarifyCard) Question() string { return c.question }

// Options returns the answer options (may be empty for free-text questions).
func (c *ClarifyCard) Options() []string { return c.options }

// HandleInput is a no-op: the card is display-only; answers are entered on the
// main input line.
func (c *ClarifyCard) HandleInput(string) {}

// Invalidate is a no-op (no cached state).
func (c *ClarifyCard) Invalidate() {}

// Render draws a bordered panel with an accent title bar, followed by the
// summary (if any), the question, and numbered options (if any).
func (c *ClarifyCard) Render(width int) []string {
	if width < 12 {
		width = 12
	}
	bd, ac, dim := cardColors()
	reset := ansi.Reset
	inner := width - 4
	if inner < 2 {
		inner = 2
	}

	body := c.renderBody(inner, ac, dim)
	if len(body) == 0 {
		body = []string{""}
	}

	top := bd + "╭" + strings.Repeat("─", width-2) + "╮" + reset
	bot := bd + "╰" + strings.Repeat("─", width-2) + "╯" + reset
	cellW := width - 2
	lines := []string{padToWidthStyled(top, width, "")}
	for _, raw := range body {
		lines = append(lines, bd+"│"+reset+padToWidthStyled(" "+raw, cellW, "")+bd+"│"+reset)
	}
	lines = append(lines, padToWidthStyled(bot, width, ""))
	return lines
}

// cardColors resolves the theme colors used by the card.
func cardColors() (bd, ac, dim string) {
	bd = TheTheme.ColorHex("goa_panel_border")
	if bd == "" {
		bd = TheTheme.ColorHex("border_default")
	}
	ac = TheTheme.ColorHex("accent")
	if ac == "" {
		ac = TheTheme.ColorHex("selection_fg")
	}
	dim = TheTheme.ColorHex("system_msg")
	return bd, ac, dim
}

// renderBody builds the inner content lines for the card at the given width.
func (c *ClarifyCard) renderBody(inner int, ac, dim string) []string {
	reset := ansi.Reset
	var body []string
	body = c.appendSection(body, ansi.Wrap("❓ "+c.title, inner), ac+"%s"+reset, c.title != "")
	body = c.appendSection(body, wrapStyled(c.summary, inner, dim+"%s"+reset), "%s", c.summary != "")
	body = c.appendSection(body, ansi.Wrap(c.question, inner), "%s", c.question != "")
	body = c.appendOptions(body, inner, ac, reset)
	return body
}

// appendSection appends a (possibly empty) section, inserting a blank
// separator before non-empty content. fmt is applied to each line.
func (c *ClarifyCard) appendSection(body []string, lines []string, fmtStr string, nonEmpty bool) []string {
	if !nonEmpty || len(lines) == 0 {
		return body
	}
	if len(body) > 0 {
		body = append(body, "")
	}
	for _, l := range lines {
		body = append(body, sprintf(fmtStr, l))
	}
	return body
}

// appendOptions appends the numbered options list.
func (c *ClarifyCard) appendOptions(body []string, inner int, ac, reset string) []string {
	if len(c.options) == 0 {
		return body
	}
	if len(body) > 0 {
		body = append(body, "")
	}
	for i, opt := range c.options {
		label := fmt.Sprintf("%d. %s", i+1, opt)
		for _, l := range ansi.Wrap(label, inner) {
			body = append(body, ac+l+reset)
		}
	}
	return body
}

// wrapStyled wraps text and applies a printf-style style template to each line.
func wrapStyled(text string, inner int, fmtStr string) []string {
	wrapped := ansi.Wrap(text, inner)
	out := make([]string, len(wrapped))
	for i, l := range wrapped {
		out[i] = sprintf(fmtStr, l)
	}
	return out
}

// sprintf is fmt.Sprintf localized to avoid name clashes.
func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }
