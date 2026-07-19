// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

// FontStyles gates SGR font-style emission (bold/italic/underline/
// strikethrough) so a terminal that renders a style poorly (typically italic)
// can disable it. The zero value enables everything; call SetFontStyles at
// startup from config. It is process-global because the markdown/inline
// renderers are reached from many components that do not carry config.
type FontStyles struct {
	Bold, Italic, Underline, Strikethrough bool
}

// fontStyles is the active gate (default: all enabled).
var fontStyles = FontStyles{Bold: true, Italic: true, Underline: true, Strikethrough: true}

// SetFontStyles replaces the active font-style gate (called once at startup).
func SetFontStyles(f FontStyles) { fontStyles = f }

// ActiveFontStyles returns the current gate (for tests).
func ActiveFontStyles() FontStyles { return fontStyles }

// StyleBold returns the bold SGR when enabled, else "".
func StyleBold() string {
	if fontStyles.Bold {
		return Bold
	}
	return ""
}

// StyleItalic returns the italic SGR when enabled, else "".
func StyleItalic() string {
	if fontStyles.Italic {
		return Italic
	}
	return ""
}

// StyleUnderline returns the underline SGR when enabled, else "".
func StyleUnderline() string {
	if fontStyles.Underline {
		return Underline
	}
	return ""
}

// StyleStrikethrough returns the strikethrough SGR when enabled, else "".
func StyleStrikethrough() string {
	if fontStyles.Strikethrough {
		return Strikethrough
	}
	return ""
}

// StyleReset returns the reset appropriate after a styled span. When the
// corresponding style is disabled (so no SGR was emitted), it returns the
// specific attribute reset (harmless, keeps surrounding styles intact) rather
// than the full Reset — matching how Style* helpers may emit nothing.
func StyleReset() string { return Reset }
