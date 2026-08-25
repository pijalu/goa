// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tools"
)

type EditConfig struct {
	tools.FileToolConfig `yaml:",inline"`
	// AllowFuzzOnEdits enables fuzzy matching for edit search/replace.
	// When true, the tool tries exact match first, then trailing whitespace
	// normalization, then full fuzzy whitespace + auto-reindent.
	// When false, only exact match (after CRLF normalization) is used.
	AllowFuzzOnEdits bool `yaml:"allow_fuzz_on_edits"`
}

// WriteConfig controls write tool behavior.
// Note: write does NOT support fuzzy filename matching — writing to the wrong
// path would cause irreversible data loss. The struct exists for future
// write-specific configuration options.
type WriteConfig struct{}

// PythonConfig controls the embedded gpython interpreter tool.
type PythonConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// Jail confines the embedded interpreter's `os` file API to the project
	// directory and below, matching the bash tool's jail. When false, file
	// operations resolve against the project directory but absolute paths
	// outside it are permitted.
	Jail bool `yaml:"jail"`
}

// RunCodeConfig controls the run_code code-mode dispatch tool (gap TL7).
type RunCodeConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// Jail confines the run_code program's own `os` file API to the project
	// directory and below, matching the python/bash jail. The worker is
	// jailed by default (nil = true); set false explicitly to allow the
	// program's own file operations outside the project. Sub-calls always
	// respect their own tools' jail configuration regardless of this flag.
	Jail *bool `yaml:"jail"`
	// MaxProgramBytes caps the submitted program length. Zero defaults to the
	// tool's built-in cap (200KB).
	MaxProgramBytes int `yaml:"max_program_bytes"`
	// MaxLogResultBytes caps the inline sub-call result bytes stored in the
	// durable dispatch log; oversized results spill to the dispatch spill dir.
	// Zero defaults to the tool's built-in cap (64KB).
	MaxLogResultBytes int `yaml:"max_log_result_bytes"`
}

// BashConfig controls bash tool behavior.
type BashConfig struct {
	BlockedCommands []string `yaml:"blocked_commands"`
	AllowedCommands []string `yaml:"allowed_commands"`
	EnvMaskPatterns []string `yaml:"env_mask_patterns"`
	// CompressOutput controls tool output compression for the bash tool.
	// nil = auto-detect based on provider (local=on, remote=off).
	// Explicit true/false overrides auto-detect.
	CompressOutput *bool `yaml:"compress_output,omitempty"`
	Jail           bool  `yaml:"jail"`
	// MaxOutputBytes caps the byte size of command output returned to the
	// agent. The tail of the output is kept. Zero defaults to 50KB.
	MaxOutputBytes int `yaml:"max_output_bytes"`
	// MaxCaptureBytes aborts commands whose combined stdout/stderr exceeds this
	// limit. Zero defaults to 10 MiB.
	MaxCaptureBytes int `yaml:"max_capture_bytes"`
	// MaxComplexityScore caps the AST complexity score at which a shell command
	// is considered too complex for reliable static analysis. Zero defaults to
	// the analyzer's conservative threshold (50).
	MaxComplexityScore int `yaml:"max_complexity_score"`
	// EnableComplexityAnalysis enables the AST-based complexity analyzer.
	// When enabled, the LLM is told to keep bash scripts simple and avoid
	// dynamic command construction. Disabled by default.
	EnableComplexityAnalysis bool `yaml:"enable_complexity_analysis"`
	// WarnFileEdits prepends a non-blocking hint to the output of shell commands
	// that modify project files (redirects, sed -i, interpreter inline writes),
	// steering the model to the edit tool. Never blocks. nil = enabled
	// (default); set false to silence the hint. Toggleable in /config.
	WarnFileEdits *bool `yaml:"warn_file_edits,omitempty"`
}

// SSHConfig controls SSH bash tool behavior.
type SSHConfig struct {
	Hosts []SSHHostConfig `yaml:"hosts"`
}

