// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

func TestThemeVars_MapTokensToCSSProperties(t *testing.T) {
	th := &tui.Theme{Name: "goa-dark", Colors: map[string]tui.ColorToken{
		"log_bg":        {Hex: "#101014"},
		"assistant_msg": {Hex: "#e6edf3"},
		"toolOutput":    {Hex: "#8b949e"},
		"token_prompt":  {Hex: "#7c5cfc"},
		"separator":     {Hex: "#30363d"},
	}}
	vars := ThemeVars(th)
	if len(vars) != 5 {
		t.Fatalf("vars = %+v, want only the 5 defined tokens", vars)
	}
	want := []CSSVar{
		{"--bg", "#101014"},
		{"--fg", "#e6edf3"},
		{"--dim", "#8b949e"},
		{"--accent", "#7c5cfc"},
		{"--border", "#30363d"},
	}
	for i, w := range want {
		if vars[i] != w {
			t.Errorf("vars[%d] = %+v, want %+v", i, vars[i], w)
		}
	}
}

// TestThemeVars_DimIsNotTheThinkingColour pins the B10 mapping bug: `--dim`
// mirrored `token_thinking`, so anything that asked for the dim colour — the
// transcript container above all — rendered in the thinking purple.
func TestThemeVars_DimIsNotTheThinkingColour(t *testing.T) {
	th := &tui.Theme{Colors: map[string]tui.ColorToken{
		"toolOutput":     {Hex: "#8b949e"},
		"token_thinking": {Hex: "#8957e5"},
	}}
	for _, v := range ThemeVars(th) {
		if v.Name == "--dim" && v.Value == "#8957e5" {
			t.Fatalf("--dim mirrors the thinking colour: %+v", v)
		}
	}
}

// TestScrollbackDoesNotRecolourHistory pins the other half of B10: the
// transcript must resolve colours exactly like the live grid, which a colour
// declaration on its container breaks (runs the server sent without an explicit
// colour inherit it instead of the page's foreground).
func TestScrollbackDoesNotRecolourHistory(t *testing.T) {
	css, err := assets.ReadFile("assets/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	start := strings.Index(string(css), ".scrollback {")
	if start < 0 {
		t.Fatal("app.css has no .scrollback rule")
	}
	end := strings.Index(string(css)[start:], "}")
	if end < 0 {
		t.Fatal("unterminated .scrollback rule")
	}
	block := string(css)[start : start+end]
	if strings.Contains(block, "color:") {
		t.Errorf(".scrollback must not declare a colour (history renders like the screen): %q", block)
	}
}

func TestThemeVars_NilThemeIsEmpty(t *testing.T) {
	if got := ThemeVars(nil); got != nil {
		t.Errorf("ThemeVars(nil) = %+v, want nil", got)
	}
}

func TestThemeVars_CSSBlockIsDeterministic(t *testing.T) {
	th := &tui.Theme{Colors: map[string]tui.ColorToken{
		"log_bg":        {Hex: "#101014"},
		"assistant_msg": {Hex: "#e6edf3"},
	}}
	got := string(cssVarsText(ThemeVars(th)))
	want := ":root{--bg:#101014;--fg:#e6edf3;}"
	if got != want {
		t.Errorf("css block = %q, want %q", got, want)
	}
	if empty := string(cssVarsText(nil)); empty != "" {
		t.Errorf("empty css block = %q", empty)
	}
}

func TestThemeVars_RejectsNonHexValues(t *testing.T) {
	th := &tui.Theme{Colors: map[string]tui.ColorToken{
		"log_bg":     {Hex: "red"},
		"toolOutput": {Hex: "#ABC"},
		"separator":  {Hex: "#12"},
	}}
	vars := ThemeVars(th)
	if len(vars) != 1 || vars[0].Value != "#aabbcc" {
		t.Errorf("vars = %+v, want only the expanded #abc", vars)
	}
}
func TestThemeName(t *testing.T) {
	tests := []struct {
		theme *tui.Theme
		want  string
	}{
		{nil, "dark"},
		{&tui.Theme{Name: "goa-dark"}, "dark"},
		{&tui.Theme{Name: "Tokyo Light"}, "light"},
	}
	for _, tc := range tests {
		if got := ThemeName(tc.theme); got != tc.want {
			t.Errorf("ThemeName(%v) = %q, want %q", tc.theme, got, tc.want)
		}
	}
}

// The page must carry the TUI palette: a browser that renders with its own
// colours is exactly the drift the web UI exists to prevent.
func TestHTMLPage_RendersThemeCustomProperties(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := NewHTMLPage().Render(rec, PageData{
		SessionID: "abc",
		ThemeVars: []CSSVar{{Name: "--bg", Value: "#0a0a0c"}},
		Nonce:     "n0nce",
	}); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// The inline block carries the CSP nonce, so the served policy needs no
	// 'unsafe-inline' escape hatch.
	if !strings.Contains(body, `<style nonce="n0nce">:root{--bg:#0a0a0c;}</style>`) {
		t.Errorf("page is missing the theme variables:\n%s", body)
	}
	if !strings.Contains(body, `data-theme="dark"`) {
		t.Errorf("page is missing the theme attribute:\n%s", body)
	}
}
