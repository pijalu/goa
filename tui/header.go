// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	_ "embed"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

//go:embed logo.ansi
var logoANSI string

// Header displays app information and keybinding hints at the top of the TUI.
// Header: logo + mascot + info, with responsive fallbacks.
type Header struct {
	appName string
	version string
	hints   string

	// Dynamic info rendered alongside the mascot
	skills    []string
	tools     []string
	modelName string
}

// NewHeader creates a Header component.
func NewHeader(appName, version string) *Header {
	h := &Header{
		appName: appName,
		version: version,
	}
	h.buildHints()
	return h
}

// SetSkills sets the list of loaded skill names.
func (h *Header) SetSkills(skills []string) { h.skills = skills }

// SetTools sets the list of registered tool names.
func (h *Header) SetTools(tools []string) { h.tools = tools }

// SetModel sets the active model name.
func (h *Header) SetModel(model string) { h.modelName = model }

func (h *Header) buildHints() {
	h.hints = "Ctrl+C/D exit  |  / commands  |  Tab complete  |  ↑↓ history"
}

// Render renders the header with mascot+logo on top and version/info below.
// The splash screen layout is:
//
//	<Mascot> <Logo>     (Band A — side by side when width allows)
//	<version details>   (Band B — full-width info lines below)
//
// Responsive fallbacks:
//   - Wide:   mascot + logo side by side, info below
//   - Medium: logo only (mascot hidden), info below
//   - Narrow: info only
func (h *Header) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	infoLines := h.buildInfoLines()
	logoLines := h.logoLines()
	mascotLines := strings.Split(goaMascot, "\n")

	// Band A: mascot + logo side by side (or logo only, or empty)
	bandA := h.renderMascotLogoBand(mascotLines, logoLines, width)

	// Band B: info lines below, each full-width
	bandB := h.padLines(infoLines, width)

	return append(bandA, bandB...)
}

// renderMascotLogoBand renders the top band with mascot + logo side by side.
// Returns only the mascot+logo rows (no info). Falls back to logo only or
// empty when the terminal is too narrow.
func (h *Header) renderMascotLogoBand(mascotLines, logoLines []string, width int) []string {
	logoW := maxLineWidth(logoLines)
	mascotW := maxLineWidth(mascotLines)

	const gap = 2

	switch {
	case mascotW+gap+logoW <= width:
		// Wide enough: mascot and logo side by side
		return h.renderSideBySide(mascotLines, logoLines, mascotW, width, gap)
	case logoW <= width:
		// Medium: logo only (mascot hidden)
		var lines []string
		for _, line := range logoLines {
			lines = append(lines, padToWidth(line, width))
		}
		return lines
	default:
		// Too narrow for any art
		return nil
	}
}

// padLines pads each line to the given width.
func (h *Header) padLines(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = padToWidth(line, width)
	}
	return out
}

func (h *Header) logoLines() []string {
	if logoANSI == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(logoANSI, "\n"), "\n")
}

// buildInfoLines assembles the right-column info text.
func (h *Header) buildInfoLines() []string {
	var lines []string
	logo := h.appName + " coding agent"
	if h.version != "" {
		logo += " v" + h.version
	}
	lines = append(lines, dimText(" "+logo))
	if h.hints != "" {
		lines = append(lines, dimText(" "+h.hints))
	}
	if len(h.skills) > 0 {
		lines = append(lines, dimText("   skills: "+strings.Join(h.skills, ", ")))
	}
	if len(h.tools) > 0 {
		lines = append(lines, dimText("   tools: "+strings.Join(h.tools, ", ")))
	}
	if h.modelName != "" {
		lines = append(lines, dimText("   model: "+h.modelName))
	}
	return lines
}

// maxLineWidth returns the longest visible width among lines.
func maxLineWidth(lines []string) int {
	w := 0
	for _, line := range lines {
		if lw := ansi.Width(line); lw > w {
			w = lw
		}
	}
	return w
}

// renderSideBySide places rightLines to the right of leftLines.
func (h *Header) renderSideBySide(leftLines, rightLines []string, leftW, width, gap int) []string {
	maxRows := max(len(leftLines), len(rightLines))
	var lines []string
	for i := 0; i < maxRows; i++ {
		left := ""
		if i < len(leftLines) {
			left = padToWidth(leftLines[i], leftW)
		} else {
			left = strings.Repeat(" ", leftW)
		}
		right := ""
		if i < len(rightLines) {
			right = rightLines[i]
		}
		lines = append(lines, padToWidth(left+strings.Repeat(" ", gap)+right, width))
	}
	return lines
}

// HandleInput is a no-op.
func (h *Header) HandleInput(data string) {}

// Invalidate is a no-op.
func (h *Header) Invalidate() {}