// SSHHostConfig configures a single SSH host.
type SSHHostConfig struct {
	ID      string `yaml:"id"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	User    string `yaml:"user"`
	KeyFile string `yaml:"key_file"`
}

// SearchConfig controls search tool behavior.
type SearchConfig struct {
	Threads    int      `yaml:"threads"`
	MaxResults int      `yaml:"max_results"`
	Exclude    []string `yaml:"exclude"`
}

// ModeLineSegmentConfig configures a single mode line segment or side.
type ModeLineSegmentConfig struct {
	Left  []string `yaml:"left,omitempty"`
	Right []string `yaml:"right,omitempty"`
}

// TUIConfig controls the terminal UI appearance and behavior.
type TUIConfig struct {
	Theme          string                `yaml:"theme"`
	Layout         internal.LayoutName   `yaml:"layout"`
	ShowTimestamps bool                  `yaml:"show_timestamps"`
	Transparency   TransparencyConfig    `yaml:"transparency"`
	ModeLine       ModeLineSegmentConfig `yaml:"modeline,omitempty"`
	Spinner        string                `yaml:"spinner"`
	// SpinnerLocation selects where the busy spinner renders: "chat" (default,
	// in-chat "⬣ Sending request..." line) or "statusbar" (suppress the chat
	// line; the animated frame shows only next to the model in the footer).
	SpinnerLocation string `yaml:"spinner_location,omitempty"`
	// AnimatedTitle animates the terminal window title with the spinner while
	// the agent is working. Default false (2026-07-21: keep the static
	// hexagon title during activities); set to true to opt in.
	AnimatedTitle *bool             `yaml:"animated_title,omitempty"`
	Tools         ToolDisplayConfig `yaml:"tools"`
	History       HistoryConfig     `yaml:"history"`
	FontStyles    FontStylesConfig  `yaml:"font_styles"`
}

// AnimatedTitleEnabled reports whether the animated title bar is enabled
// (default false unless explicitly enabled).
func (c TUIConfig) AnimatedTitleEnabled() bool {
	return c.AnimatedTitle != nil && *c.AnimatedTitle
}

// FontStylesConfig toggles SGR font-style support in rendered output
// (markdown, tool help). Italic is opt-out-capable because some terminals
// render it poorly; all styles default to enabled.
type FontStylesConfig struct {
	// Bold enables bold (\e[1m) for **text**. Default true.
	Bold *bool `yaml:"bold,omitempty"`
	// Italic enables italic (\e[3m) for *text* and _text_. Default true.
	Italic *bool `yaml:"italic,omitempty"`
	// Underline enables underline (\e[4m) for links. Default true.
	Underline *bool `yaml:"underline,omitempty"`
	// Strikethrough enables strikethrough (\e[9m) for ~~text~~. Default true.
	Strikethrough *bool `yaml:"strikethrough,omitempty"`
}

// BoldEnabled reports whether bold styling is enabled (default true).
func (f FontStylesConfig) BoldEnabled() bool { return f.Bold == nil || *f.Bold }

// ItalicEnabled reports whether italic styling is enabled (default true).
func (f FontStylesConfig) ItalicEnabled() bool { return f.Italic == nil || *f.Italic }

// UnderlineEnabled reports whether underline styling is enabled (default true).
func (f FontStylesConfig) UnderlineEnabled() bool { return f.Underline == nil || *f.Underline }

// StrikethroughEnabled reports whether strikethrough styling is enabled (default true).
func (f FontStylesConfig) StrikethroughEnabled() bool {
	return f.Strikethrough == nil || *f.Strikethrough
}

// HistoryConfig controls per-session input history loading and search.
type HistoryConfig struct {
	// MaxLoaded is the maximum number of input history entries loaded from
	// all session input history files on startup and session restore.
	// nil or 0 disables history loading entirely. Default is 100.
	MaxLoaded *int `yaml:"max_loaded"`
}

// ToolView is the default display mode for tool blocks in the chat.
type ToolView string

const (
	// ToolViewSummary shows a compact N-line preview of each tool call's
	// input/output (the default).
	ToolViewSummary ToolView = "summary"
	// ToolViewFull shows the complete input/output of every tool call.
	ToolViewFull ToolView = "full"
)

// ToolDisplayConfig controls how tool calls are rendered in the chat.
