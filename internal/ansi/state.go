// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import (
	"fmt"
	"strings"
)

// AnsiState tracks active ANSI attributes (colors, bold, italic, hyperlinks)
// across wrapped lines. Tracks active ANSI codes across wrapped lines.
type AnsiState struct {
	bold, dim, italic, underline, blink, inverse, hidden, strikethrough bool
	fgColor, bgColor                                                    string // e.g. "38;5;196" or "48;2;255;0;0"
	hyperlinkURL                                                        string
}

// Process updates state from a single ANSI escape sequence.
func (a *AnsiState) Process(seq string) {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		if strings.HasPrefix(seq, "\x1b]8;") {
			a.processHyperlink(seq)
		}
		return
	}
	params := seq[2 : len(seq)-1]
	if params == "" {
		a.reset()
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		a.handleSGRCodeAt(parts, &i)
	}
}

// handleSGRCodeAt processes a single SGR parameter, consuming additional
// elements from parts (for color sequences like 38;5;N or 38;2;R;G;B).
func (a *AnsiState) handleSGRCodeAt(parts []string, i *int) {
	if fn, ok := sgrHandlers[parts[*i]]; ok {
		fn(a)
		return
	}
	// Color codes (38/48) need additional parameter consumption
	switch parts[*i] {
	case "38":
		a.fgColor = consumeColor(parts, i)
	case "48":
		a.bgColor = consumeColor(parts, i)
	}
}

// sgrHandlers maps SGR parameter codes to state mutations.
var sgrHandlers = map[string]func(*AnsiState){
	"0":  func(a *AnsiState) { a.reset() },
	"1":  func(a *AnsiState) { a.bold = true },
	"2":  func(a *AnsiState) { a.dim = true },
	"3":  func(a *AnsiState) { a.italic = true },
	"4":  func(a *AnsiState) { a.underline = true },
	"5":  func(a *AnsiState) { a.blink = true },
	"7":  func(a *AnsiState) { a.inverse = true },
	"8":  func(a *AnsiState) { a.hidden = true },
	"9":  func(a *AnsiState) { a.strikethrough = true },
	"21": func(a *AnsiState) { a.bold = false; a.dim = false },
	"22": func(a *AnsiState) { a.bold = false; a.dim = false },
	"23": func(a *AnsiState) { a.italic = false },
	"24": func(a *AnsiState) { a.underline = false },
	"25": func(a *AnsiState) { a.blink = false },
	"27": func(a *AnsiState) { a.inverse = false },
	"28": func(a *AnsiState) { a.hidden = false },
	"29": func(a *AnsiState) { a.strikethrough = false },
	"39": func(a *AnsiState) { a.fgColor = "" },
	"49": func(a *AnsiState) { a.bgColor = "" },
}

// processHyperlink handles OSC 8 hyperlink sequences.
// Format: ESC ] 8 ; params ; url BEL or ESC ] 8 ; params ; url ST
func (a *AnsiState) processHyperlink(seq string) {
	// Strip OSC prefix \x1b]8; and suffix \x07 or \x1b\\
	inner := seq[4:]
	if strings.HasSuffix(inner, "\x07") {
		inner = inner[:len(inner)-1]
	} else if strings.HasSuffix(inner, "\x1b\\") {
		inner = inner[:len(inner)-2]
	}
	// Split on first semicolon: params;url
	if idx := strings.IndexByte(inner, ';'); idx >= 0 {
		a.hyperlinkURL = inner[idx+1:]
	} else {
		// \x1b]8;;\x07 — close hyperlink
		a.hyperlinkURL = ""
	}
}

// GetActiveCodes returns the ANSI escape sequence to restore the current state.
func (a *AnsiState) GetActiveCodes() string {
	var parts []string
	a.appendActiveAttrs(&parts)
	if a.fgColor != "" {
		parts = append(parts, a.fgColor)
	}
	if a.bgColor != "" {
		parts = append(parts, a.bgColor)
	}
	if len(parts) == 0 {
		return ""
	}
	sgr := "\x1b[" + strings.Join(parts, ";") + "m"
	if a.hyperlinkURL == "" {
		return sgr
	}
	return fmt.Sprintf("\x1b]8;;%s\x07", a.hyperlinkURL) + sgr
}

func (a *AnsiState) appendActiveAttrs(parts *[]string) {
	if a.bold {
		*parts = append(*parts, "1")
	}
	if a.dim {
		*parts = append(*parts, "2")
	}
	if a.italic {
		*parts = append(*parts, "3")
	}
	if a.underline {
		*parts = append(*parts, "4")
	}
	if a.blink {
		*parts = append(*parts, "5")
	}
	if a.inverse {
		*parts = append(*parts, "7")
	}
	if a.hidden {
		*parts = append(*parts, "8")
	}
	if a.strikethrough {
		*parts = append(*parts, "9")
	}
}

// GetLineEndReset returns the reset code for attributes that bleed across lines
// (underline and hyperlink should be reset at line endings).
func (a *AnsiState) GetLineEndReset() string {
	// Underline and hyperlink need explicit reset at line ends
	var resets []string
	if a.underline {
		resets = append(resets, "\x1b[24m")
	}
	if a.hyperlinkURL != "" {
		resets = append(resets, "\x1b]8;;\x07")
	}
	return strings.Join(resets, "")
}

func (a *AnsiState) reset() {
	a.bold = false
	a.dim = false
	a.italic = false
	a.underline = false
	a.blink = false
	a.inverse = false
	a.hidden = false
	a.strikethrough = false
	a.fgColor = ""
	a.bgColor = ""
	a.hyperlinkURL = ""
}

// consumeColor reads a color specification starting from parts[idx].
// Supports 38;5;N (256 color) and 38;2;R;G;B (true color).
// Updates idx to point to the last consumed part.
func consumeColor(parts []string, idx *int) string {
	if *idx+1 >= len(parts) {
		return ""
	}
	sub := parts[*idx+1]
	switch sub {
	case "5":
		// 38;5;N or 48;5;N
		if *idx+2 < len(parts) {
			result := parts[*idx] + ";5;" + parts[*idx+2]
			*idx += 2
			return result
		}
	case "2":
		// 38;2;R;G;B or 48;2;R;G;B
		if *idx+4 < len(parts) {
			result := parts[*idx] + ";2;" + parts[*idx+2] + ";" + parts[*idx+3] + ";" + parts[*idx+4]
			*idx += 4
			return result
		}
	}
	return ""
}
