// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package internal contains shared types, enums, and utilities used across
// all Goa subsystems. This is the foundation package — every other chunk
// imports from here.
package internal

import "strings"

// ExecutionMode controls how the agent executes tool calls.
type ExecutionMode string

const (
	// ExecutionYolo executes all tool calls without confirmation.
	ExecutionYolo ExecutionMode = "yolo"
	// ExecutionSolo auto-executes tool calls constrained to the codebase.
	ExecutionSolo ExecutionMode = "solo"
	// ExecutionConfirm pauses before each tool call for user confirmation.
	ExecutionConfirm ExecutionMode = "confirm"
	// ExecutionReview queues editable writes for batch approval at turn end.
	ExecutionReview ExecutionMode = "review"
)

// ThemeName identifies a built-in or custom theme.
type ThemeName string

const (
	// ThemeDark is the default dark theme (GitHub-dark inspired).
	ThemeDark ThemeName = "dark"
	// ThemeLight is a light theme (GitHub-light inspired).
	ThemeLight ThemeName = "light"
	// ThemeCustom indicates a user-provided custom theme file.
	ThemeCustom ThemeName = "custom"
)

// LayoutName controls the arrangement of panes in the TUI.
type LayoutName string

const (
	// LayoutDefault is the standard layout: chat left, thinking+side right.
	LayoutDefault LayoutName = "default"
	// LayoutWide centers the chat with thinking left and side panel right.
	LayoutWide LayoutName = "wide"
	// LayoutMinimal shows only the chat pane (full width).
	LayoutMinimal LayoutName = "minimal"
	// LayoutDebug shows all panes with emphasis on logs and thinking.
	LayoutDebug LayoutName = "debug"
)

// SidePanelTab identifies which tab is active in the side panel.
type SidePanelTab string

const (
	// SidePanelFiles shows the project file tree.
	SidePanelFiles SidePanelTab = "files"
	// SidePanelMemory shows memory files.
	SidePanelMemory SidePanelTab = "memory"
	// SidePanelSkills shows available skills.
	SidePanelSkills SidePanelTab = "skills"
	// SidePanelPrompt shows the assembled system prompt.
	SidePanelPrompt SidePanelTab = "prompt"
)

// WorktreeMode controls git worktree creation behavior.
type WorktreeMode string

const (
	// WorktreeAlways creates a worktree for every session.
	WorktreeAlways WorktreeMode = "always"
	// WorktreeMultiAgent creates worktrees only in multi-agent mode.
	WorktreeMultiAgent WorktreeMode = "multi_agent"
)

// ConfirmResponse represents the user's decision in a confirmation dialog.
type ConfirmResponse int

const (
	// ConfirmYes executes the tool call.
	ConfirmYes ConfirmResponse = iota
	// ConfirmNo skips the tool call and returns an error to the agent.
	ConfirmNo
	// ConfirmAlways switches to yolo mode for the remainder of this turn.
	ConfirmAlways
	// ConfirmEdit allows the user to modify the tool input before execution.
	ConfirmEdit
)

// ConfirmRequest carries a tool confirmation request to the TUI modal.
type ConfirmRequest struct {
	ToolName     string
	ToolInput    string
	ResponseChan chan ConfirmResponse
}

// MajorMode represents the primary agent profile/role.
type MajorMode string

const (
	// MajorCoder is the default coding mode with full tool access.
	MajorCoder MajorMode = "coder"
	// MajorPlanner focuses on planning and architecture.
	MajorPlanner MajorMode = "planner"
	// MajorReviewer focuses on code review and quality assurance.
	MajorReviewer MajorMode = "reviewer"
)

// AutonomyLevel controls how autonomously the agent executes tool calls.
type AutonomyLevel string

const (
	// AutonomyYolo auto-executes all tool calls without confirmation.
	AutonomyYolo AutonomyLevel = "yolo"
	// AutonomyConfirm pauses before each tool call for user confirmation.
	AutonomyConfirm AutonomyLevel = "confirm"
	// AutonomyReview queues editable writes for batch approval at turn end.
	AutonomyReview AutonomyLevel = "review"
	// AutonomySolo auto-executes tool calls but constrains them to the
	// codebase directory and restricts git to commit/diff.
	AutonomySolo AutonomyLevel = "solo"
)

