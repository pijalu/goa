// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"html/template"
	"strings"

	"github.com/pijalu/goa/tui"
)

// CSSVar is one CSS custom property injected into the page. The page defines
// sane defaults for every name in :root; the server overrides them with the
// colours the TUI is actually using, so the browser cannot drift from the
// terminal (spec §12.3: the palette comes from tui.TheTheme.ColorHex).
type CSSVar struct {
	Name  string
	Value string
}

// themeTokens maps each CSS custom property to the tui theme token it mirrors,
// in emission order. Keeping it an ordered slice (not a map) makes the rendered
// stylesheet deterministic, which is what the page test asserts.
//
// Tokens are chosen by ROLE, and that matters: `--dim` used to mirror
// `token_thinking` (the purple thinking blocks use) and `--fg` mirrored
// `toolOutput` (a dim grey), so "dim" text rendered purple and the page's
// foreground was the secondary colour (bugs.md B10). `--dim` now mirrors the
// theme's dim/secondary text and `--fg` its normal text.
var themeTokens = []struct {
	css   string
	token string
}{
	{"--bg", "log_bg"},
	{"--panel-bg", "sidebar_bg"},
	{"--fg", "assistant_msg"},
	{"--dim", "toolOutput"},
	{"--accent", "token_prompt"},
	{"--border", "separator"},
	{"--selection-bg", "selection_bg"},
	{"--selection-fg", "selection_fg"},
	{"--link", "tool_running"},
	{"--ok", "tool_success"},
	{"--error", "tool_error"},
	{"--warning", "warning"},
}

// ThemeVars derives the page's CSS custom properties from a TUI theme. Tokens
// the theme does not define are skipped, leaving the stylesheet's own default
// in place — a missing token must never render as an empty/invalid colour.
func ThemeVars(th *tui.Theme) []CSSVar {
	if th == nil {
		return nil
	}
	out := make([]CSSVar, 0, len(themeTokens))
	for _, m := range themeTokens {
		hex := normalizeHex(th.ColorHex(m.token))
		if hex == "" {
			continue
		}
		out = append(out, CSSVar{Name: m.css, Value: hex})
	}
	return out
}

// normalizeHex accepts only #rgb / #rrggbb and returns the value in
// six-digit lowercase form. Anything else (including a theme file with a
// non-colour token) is dropped, so a value that reaches the stylesheet is
// always a plain colour — the page never emits a declaration the browser
// would have to parse creatively.
func normalizeHex(hex string) string {
	h := strings.ToLower(strings.TrimSpace(hex))
	switch len(h) {
	case 4:
		if h[0] != '#' {
			return ""
		}
		for _, c := range h[1:] {
			if !isHexDigit(c) {
				return ""
			}
		}
		return "#" + string([]byte{h[1], h[1], h[2], h[2], h[3], h[3]})
	case 7:
		if h[0] != '#' {
			return ""
		}
		for _, c := range h[1:] {
			if !isHexDigit(c) {
				return ""
			}
		}
		return h
	}
	return ""
}

func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// ThemeName normalises a theme name to the "dark"/"light" attribute the page
// expects. Anything unrecognised is dark, the default palette.
func ThemeName(th *tui.Theme) string {
	if th != nil && strings.Contains(strings.ToLower(th.Name), "light") {
		return "light"
	}
	return "dark"
}

// cssVarsText renders the variables as a ":root{…}" declaration block. The
// values are validated hex colours taken from the theme, and the result is
// typed template.CSS so html/template emits it verbatim instead of refusing to
// render a stylesheet it cannot prove safe.
func cssVarsText(vars []CSSVar) template.CSS {
	if len(vars) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(":root{")
	for _, v := range vars {
		b.WriteString(v.Name)
		b.WriteByte(':')
		b.WriteString(normalizeHex(v.Value))
		b.WriteByte(';')
	}
	b.WriteString("}")
	return template.CSS(b.String())
}
