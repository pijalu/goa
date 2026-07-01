// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// ── Inline rendering (pattern matching) ────────────────────────

// entityMap maps $entity_name$ patterns to their Unicode characters.
// These are commonly used in LLM-generated markdown for math symbols
// and special characters.
var entityMap = map[string]string{
	"$rightarrow$":     "→",
	"$leftarrow$":      "←",
	"$uparrow$":        "↑",
	"$downarrow$":      "↓",
	"$Rightarrow$":     "⇒",
	"$Leftarrow$":      "⇐",
	"$leftrightarrow$": "↔",
	"$Leftrightarrow$": "⇔",
	"$mapsto$":         "↦",
	"$times$":          "×",
	"$div$":            "÷",
	"$pm$":             "±",
	"$alpha$":          "α",
	"$beta$":           "β",
	"$gamma$":          "γ",
	"$delta$":          "δ",
	"$epsilon$":        "ε",
	"$zeta$":           "ζ",
	"$eta$":            "η",
	"$theta$":          "θ",
	"$iota$":           "ι",
	"$kappa$":          "κ",
	"$lambda$":         "λ",
	"$mu$":             "μ",
	"$nu$":             "ν",
	"$xi$":             "ξ",
	"$omicron$":        "ο",
	"$pi$":             "π",
	"$rho$":            "ρ",
	"$sigma$":          "σ",
	"$tau$":            "τ",
	"$upsilon$":        "υ",
	"$phi$":            "φ",
	"$chi$":            "χ",
	"$psi$":            "ψ",
	"$omega$":          "ω",
	"$Gamma$":          "Γ",
	"$Delta$":          "Δ",
	"$Theta$":          "Θ",
	"$Lambda$":         "Λ",
	"$Xi$":             "Ξ",
	"$Pi$":             "Π",
	"$Sigma$":          "Σ",
	"$Phi$":            "Φ",
	"$Psi$":            "Ψ",
	"$Omega$":          "Ω",
	"$infty$":          "∞",
	"$partial$":        "∂",
	"$nabla$":          "∇",
	"$exists$":         "∃",
	"$forall$":         "∀",
	"$in$":             "∈",
	"$notin$":          "∉",
	"$subset$":         "⊂",
	"$supset$":         "⊃",
	"$subseteq$":       "⊆",
	"$supseteq$":       "⊇",
	"$cup$":            "∪",
	"$cap$":            "∩",
	"$vee$":            "∨",
	"$wedge$":          "∧",
	"$oplus$":          "⊕",
	"$otimes$":         "⊗",
	"$sim$":            "∼",
	"$simeq$":          "≃",
	"$approx$":         "≈",
	"$equiv$":          "≡",
	"$neq$":            "≠",
	"$leq$":            "≤",
	"$geq$":            "≥",
	"$perp$":           "⊥",
	"$parallel$":       "∥",
	"$angle$":          "∠",
	"$sqrt$":           "√",
	"$therefore$":      "∴",
	"$because$":        "∵",
	"$cdot$":           "·",
	"$cdots$":          "⋯",
	"$ldots$":          "…",
	"$dots$":           "…",
	"$star$":           "★",
	"$diamond$":        "◇",
	"$square$":         "□",
	"$triangle$":       "△",
	"$checkmark$":      "✓",
	"$cross$":          "✗",
	"$circ$":           "∘",
	"$bullet$":         "•",
	"$degree$":         "°",
	"$prime$":          "′",
	"$dprime$":         "″",
	"$hellip$":         "…",
	"$ndash$":          "–",
	"$mdash$":          "—",
	"$lsquo$":          "‘",
	"$rsquo$":          "’",
	"$ldquo$":          "“",
	"$rdquo$":          "”",
	"$laquo$":          "«",
	"$raquo$":          "»",
	"$cent$":           "¢",
	"$pound$":          "£",
	"$euro$":           "€",
	"$yen$":            "¥",
	"$copy$":           "©",
	"$reg$":            "®",
	"$trade$":          "™",
	"$sect$":           "§",
	"$para$":           "¶",
	"$dagger$":         "†",
	"$Dagger$":         "‡",
}

// translateEntities replaces $entity_name$ patterns with their Unicode equivalents.
func translateEntities(text string) string {
	for entity, char := range entityMap {
		text = strings.ReplaceAll(text, entity, char)
	}
	return text
}

// renderInline converts markdown inline formatting to ANSI escape codes.
// It only formats COMPLETE constructs; incomplete ones at EOF are left as
// plain text. Processed in order: entities, escapes, code, bold, italic,
// strikethrough, links.
func renderInline(text string, theme *Theme) string {
	result := translateEntities(text)
	result = renderInlineEscapes(result)
	result = renderInlineCode(result, theme)
	result = renderInlineBold(result)
	result = renderInlineItalic(result)
	result = renderInlineStrikethrough(result)
	result = renderInlineLinks(result, theme)
	return result
}

// findPair locates the first complete delimiter pair in text starting from
// position start. It returns the content between the delimiters and the
// positions of the opening and closing delimiters. If no complete pair is
// found, found is false.
func findPair(text, open, close string, start int) (content string, openStart, closeEnd int, found bool) {
	openStart = strings.Index(text[start:], open)
	if openStart < 0 {
		return "", 0, 0, false
	}
	openStart += start
	afterOpen := openStart + len(open)
	closeStart := strings.Index(text[afterOpen:], close)
	if closeStart < 0 {
		return "", 0, 0, false
	}
	closeStart += afterOpen
	return text[afterOpen:closeStart], openStart, closeStart + len(close), true
}