// ModeState is the canonical representation of the agent's current mode.
// It is a value type — all With* methods return new copies (immutable).
type ModeState struct {
	Major    MajorMode     `yaml:"major" json:"major"`
	Skills   []string      `yaml:"skills,omitempty" json:"skills,omitempty"`
	Autonomy AutonomyLevel `yaml:"autonomy" json:"autonomy"`
}

// String returns a human-readable mode representation.
// Examples: "coder+test-gen (yolo)", "planner (review)".
func (m ModeState) String() string {
	var b strings.Builder
	b.WriteString(string(m.Major))
	if len(m.Skills) > 0 {
		b.WriteString("+")
		b.WriteString(strings.Join(m.Skills, ","))
	}
	if m.Autonomy != "" {
		b.WriteString(" (")
		b.WriteString(string(m.Autonomy))
		b.WriteString(")")
	}
	return b.String()
}

// WithMajor returns a new ModeState with the given major.
func (m ModeState) WithMajor(major MajorMode) ModeState {
	m.Major = major
	return m
}

// WithSkills returns a new ModeState with the given skill stack (replaces).
// The input slice is copied to preserve immutability.
func (m ModeState) WithSkills(skills []string) ModeState {
	cpy := make([]string, len(skills))
	copy(cpy, skills)
	m.Skills = cpy
	return m
}

// WithAutonomy returns a new ModeState with the given autonomy.
func (m ModeState) WithAutonomy(a AutonomyLevel) ModeState {
	m.Autonomy = a
	return m
}

// AddSkill returns a new ModeState with the skill appended (if not already present).
func (m ModeState) AddSkill(skill string) ModeState {
	for _, s := range m.Skills {
		if s == skill {
			return m
		}
	}
	cpy := make([]string, len(m.Skills)+1)
	copy(cpy, m.Skills)
	cpy[len(m.Skills)] = skill
	m.Skills = cpy
	return m
}

// RemoveSkill returns a new ModeState with the skill removed.
func (m ModeState) RemoveSkill(skill string) ModeState {
	idx := -1
	for i, s := range m.Skills {
		if s == skill {
			idx = i
			break
		}
	}
	if idx < 0 {
		return m
	}
	cpy := make([]string, len(m.Skills)-1)
	copy(cpy, m.Skills[:idx])
	copy(cpy[idx:], m.Skills[idx+1:])
	m.Skills = cpy
	return m
}

// IsZero returns true if no major is set.
func (m ModeState) IsZero() bool {
	return m.Major == ""
}

// ThinkingLevel controls how much reasoning a model performs.
type ThinkingLevel string

const (
	// ThinkingLevelOff disables reasoning entirely.
	ThinkingLevelOff ThinkingLevel = "off"
	// ThinkingLevelMinimal requests very brief reasoning (~1k tokens).
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	// ThinkingLevelLow requests light reasoning (~2k tokens).
	ThinkingLevelLow ThinkingLevel = "low"
	// ThinkingLevelMedium requests moderate reasoning (~8k tokens).
	ThinkingLevelMedium ThinkingLevel = "medium"
	// ThinkingLevelHigh requests deep reasoning (~16k tokens).
	ThinkingLevelHigh ThinkingLevel = "high"
	// ThinkingLevelXHigh requests maximum reasoning (~32k tokens).
	ThinkingLevelXHigh ThinkingLevel = "xhigh"
)

// AllThinkingLevels returns all valid thinking levels in order.
func AllThinkingLevels() []ThinkingLevel {
	return []ThinkingLevel{ThinkingLevelOff, ThinkingLevelMinimal, ThinkingLevelLow, ThinkingLevelMedium, ThinkingLevelHigh, ThinkingLevelXHigh}
}

// IsValidThinkingLevel checks if a string is a valid thinking level.
func IsValidThinkingLevel(s string) bool {
	switch ThinkingLevel(s) {
	case ThinkingLevelOff, ThinkingLevelMinimal, ThinkingLevelLow, ThinkingLevelMedium, ThinkingLevelHigh, ThinkingLevelXHigh:
		return true
	}
	return false
}

// ReviewItem represents a single pending edit in review mode.
type ReviewItem struct {
	TurnID   int
	ToolName string
	FilePath string
	Diff     string
	Approved *bool // nil = pending, true = approved, false = rejected
}
