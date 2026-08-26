// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"time"

	"github.com/pijalu/goa/tools"
)

// FeaturesConfig holds opt-in feature gates. Every gate defaults to off so
// that enabling a feature is an explicit, reversible choice; the zero Config
// (and any config that omits the features block) keeps prior behavior.
type FeaturesConfig struct {
	// RemoteCompaction gates server-side conversation compaction
	// (Codex Phase 2b, POST /responses/compact). Tri-state: nil = inherit the
	// lower cascade layer (embedded default: off); an explicit true/false in a
	// home/project/local file overrides it, so the gate is reversible. Even
	// when a provider/model advertises the capability, the local compression
	// ladder runs unless this resolves true. Detection/gating only — no
	// request logic.
	RemoteCompaction *bool `yaml:"remote_compaction,omitempty"`

	// MultiAgentScrollbackReplay gates the multi-agent TUI's scrollback
	// replay on tab switch (plan T3). Tri-state: nil = inherit (embedded
	// default: off). When ON, switching to another agent's tab replays that
	// agent's committed transcript rows into the real terminal scrollback via
	// the dedicated ReplayRunner goroutine, so the switched-to agent has a
	// faithful, scrollable history. When OFF (default), a switch repaints only
	// the visible window (the T2 behavior) — committed rows of the target are
	// NOT re-emitted. The flag lets T3 land and be tested in isolation without
	// changing default behavior.
	MultiAgentScrollbackReplay *bool `yaml:"multi_agent_scrollback_replay,omitempty"`
}

// RemoteCompactionEnabled reports whether the remote-compaction gate resolves
// on (default false when unset at every cascade layer).
func (f FeaturesConfig) RemoteCompactionEnabled() bool {
	return f.RemoteCompaction != nil && *f.RemoteCompaction
}

// MultiAgentScrollbackReplayEnabled reports whether the multi-agent
// scrollback-replay gate resolves on (default false when unset at every
// cascade layer).
func (f FeaturesConfig) MultiAgentScrollbackReplayEnabled() bool {
	return f.MultiAgentScrollbackReplay != nil && *f.MultiAgentScrollbackReplay
}

type GoalsConfig struct {
	Retention GoalsRetentionConfig `yaml:"retention,omitempty"`
	// DoneGate selects how strictly model-initiated goal completion is
	// checked before the goal may close: "verify" (default) intercepts the
	// first complete request with a verification challenge when a completion
	// criterion is recorded; "evidence" requires the complete request to
	// carry the validation evidence as its reason in a single call; "off"
	// disables the gate. See docs/GOALS.md.
	DoneGate string `yaml:"done_gate,omitempty"`
	// VerifyCommands allows the done-gate to execute a goal's recorded
	// verify command after the model confirms completion (exit 0 = pass).
	// Nil = default (enabled); explicit false disables machine verification.
	VerifyCommands *bool `yaml:"verify_commands,omitempty"`
	// MaxVerifyFailures caps consecutive machine-verification failures
	// (verify command non-zero or judge FAIL) before the goal is auto-blocked
	// for user review. 0 = inherit default; -1 = no cap (not recommended).
	MaxVerifyFailures int `yaml:"max_verify_failures,omitempty"`
	// StallTurns is the number of consecutive continuation turns with no
	// measurable progress (todo transitions or workspace changes) before the
	// driver challenges the goal. 0 = inherit default; -1 = disable.
	StallTurns int `yaml:"stall_turns,omitempty"`
	// DefaultTurnBudget gives every new goal a hard turn ceiling. 0 = inherit
	// default; -1 = unlimited (goals run until they end or are stopped).
	DefaultTurnBudget int `yaml:"default_turn_budget,omitempty"`
	// Judge selects the independent completion auditor: "off" (default),
	// "same" (the active model), or "model:<id>" (a configured model).
	Judge string `yaml:"judge,omitempty"`
	// AutoUnblock controls whether a model-blocked goal (with justification)
	// automatically spawns an "unblocking" investigation goal in front of it,
	// forcing a search for solutions before the user is asked for guidance.
	// Nil = default (enabled); explicit false falls back to plain blocking.
	AutoUnblock *bool `yaml:"auto_unblock,omitempty"`
	// FreshContext selects the default context mode for newly created goals:
	// true (default) starts each goal on a clean context (objective + handover
	// only); false reuses the current conversation so its context survives
	// into the goal. The model-facing goal tool and /goal:new follow this
	// default unless overridden per goal (freshContext arg, /goal:new:fresh,
	// /goal:new:reuse). Nil = default (true).
	FreshContext *bool `yaml:"fresh_context,omitempty"`
	// VerifyTimeout bounds a single verify-command execution at goal
	// completion (e.g. "2m", "90s"). Empty/invalid = default (2m). The bound
	// is displayed to the user in the completion evidence (Bug A:
	// "the goal complete should have a clear timeout").
	VerifyTimeout string `yaml:"verify_timeout,omitempty"`
}

