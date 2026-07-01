// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tui implements the ANSI-native terminal UI for Goa. It
// provides the theme system and console UI.
package tui

import (
	"fmt"
	"image/color"
	"strconv"
)

// ColorToken defines a themed color with optional text attributes.
type ColorToken struct {
	Hex    string `yaml:"hex"`
	Bold   bool   `yaml:"bold,omitempty"`
	Italic bool   `yaml:"italic,omitempty"`
	Faint  bool   `yaml:"faint,omitempty"`
}

// Theme holds all color tokens for the TUI.
type Theme struct {
	Name   string
	Colors map[string]ColorToken
}

// RequiredColorTokens lists all tokens that a theme must define.
var RequiredColorTokens = []string{
	"thinking_border", "thinking_text", "thinking_header", "thinking_pane_bg",
	"tool_running", "tool_success", "tool_error",
	"tool_pending_bg", "tool_running_bg", "tool_success_bg", "tool_error_bg",
	"toolTitle", "toolOutput", "warning", "error",
	"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
	"token_prompt", "token_thinking", "token_completion",
	"token_warning", "token_critical",
	"finish_stop", "finish_tool_calls", "finish_length",
	"companion_thinking_text", "companion_thinking_header",
	"user_msg", "user_msg_bg", "assistant_msg", "system_msg",
	"status_bar_bg", "status_bar_fg", "status_bar_highlight",
	"chat_bg", "sidebar_bg", "log_bg", "input_bg",
	"separator",
	"border_default", "border_focused", "selection_bg", "selection_fg",
	"goa_panel_bg", "goa_panel_border",
	"code_bg", "code_fg", "quote_fg", "heading_fg",
}

// Color returns the color for a token name, or a fallback gray.
func (t *Theme) Color(name string) color.Color {
	if token, ok := t.Colors[name]; ok {
		return parseHexColor(token.Hex)
	}
	return parseHexColor("#888888")
}

// Style returns a simple ANSI-styled string wrapper for the given token.
func (t *Theme) Style(name string) Styled {
	if token, ok := t.Colors[name]; ok {
		return Styled{hex: token.Hex, bold: token.Bold, italic: token.Italic, faint: token.Faint}
	}
	return Styled{hex: "#888888"}
}

// Styled wraps text with ANSI escape codes.
type Styled struct {
	hex    string
	bold   bool
	italic bool
	faint  bool
}

// Render returns the text wrapped in ANSI escape codes.
func (s Styled) Render(text string) string {
	return s.Prefix() + text + "\x1b[0m"
}

// Prefix returns the opening ANSI escape sequence without the reset.
func (s Styled) Prefix() string {
	r, g, b := hexToRGB(s.hex)
	var out string
	out = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
	if s.bold {
		out += "\x1b[1m"
	}
	if s.italic {
		out += "\x1b[3m"
	}
	if s.faint {
		out += "\x1b[2m"
	}
	return out
}

// ColorHex returns the hex color string for a token, or "" if not found.
func (t *Theme) ColorHex(name string) string {
	if token, ok := t.Colors[name]; ok {
		return token.Hex
	}
	return ""
}

// parseHexColor parses a hex color string into a color.Color.
func parseHexColor(hex string) color.Color {
	r, g, b := hexToRGB(hex)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

func hexToRGB(hex string) (r, g, b uint8) {
	hex = trimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 128, 128, 128
	}
	var rv, gv, bv uint8
	fmt.Sscanf(hex, "%02x%02x%02x", &rv, &gv, &bv)
	return rv, gv, bv
}

func trimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// TheTheme is the active theme used by the TUI. Set by main.go at startup.
var TheTheme = DarkTheme()

