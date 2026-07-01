// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
)

// Text is a Component that displays text with optional word wrapping and padding.
// Like pi's Text component — wraps to width, pads with spaces.
type Text struct {
	content    string
	padX       int
	padY       int
	nilIfEmpty bool
}

// NewText creates a Text component.
func NewText(content string, padX, padY int) *Text {
	return &Text{content: content, padX: padX, padY: padY}
}

// SetContent updates the text and invalidates.
func (t *Text) SetContent(content string) {
	t.content = content
}

// Render returns the text rendered to lines.
func (t *Text) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	if t.nilIfEmpty && t.content == "" {
		return nil
	}

	contentWidth := width - t.padX*2
	if contentWidth < 1 {
		contentWidth = 1
	}

	wrapped := wrapText(t.content, contentWidth)
	leftPad := strings.Repeat(" ", t.padX)
	emptyLine := strings.Repeat(" ", width)

	var result []string

	// Top padding
	for i := 0; i < t.padY; i++ {
		result = append(result, emptyLine)
	}

	// Content
	for _, line := range wrapped {
		padded := leftPad + line
		result = append(result, padToWidth(padded, width))
	}

	// Bottom padding
	for i := 0; i < t.padY; i++ {
		result = append(result, emptyLine)
	}

	return result
}

// HandleInput is a no-op.
func (t *Text) HandleInput(data string) {}

// Invalidate is a no-op.
func (t *Text) Invalidate() {}

// Spacer is a Component that adds N empty lines of vertical space.
type Spacer struct {
	lines int
}

// NewSpacer creates a Spacer.
func NewSpacer(n int) *Spacer {
	if n < 0 {
		n = 0
	}
	return &Spacer{lines: n}
}

// Render returns empty lines padded to width.
func (s *Spacer) Render(width int) []string {
	if s.lines <= 0 {
		return nil
	}
	line := strings.Repeat(" ", width)
	result := make([]string, s.lines)
	for i := range result {
		result[i] = line
	}
	return result
}

// HandleInput is a no-op.
func (s *Spacer) HandleInput(data string) {}

// Invalidate is a no-op.
func (s *Spacer) Invalidate() {}