// VerifyCommandsEnabled reports whether machine verification runs (default true).
func (g GoalsConfig) VerifyCommandsEnabled() bool {
	return g.VerifyCommands == nil || *g.VerifyCommands
}

// AutoUnblockEnabled reports whether a justified block auto-spawns an
// unblocking investigation goal (default true).
func (g GoalsConfig) AutoUnblockEnabled() bool {
	return g.AutoUnblock == nil || *g.AutoUnblock
}

// FreshContextEnabled reports the default context mode for new goals
// (default true = clean context per goal; explicit false = reuse conversation).
func (g GoalsConfig) FreshContextEnabled() bool {
	return g.FreshContext == nil || *g.FreshContext
}

// AutoSaveModelEnabled reports whether a model switch pins active_provider /
// active_model into the project .goa/config.yaml (default true). An explicit
// execution.auto_save_model:false opts out, keeping legacy home-only
// persistence. Tri-state: nil inherits the lower cascade layer's value, so
// configs written before the key existed resolve to the embedded default.
func (e ExecutionConfig) AutoSaveModelEnabled() bool {
	return e.AutoSaveModel == nil || *e.AutoSaveModel
}

// VerifyTimeoutOr parses goals.verify_timeout, returning fallback when the
// value is empty or invalid.
func (g GoalsConfig) VerifyTimeoutOr(fallback time.Duration) time.Duration {
	if g.VerifyTimeout == "" {
		return fallback
	}
	if d, err := time.ParseDuration(g.VerifyTimeout); err == nil && d > 0 {
		return d
	}
	return fallback
}

// GoalsRetentionConfig controls how long terminal normal goals are kept.
// Enabled=false or Days=0 means "keep forever".
type GoalsRetentionConfig struct {
	Enabled bool `yaml:"enabled"`
	Days    int  `yaml:"days"`
}

// PlanConfig controls the durable plan subsystem.
type PlanConfig struct {
	Retention PlanRetentionConfig `yaml:"retention,omitempty"`
}

// PlanRetentionConfig controls how long terminal-state plans are kept.
// Enabled=false or Days=0 means "keep forever". Only plans with status
// done/blocked/failed are eligible for deletion.
type PlanRetentionConfig struct {
	Enabled bool `yaml:"enabled"`
	Days    int  `yaml:"days"`
}

// MemoryConfig controls persistent memory file settings.
type MemoryConfig struct {
	Enabled       bool        `yaml:"enabled"`
	Dir           string      `yaml:"dir"`
	AutoSummarize bool        `yaml:"auto_summarize"`
	Dream         DreamConfig `yaml:"dream,omitempty"`
}

// DreamConfig controls memory consolidation (dream mode).
type DreamConfig struct {
	Enabled          bool    `yaml:"enabled"`
	Auto             bool    `yaml:"auto"`
	Interval         string  `yaml:"interval,omitempty"`
	MinSessions      int     `yaml:"min_sessions,omitempty"`
	Model            string  `yaml:"model,omitempty"`
	Provider         string  `yaml:"provider,omitempty"`
	MaxTokens        int     `yaml:"max_tokens,omitempty"`
	Temperature      float64 `yaml:"temperature,omitempty"`
	OutputDir        string  `yaml:"output_dir,omitempty"`
	ConsolidatedDir  string  `yaml:"consolidated_dir,omitempty"`
	ApplyAfterReview bool    `yaml:"apply_after_review,omitempty"`
}