// DarkTheme returns the built-in dark theme (GitHub-dark inspired).
func DarkTheme() *Theme {
	return &Theme{
		Name: "dark",
		Colors: map[string]ColorToken{
			"thinking_border":           {Hex: "#30363d"},
			"thinking_text":             {Hex: "#8b949e", Italic: true},
			"thinking_header":           {Hex: "#58a6ff"},
			"thinking_pane_bg":          {Hex: "#0d1117"},
			"tool_running":              {Hex: "#d29922"},
			"tool_success":              {Hex: "#3fb950"},
			"tool_error":                {Hex: "#f85149"},
			"tool_pending_bg":           {Hex: "#2a3241"},
			"tool_running_bg":           {Hex: "#4a422a"},
			"tool_success_bg":           {Hex: "#2a3229"},
			"tool_error_bg":             {Hex: "#392928"},
			"toolTitle":                 {Hex: "#ffffff", Bold: true},
			"toolDiffAdded":             {Hex: "#b5bd68"},
			"toolDiffRemoved":           {Hex: "#cc6666"},
			"toolDiffContext":           {Hex: "#808080"},
			"bash_prompt":               {Hex: "#7dd3fc", Bold: true},
			"toolOutput":                {Hex: "#8b949e"},
			"warning":                   {Hex: "#d29922"},
			"error":                     {Hex: "#f85149"},
			"token_prompt":              {Hex: "#1f6feb"},
			"token_thinking":            {Hex: "#8957e5"},
			"token_completion":          {Hex: "#3fb950"},
			"token_warning":             {Hex: "#d29922"},
			"token_critical":            {Hex: "#f85149"},
			"finish_stop":               {Hex: "#3fb950"},
			"finish_tool_calls":         {Hex: "#d29922"},
			"finish_length":             {Hex: "#f85149"},
			"companion_thinking_text":   {Hex: "#6e7681", Italic: true},
			"companion_thinking_header": {Hex: "#8b949e"},
			"user_msg":                  {Hex: "#ececec", Bold: false},
			"user_msg_bg":               {Hex: "#343541"},
			"assistant_msg":             {Hex: "#c9d1d9"},
			"system_msg":                {Hex: "#8b949e", Italic: true},
			"code_bg":                   {Hex: "#21262d"},
			"code_fg":                   {Hex: "#8b949e"},
			"quote_fg":                  {Hex: "#8b949e"},
			"heading_fg":                {Hex: "#58a6ff"},
			"status_bar_bg":             {Hex: "#161b22"},
			"status_bar_fg":             {Hex: "#8b949e"},
			"status_bar_highlight":      {Hex: "#c9d1d9"},
			"separator":                 {Hex: "#21262d"},
			"separator_off":             {Hex: "#21262d"},
			"separator_minimal":         {Hex: "#7ee787"},
			"separator_low":             {Hex: "#58a6ff"},
			"separator_medium":          {Hex: "#8957e5"},
			"separator_high":            {Hex: "#d29922"},
			"separator_xhigh":           {Hex: "#f85149"},
			"chat_bg":                   {Hex: "#0d1117"},
			"sidebar_bg":                {Hex: "#161b22"},
			"log_bg":                    {Hex: "#0d1117"},
			"input_bg":                  {Hex: "#161b22"},
			"border_default":            {Hex: "#30363d"},
			"border_focused":            {Hex: "#1f6feb"},
			"selection_bg":              {Hex: "#1f6feb"},
			"selection_fg":              {Hex: "#c9d1d9"},
			"goa_panel_bg":              {Hex: "#11161d"},
			"goa_panel_border":          {Hex: "#30363d"},
		},
	}
}

