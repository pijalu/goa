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
		"log_bg":       {Hex: "#101014"},
		"toolOutput":   {Hex: "#e6edf3"},
		"token_prompt": {Hex: "#7c5cfc"},
		"separator":    {Hex: "#30363d"},
	}}
	vars := ThemeVars(th)
	if len(vars) != 4 {
		t.Fatalf("vars = %+v, want only the 4 defined tokens", vars)
	}
	want := []CSSVar{
		{"--bg", "#101014"},
		{"--fg", "#e6edf3"},
		{"--accent", "#7c5cfc"},
		{"--border", "#30363d"},
	}
	for i, w := range want {
		if vars[i] != w {
			t.Errorf("vars[%d] = %+v, want %+v", i, vars[i], w)
		}
	}
}

func TestThemeVars_NilThemeIsEmpty(t *testing.T) {
	if got := ThemeVars(nil); got != nil {
		t.Errorf("ThemeVars(nil) = %+v, want nil", got)
	}
}

func TestThemeVars_CSSBlockIsDeterministic(t *testing.T) {
	th := &tui.Theme{Colors: map[string]tui.ColorToken{
		"log_bg":     {Hex: "#101014"},
		"toolOutput": {Hex: "#e6edf3"},
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