// SkillsConfig controls the skill system.
type SkillsConfig struct {
	Dirs          []string `yaml:"dirs"`
	Embedded      bool     `yaml:"embedded"`
	ExecutionMode string   `yaml:"execution_mode"` // "inline" (default) or "sub-agent"
	// Enabled is an allowlist of skill names to load. When non-empty, only the
	// listed skills are registered (from any source: embedded, file-based dirs,
	// plugin dirs). Empty means all skills are eligible. Applies at load time;
	// the system prompt is not rebuilt mid-session.
	Enabled []string `yaml:"enabled,omitempty"`
	// Disabled lists skill names to turn off (any source: embedded and
	// file-based). Disabled skills are not registered, so they never appear in
	// the system prompt listing, the skills banner, or the <available_skills>
	// catalog. A name in both Enabled and Disabled is disabled (explicit off
	// wins). Load-time only: the system prompt is not rebuilt mid-session.
	Disabled []string `yaml:"disabled,omitempty"`
	// EmbeddedEnabled re-enables individual embedded skills that are OFF by
	// default (all embedded skills except telegram). Unlike the
	// global Enabled allowlist — which gates EVERY source and would suppress
	// home/project/plugin file skills — this list is embedded-scoped: it only
	// opts embedded skills back in, leaving file-based skills untouched.
	// Load-time only.
	EmbeddedEnabled []string `yaml:"embedded_enabled,omitempty"`
	// Sticky forces the named knowledge skills sticky-on regardless of their
	// frontmatter (always-on: body persisted into every agent's history for
	// the whole session). Toggled via /skill:sticky and /config Skills; the
	// state is persisted at PROJECT level (skills.sticky in .goa/config.yaml)
	// so it sticks across sessions per project. Action-category skills and
	// disabled skills are never affected. Load-time only.
	Sticky []string `yaml:"sticky,omitempty"`
	// StickyOff overrides the frontmatter sticky:true of the named knowledge
	// skills off, so a skill shipped always-on can be turned back to on-demand
	// without editing its SKILL.md. A name in both Sticky and StickyOff is not
	// sticky (explicit off wins, mirroring Disabled). Project-level; load-time
	// only.
	StickyOff []string `yaml:"sticky_off,omitempty"`
}

// ToolsConfig holds tool-specific sub-configurations.
type ToolsConfig struct {
	Bash        BashConfig           `yaml:"bash"`
	Terminal    TerminalConfig       `yaml:"terminal"`
	SSH         SSHConfig            `yaml:"ssh"`
	Search      SearchConfig         `yaml:"search"`
	SmartSearch SmartSearchConfig    `yaml:"smartsearch"`
	Edit        EditConfig           `yaml:"edit"`
	Python      PythonConfig         `yaml:"python"`
	RunCode     RunCodeConfig        `yaml:"run_code"`
	ReadFile    tools.FileToolConfig `yaml:"read_file"`
	Write       WriteConfig          `yaml:"write"`
	WebFetch    tools.WebFetchConfig `yaml:"webfetch"`
	Enabled     ToolEnabledConfig    `yaml:"enabled"`
	// MaxInlineBytes caps the model-facing size of any single plain-text tool
	// result (gap CX2 spill policy). A result over the cap is saved verbatim
	// to ~/.goa/spill/<session>/ and replaced by a budgeted head/tail preview
	// plus a locator notice. Zero (the default) disables the policy entirely.
	MaxInlineBytes int `yaml:"max_inline_bytes"`
}

// SmartSearchConfig controls the smartsearch tool.
type SmartSearchConfig struct {
	Enabled     bool     `yaml:"enabled"`
	MaxResults  int      `yaml:"max_results"`
	MinScore    float64  `yaml:"min_score"`
	ExcludeDirs []string `yaml:"exclude_dirs"`
	K1          float64  `yaml:"k1"`
	B           float64  `yaml:"b"`
}

// TerminalConfig controls the hardened terminal tool.
type TerminalConfig struct {
	Sandbox TerminalSandboxConfig `yaml:"sandbox"`
}

// TerminalSandboxConfig controls sandboxing for the terminal tool.
type TerminalSandboxConfig struct {
	Enabled         bool     `yaml:"enabled"`
	BlockedCommands []string `yaml:"blocked_commands"`
	AllowedCommands []string `yaml:"allowed_commands"`
	TimeoutSeconds  int      `yaml:"timeout_seconds"`
	MaxOutputChars  int      `yaml:"max_output_chars"`
	BypassAllowed   bool     `yaml:"bypass_allowed"`
}

// ToolEnabledConfig controls which optional tools are registered and exposed
// to the model. Tools not listed here follow their built-in defaults.