// renderInlineCode replaces `code` spans with ANSI-colored backgrounds.
func renderInlineCode(text string, theme *Theme) string {
	bg := ansi.Bg(theme.ColorHex("code_bg"))
	fg := ansi.Fg(theme.ColorHex("code_fg"))
	if bg == ansi.Bg("") || bg == ansi.Bg("#888888") {
		bg = ansi.Bg("#21262d")
	}
	if fg == ansi.Fg("") || fg == ansi.Fg("#888888") {
		fg = ansi.Fg("#8b949e")
	}

	var out strings.Builder
	pos := 0
	for {
		content, start, end, ok := findPair(text, "`", "`", pos)
		if !ok {
			break
		}
		out.WriteString(text[pos:start])
		out.WriteString(bg + fg)
		out.WriteString(content)
		out.WriteString(ansi.Reset)
		pos = end
	}
	out.WriteString(text[pos:])
	return out.String()
}

// renderInlineBold replaces **text** with bold ANSI.
func renderInlineBold(text string) string {
	var out strings.Builder
	pos := 0
	for {
		content, start, end, ok := findPair(text, "**", "**", pos)
		if !ok {
			break
		}
		out.WriteString(text[pos:start])
		out.WriteString(ansi.Bold)
		out.WriteString(content)
		out.WriteString(ansi.Reset)
		pos = end
	}
	out.WriteString(text[pos:])
	return out.String()
}

// renderInlineItalic replaces *text* with italic ANSI.
// It skips ** which was already handled by renderInlineBold.
func renderInlineItalic(text string) string {
	var out strings.Builder
	pos := 0
	for {
		// Find a single * that is not preceded or followed by *
		idx := strings.Index(text[pos:], "*")
		if idx < 0 {
			break
		}
		idx += pos
		// Skip ** (bold markers already processed)
		if idx+1 < len(text) && text[idx+1] == '*' {
			out.WriteString(text[pos : idx+2])
			pos = idx + 2
			continue
		}
		if idx > 0 && text[idx-1] == '*' {
			out.WriteString(text[pos : idx+1])
			pos = idx + 1
			continue
		}
		// Find closing *
		closeIdx := strings.Index(text[idx+1:], "*")
		if closeIdx < 0 {
			break
		}
		closeIdx += idx + 1
		// Ensure closing is not part of **
		if closeIdx+1 < len(text) && text[closeIdx+1] == '*' {
			// This is an opening **, not a closing *
			out.WriteString(text[pos : closeIdx+2])
			pos = closeIdx + 2
			continue
		}
		content := text[idx+1 : closeIdx]
		out.WriteString(text[pos:idx])
		out.WriteString(ansi.Italic)
		out.WriteString(content)
		out.WriteString(ansi.Reset)
		pos = closeIdx + 1
	}
	out.WriteString(text[pos:])
	return out.String()
}

// renderInlineStrikethrough replaces ~~text~~ with faint ANSI.
func renderInlineStrikethrough(text string) string {
	var out strings.Builder
	pos := 0
	for {
		content, start, end, ok := findPair(text, "~~", "~~", pos)
		if !ok {
			break
		}
		out.WriteString(text[pos:start])
		out.WriteString(ansi.Faint)
		out.WriteString(content)
		out.WriteString(ansi.Reset)
		pos = end
	}
	out.WriteString(text[pos:])
	return out.String()
}

// renderInlineLinks replaces [text](url) with underlined text.
func renderInlineLinks(text string, theme *Theme) string {
	color := ansi.Fg(theme.ColorHex("user_msg"))
	if color == ansi.Fg("") || color == ansi.Fg("#888888") {
		color = ansi.Fg("#58a6ff")
	}

	var out strings.Builder
	pos := 0
	for {
		// Find [ — use ANSI-aware search to avoid mistaking [ inside
		// escape sequences (e.g. \x1b[1m from bold rendering) for a link.
		bracketOpen := ansi.FindNextUnescaped(text, "[", pos)
		if bracketOpen < 0 {
			break
		}

		// Find ] — ANSI-aware to handle edge cases where ] is inside
		// an escape sequence.
		bracketClose := ansi.FindNextUnescaped(text, "]", bracketOpen+1)
		if bracketClose < 0 {
			break
		}

		// Find (
		if bracketClose+1 >= len(text) || text[bracketClose+1] != '(' {
			out.WriteString(text[pos : bracketOpen+1])
			pos = bracketOpen + 1
			continue
		}

		// Find ) — ANSI-aware for the same reason.
		parenClose := ansi.FindNextUnescaped(text, ")", bracketClose+2)
		if parenClose < 0 {
			break
		}

		linkText := text[bracketOpen+1 : bracketClose]
		out.WriteString(text[pos:bracketOpen])
		out.WriteString(ansi.Underline + color)
		out.WriteString(linkText)
		out.WriteString(ansi.Reset)
		pos = parenClose + 1
	}
	out.WriteString(text[pos:])
	return out.String()
}

// renderInlineEscapes removes backslash escapes.
func renderInlineEscapes(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			out.WriteByte(text[i+1])
			i++
		} else {
			out.WriteByte(text[i])
		}
	}
	return out.String()
}
