// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

// TestTheme_DarkTheme_RequiredTokens verifies that DarkTheme defines all required tokens.
func TestTheme_DarkTheme_RequiredTokens(t *testing.T) {
	theme := DarkTheme()
	if theme.Name != "dark" {
		t.Errorf("expected name 'dark', got %q", theme.Name)
	}
	for _, token := range RequiredColorTokens {
		if _, ok := theme.Colors[token]; !ok {
			t.Errorf("DarkTheme missing required token: %s", token)
		}
	}
}

// TestTheme_LightTheme_RequiredTokens verifies that LightTheme defines all required tokens.
func TestTheme_LightTheme_RequiredTokens(t *testing.T) {
	theme := LightTheme()
	if theme.Name != "light" {
		t.Errorf("expected name 'light', got %q", theme.Name)
	}
	for _, token := range RequiredColorTokens {
		if _, ok := theme.Colors[token]; !ok {
			t.Errorf("LightTheme missing required token: %s", token)
		}
	}
}

// TestTheme_Color_Fallback verifies that Color() returns a fallback for unknown tokens.
func TestTheme_Color_Fallback(t *testing.T) {
	theme := DarkTheme()
	c := theme.Color("nonexistent_token")
	if c == nil {
		t.Error("Color() should not return nil")
	}
}

// TestTheme_Style_Fallback verifies that Style() returns a fallback style for unknown tokens.
func TestTheme_Style_Fallback(t *testing.T) {
	theme := DarkTheme()
	style := theme.Style("nonexistent_token")
	rendered := style.Render("test")
	if rendered == "" {
		t.Error("Style() should render non-empty text")
	}
}

// TestTheme_DarkTheme_TokenValues verifies specific token values in DarkTheme.
func TestTheme_DarkTheme_TokenValues(t *testing.T) {
	theme := DarkTheme()

	tests := []struct {
		token string
		want  string
	}{
		{"status_bar_bg", "#161b22"},
		{"status_bar_fg", "#8b949e"},
		{"thinking_border", "#30363d"},
		{"thinking_text", "#8b949e"},
		{"thinking_header", "#58a6ff"},
		{"tool_running", "#d29922"},
		{"tool_success", "#3fb950"},
		{"tool_error", "#f85149"},
		{"toolTitle", "#ffffff"},
		{"bash_prompt", "#7dd3fc"},
		{"tool_pending_bg", "#2a3241"},
		{"tool_running_bg", "#4a422a"},
		{"tool_success_bg", "#2a3229"},
		{"tool_error_bg", "#392928"},
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			token, ok := theme.Colors[tt.token]
			if !ok {
				t.Fatalf("token %q not found", tt.token)
			}
			if token.Hex != tt.want {
				t.Errorf("expected hex %q, got %q", tt.want, token.Hex)
			}
		})
	}
}

// TestTheme_LightTheme_TokenValues verifies specific token values in LightTheme.
func TestTheme_LightTheme_TokenValues(t *testing.T) {
	theme := LightTheme()

	tests := []struct {
		token string
		want  string
	}{
		{"status_bar_bg", "#f6f8fa"},
		{"status_bar_fg", "#656d76"},
		{"thinking_border", "#d0d7de"},
		{"thinking_text", "#656d76"},
		{"thinking_header", "#0969da"},
		{"tool_running", "#9a6700"},
		{"tool_success", "#1a7f37"},
		{"tool_error", "#cf222e"},
		{"toolTitle", "#1f2328"},
		{"bash_prompt", "#0369a1"},
		{"tool_pending_bg", "#e0e7ff"},
		{"tool_running_bg", "#fef3c7"},
		{"tool_success_bg", "#dcfce7"},
		{"tool_error_bg", "#fee2e2"},
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			token, ok := theme.Colors[tt.token]
			if !ok {
				t.Fatalf("token %q not found", tt.token)
			}
			if token.Hex != tt.want {
				t.Errorf("expected hex %q, got %q", tt.want, token.Hex)
			}
		})
	}
}