// goaMascot is the startup ANSI art (Goa mascot with embedded colors).
// Single string with ANSI SGR sequences — printed directly.
var goaMascot = "\x1b[49m          \x1b[38;5;80;49m\u2584\u2584\u2584\x1b[38;5;80;48;5;0m\u2584\u2584\x1b[48;5;80m      \x1b[38;5;80;48;5;0m\u2584\u2584\x1b[38;5;80;49m\u2584\u2584\x1b[38;5;0;49m\u2584\x1b[49m         \x1b[m\n" +
	"\x1b[49m  \x1b[38;5;0;49m\u2584\x1b[38;5;80;49m\u2584\u2584\u2584\x1b[38;5;0;49m\u2584\x1b[38;5;80;49m\u2584\x1b[48;5;80m \x1b[38;5;0;48;5;80m\u2584\x1b[38;5;15;48;5;80m\u2584\u2584\u2584\x1b[38;5;0;48;5;80m\u2584\x1b[48;5;80m      \x1b[38;5;15;48;5;80m\u2584\u2584\u2584\u2584\u2584\x1b[38;5;80;48;5;80m\u2584\x1b[48;5;80m \x1b[38;5;80;49m\u2584\x1b[38;5;80;48;5;0m\u2584\x1b[48;5;80m  \x1b[38;5;80;49m\u2584\x1b[38;5;0;49m\u2584\x1b[49m  \x1b[m\n" +
	"\x1b[49m \x1b[38;5;80;48;5;0m\u2584\x1b[48;5;80m \x1b[38;5;0;48;5;80m\u2584\u2584\x1b[38;5;80;48;5;0m\u2584\x1b[48;5;80m \x1b[38;5;15;48;5;80m\u2584\x1b[48;5;15m       \x1b[38;5;238;48;5;80m\u2584\x1b[48;5;80m  \x1b[38;5;15;48;5;80m\u2584\x1b[48;5;15m       \x1b[38;5;255;48;5;80m\u2584\x1b[48;5;80m  \x1b[38;5;80;48;5;0m\u2584\x1b[38;5;0;48;5;80m\u2584\x1b[48;5;80m  \x1b[49m  \x1b[m\n" +
	"\x1b[49m \x1b[38;5;0;48;5;80m\u2584\x1b[48;5;80m \x1b[38;5;80;48;5;0m\u2584\x1b[38;5;80;48;5;238m\u2584\x1b[48;5;80m  \x1b[48;5;15m \x1b[48;5;0m  \x1b[48;5;15m      \x1b[48;5;80m  \x1b[48;5;15m \x1b[48;5;0m \x1b[38;5;15;48;5;0m\u2584\x1b[48;5;15m      \x1b[48;5;80m   \x1b[38;5;0;48;5;80m\u2584\x1b[48;5;80m \x1b[38;5;0;48;5;80m\u2584\x1b[49m  \x1b[m\n" +
	"\x1b[49m  \x1b[49;38;5;0m\u2580\x1b[38;5;80;48;5;0m\u2584\x1b[48;5;80m   \x1b[38;5;0;48;5;15m\u2584\x1b[38;5;15;48;5;0m\u2584\u2584\x1b[48;5;15m     \x1b[38;5;80;48;5;0m\u2584\x1b[48;5;80m  \x1b[38;5;80;48;5;15m\u2584\x1b[48;5;15m \x1b[38;5;15;48;5;0m\u2584\x1b[48;5;15m     \x1b[38;5;80;48;5;0m\u2584\x1b[48;5;80m    \x1b[49m    \x1b[m\n" +
	"\x1b[49m   \x1b[48;5;80m     \x1b[38;5;80;48;5;15m\u2584\u2584\x1b[38;5;0;48;5;15m\u2584\u2584\x1b[38;5;80;48;5;15m\u2584\u2584\x1b[48;5;80m \x1b[38;5;0;48;5;80m\u2584\x1b[48;5;0m   \x1b[38;5;234;48;5;80m\u2584\x1b[38;5;80;48;5;15m\u2584\u2584\u2584\u2584\x1b[38;5;80;48;5;0m\u2584\x1b[48;5;80m      \x1b[38;5;0;49m\u2584\x1b[49m   \x1b[m\n" +
	"\x1b[49m   \x1b[48;5;80m           \x1b[48;5;223m      \x1b[38;5;223;48;5;236m\u2584\x1b[48;5;80m          \x1b[38;5;236;48;5;0m\u2584\x1b[49m   \x1b[m\n" +
	"\x1b[49m   \x1b[48;5;80m            \x1b[48;5;15m    \x1b[38;5;80;48;5;234m\u2584\x1b[48;5;80m            \x1b[49m   \x1b[m\n" +
	"\x1b[49m   \x1b[48;5;80m            \x1b[38;5;80;48;5;232m\u2584\x1b[38;5;80;48;5;15m\u2584\u2584\u2584\x1b[48;5;80m             \x1b[49m   \x1b[m\n"
