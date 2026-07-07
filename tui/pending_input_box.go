// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// PendingInputBox renders the active main-input prompt (e.g. "Describe the
// issue (optional), then press Enter:") as a single static line in the base
// layer tree.
//
// Unlike a chat system message, it is NOT owned by ChatViewport, so it stays
// visible across tab switches even when the chat is suppressed (Stats/agent
// tabs during orchestration). Unlike the editor title, it renders the prompt
// as a dedicated, prominent line rather than a bordered label.
//
// It returns nil (zero height) when no prompt is pending, so it does not shift
// the layout when idle. The line is plain (no spinner animation): a pending
// input is waiting for the user, not processing.
type PendingInputBox struct {
	prompt string
}

// NewPendingInputBox returns an empty (invisible) box.
func NewPendingInputBox() *PendingInputBox { return &PendingInputBox{} }

// SetPrompt sets the prompt text to display. An empty string clears the box.
func (p *PendingInputBox) SetPrompt(s string) { p.prompt = s }

// Clear removes the prompt, making the box render nothing.
func (p *PendingInputBox) Clear() { p.prompt = "" }

// Prompt returns the current prompt text ("" when none).
func (p *PendingInputBox) Prompt() string { return p.prompt }

// Render returns a single muted line showing the prompt, or nil when empty.
func (p *PendingInputBox) Render(width int) []string {
	if p.prompt == "" || width <= 0 {
		return nil
	}
	line := " " + padToWidth(ansiMuted(p.prompt), width-1)
	return []string{line}
}

// HandleInput is a no-op (display only).
func (p *PendingInputBox) HandleInput(string) {}

// Invalidate is a no-op (state is the prompt string itself).
func (p *PendingInputBox) Invalidate() {}
