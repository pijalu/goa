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

// mathCommandMap maps LaTeX command names (without backslash) to Unicode characters.
// Derived from entityMap by stripping $ delimiters, plus LaTeX shorthands.
var mathCommandMap map[string]string

func init() {
	mathCommandMap = make(map[string]string, len(entityMap)+4)
	for entity, char := range entityMap {
		if len(entity) >= 2 && entity[0] == '$' && entity[len(entity)-1] == '$' {
			name := entity[1 : len(entity)-1]
			mathCommandMap[name] = char
		}
	}
	// Add LaTeX shorthands not present in entityMap
	mathCommandMap["ge"] = "≥" // shorthand for \geq
	mathCommandMap["le"] = "≤" // shorthand for \leq
	mathCommandMap["ne"] = "≠" // shorthand for \neq
	mathCommandMap["to"] = "→" // shorthand for \to
}

// isLetter reports whether b is an ASCII letter.
func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// translateLaTeXMathContent processes the inner content of a $...$ math block
// (without the outer $ delimiters). It translates:
//   - \command → Unicode character (via mathCommandMap)
//   - \X (non-letter escape) → X (e.g., \% → %)
//   - All other text is preserved literally.
func translateLaTeXMathContent(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			out.WriteByte(s[i])
			continue
		}
		if isLetter(s[i+1]) {
			j := i + 2
			for j < len(s) && isLetter(s[j]) {
				j++
			}
			writeCommand(&out, s[i:j])
			i = j - 1
		} else {
			out.WriteByte(s[i+1])
			i++
		}
	}
	return out.String()
}

func writeCommand(out *strings.Builder, raw string) {
	cmdName := raw[1:]
	if char, ok := mathCommandMap[cmdName]; ok {
		out.WriteString(char)
	} else {
		out.WriteString(raw)
	}
}

func translateLatexMath(text string) string {
	var out strings.Builder
	pos := 0
	for pos < len(text) {
		idx := strings.Index(text[pos:], "$")
		if idx < 0 {
			break
		}
		idx += pos
		out.WriteString(text[pos:idx])

		closeIdx := findLatexMathClose(text, idx)
		if closeIdx < 0 {
			out.WriteByte('$')
			pos = idx + 1
			continue
		}

		inner := text[idx+1 : closeIdx]
		out.WriteString(translateLaTeXMathContent(inner))
		pos = closeIdx + 1
	}
	out.WriteString(text[pos:])
	return out.String()
}

func findLatexMathClose(text string, start int) int {
	if start+1 < len(text) && text[start+1] == '\\' {
		return nextDollarAfter(text, start+1)
	}
	nextDollar := strings.Index(text[start+1:], "$")
	if nextDollar <= 0 {
		return -1
	}
	between := text[start+1 : start+1+nextDollar]
	if !strings.Contains(between, "\\") {
		return -1
	}
	return nextDollarAfter(text, start+1)
}

func nextDollarAfter(text string, pos int) int {
	if idx := strings.Index(text[pos:], "$"); idx >= 0 {
		return idx + pos
	}
	return -1
}

// translateEntities replaces special entity patterns with their Unicode equivalents.
// Order of operations:
//  1. Process $\...$ LaTeX math blocks (e.g., $\ge 90\%$ → ≥ 90%)
//  2. Replace exact $entity$ patterns from entityMap (e.g., $rightarrow$ → →)
func translateEntities(text string) string {
	// First pass: handle LaTeX math blocks with $\...$
	text = translateLatexMath(text)
	// Second pass: handle exact $entity$ matches
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

// renderInlineBold replaces **text** with bold ANSI (gated by font styles).
func renderInlineBold(text string) string {
	var out strings.Builder
	pos := 0
	for {
		content, start, end, ok := findPair(text, "**", "**", pos)
		if !ok {
			break
		}
		out.WriteString(text[pos:start])
		out.WriteString(ansi.StyleBold())
		out.WriteString(content)
		out.WriteString(ansi.Reset)
		pos = end
	}
	out.WriteString(text[pos:])
	return out.String()
}

// renderInlineItalic replaces *text* and _text_ with italic ANSI (gated by
// font styles). It skips ** which was already handled by renderInlineBold.
func renderInlineItalic(text string) string {
	text = renderItalicMarker(text, '*')
	text = renderItalicMarker(text, '_')
	return text
}

// renderItalicMarker replaces marker-delimited spans (single * or _) with
// italic ANSI, skipping the bold double-marker form.
func renderItalicMarker(text string, marker byte) string {
	var out strings.Builder
	pos := 0
	for {
		idx := strings.IndexByte(text[pos:], marker)
		if idx < 0 {
			break
		}
		idx += pos
		// Skip the double (bold) marker — already processed by renderInlineBold.
		if idx+1 < len(text) && text[idx+1] == marker {
			out.WriteString(text[pos : idx+2])
			pos = idx + 2
			continue
		}
		if idx > 0 && text[idx-1] == marker {
			out.WriteString(text[pos : idx+1])
			pos = idx + 1
			continue
		}
		// Find the closing single marker.
		closeIdx := strings.IndexByte(text[idx+1:], marker)
		if closeIdx < 0 {
			break
		}
		closeIdx += idx + 1
		// Ensure closing is not part of a double marker.
		if closeIdx+1 < len(text) && text[closeIdx+1] == marker {
			out.WriteString(text[pos : closeIdx+2])
			pos = closeIdx + 2
			continue
		}
		content := text[idx+1 : closeIdx]
		out.WriteString(text[pos:idx])
		out.WriteString(ansi.StyleItalic())
		out.WriteString(content)
		out.WriteString(ansi.Reset)
		pos = closeIdx + 1
	}
	out.WriteString(text[pos:])
	return out.String()
}

// renderInlineStrikethrough replaces ~~text~~ with strikethrough ANSI (gated
// by font styles).
func renderInlineStrikethrough(text string) string {
	var out strings.Builder
	pos := 0
	for {
		content, start, end, ok := findPair(text, "~~", "~~", pos)
		if !ok {
			break
		}
		out.WriteString(text[pos:start])
		out.WriteString(ansi.StyleStrikethrough())
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
		out.WriteString(ansi.StyleUnderline() + color)
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