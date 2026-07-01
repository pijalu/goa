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
	borderHex := TheTheme.ColorHex("goa_panel_border")
	if borderHex == "" {
		borderHex = TheTheme.ColorHex("border_default")
	}
	bd := ansi.Fg(borderHex)
	accentHex := TheTheme.ColorHex("accent")
	if accentHex == "" {
		accentHex = TheTheme.ColorHex("selection_fg")
	}
	ac := ansi.Fg(accentHex)
	dim := ansi.Fg(TheTheme.ColorHex("system_msg"))
	reset := ansi.Reset

	// Inner content width: width minus two borders minus 2 padding cols.
	inner := width - 4
	if inner < 2 {
		inner = 2
	}

	var body []string
	// Title bar (accent).
	if c.title != "" {
		for _, l := range ansi.Wrap("❓ "+c.title, inner) {
			body = append(body, ac+l+reset)
		}
	}
	// Summary (dim).
	if c.summary != "" {
		if len(body) > 0 {
			body = append(body, "")
		}
		for _, l := range ansi.Wrap(c.summary, inner) {
			body = append(body, dim+l+reset)
		}
	}
	// Question (normal weight).
	if c.question != "" {
		if len(body) > 0 {
			body = append(body, "")
		}
		for _, l := range ansi.Wrap(c.question, inner) {
			body = append(body, l)
		}
	}
	// Options (numbered).
	if len(c.options) > 0 {
		if len(body) > 0 {
			body = append(body, "")
		}
		for i, opt := range c.options {
			label := fmt.Sprintf("%d. %s", i+1, opt)
			for _, l := range ansi.Wrap(label, inner) {
				body = append(body, ac+l+reset)
			}
		}
	}
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