// LightTheme returns the built-in light theme (GitHub-light inspired).
func LightTheme() *Theme {
	return &Theme{
		Name: "light",
		Colors: map[string]ColorToken{
			"thinking_border":           {Hex: "#d0d7de"},
			"thinking_text":             {Hex: "#656d76", Italic: true},
			"thinking_header":           {Hex: "#0969da"},
			"thinking_pane_bg":          {Hex: "#ffffff"},
			"tool_running":              {Hex: "#9a6700"},
			"tool_success":              {Hex: "#1a7f37"},
			"tool_error":                {Hex: "#cf222e"},
			"tool_pending_bg":           {Hex: "#e0e7ff"},
			"tool_running_bg":           {Hex: "#fef3c7"},
			"tool_success_bg":           {Hex: "#dcfce7"},
			"tool_error_bg":             {Hex: "#fee2e2"},
			"toolTitle":                 {Hex: "#1f2328", Bold: true},
			"toolDiffAdded":             {Hex: "#588458"},
			"toolDiffRemoved":           {Hex: "#aa5555"},
			"toolDiffContext":           {Hex: "#6c6c6c"},
			"bash_prompt":               {Hex: "#0369a1", Bold: true},
			"toolOutput":                {Hex: "#656d76"},
			"warning":                   {Hex: "#9a6700"},
			"error":                     {Hex: "#cf222e"},
			"token_prompt":              {Hex: "#0969da"},
			"token_thinking":            {Hex: "#8250df"},
			"token_completion":          {Hex: "#1a7f37"},
			"token_warning":             {Hex: "#9a6700"},
			"token_critical":            {Hex: "#cf222e"},
			"finish_stop":               {Hex: "#1a7f37"},
			"finish_tool_calls":         {Hex: "#9a6700"},
			"finish_length":             {Hex: "#cf222e"},
			"companion_thinking_text":   {Hex: "#656d76", Italic: true},
			"companion_thinking_header": {Hex: "#656d76"},
			"user_msg":                  {Hex: "#0969da", Bold: true},
			"user_msg_bg":               {Hex: "#ddf4ff"},
			"assistant_msg":             {Hex: "#1f2328"},
			"system_msg":                {Hex: "#656d76", Italic: true},
			"code_bg":                   {Hex: "#f6f8fa"},
			"code_fg":                   {Hex: "#656d76"},
			"quote_fg":                  {Hex: "#656d76"},
			"heading_fg":                {Hex: "#0969da"},
			"status_bar_bg":             {Hex: "#f6f8fa"},
			"status_bar_highlight":      {Hex: "#1f2328"},
			"separator":                 {Hex: "#d0d7de"},
			"separator_off":             {Hex: "#d0d7de"},
			"separator_minimal":         {Hex: "#1a7f37"},
			"separator_low":             {Hex: "#0969da"},
			"separator_medium":          {Hex: "#8250df"},
			"separator_high":            {Hex: "#9a6700"},
			"separator_xhigh":           {Hex: "#cf222e"},
			"status_bar_fg":             {Hex: "#656d76"},
			"chat_bg":                   {Hex: "#ffffff"},
			"sidebar_bg":                {Hex: "#f6f8fa"},
			"log_bg":                    {Hex: "#ffffff"},
			"input_bg":                  {Hex: "#f6f8fa"},
			"border_default":            {Hex: "#d0d7de"},
			"border_focused":            {Hex: "#0969da"},
			"selection_bg":              {Hex: "#0969da"},
			"selection_fg":              {Hex: "#1f2328"},
			"goa_panel_bg":              {Hex: "#f6f8fa"},
			"goa_panel_border":          {Hex: "#d0d7de"},
		},
	}
}

// RGBA returns the RGBA values of a color.Color as uint8 components.
func RGBA(c color.Color) (r, g, b, a uint8) {
	r32, g32, b32, a32 := c.RGBA()
	return uint8(r32 >> 8), uint8(g32 >> 8), uint8(b32 >> 8), uint8(a32 >> 8)
}

// ParseUint8 parses a uint8 from a string, returning 0 on error.
func ParseUint8(s string) uint8 {
	v, _ := strconv.ParseUint(s, 16, 8)
	return uint8(v)
}

// ThinkingLevelSeparatorColor returns the separator color for a thinking level.
// It prefers the dedicated level token (separator_<level>) and falls back to the
// neutral "separator" token so custom themes without the new tokens still work.
func ThinkingLevelSeparatorColor(level string) string {
	if level == "" {
		return TheTheme.ColorHex("separator")
	}
	key := "separator_" + level
	if c := TheTheme.ColorHex(key); c != "" {
		return c
	}
	return TheTheme.ColorHex("separator")
}
