// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// ── Completion ──

func (e *Editor) triggerCompletion() {
	if e.completer == nil {
		e.insertString("  ")
		return
	}
	prefix := e.currentPrefix()
	comps := e.completer.Complete(prefix)
	if len(comps) == 0 {
		return
	}
	if len(comps) == 1 {
		e.pushUndo()
		e.replacePrefix(prefix, comps[0].Value)
		return
	}
	e.compState.Phase = PhaseCommand
	e.compState.Items = comps
	e.compState.Idx = 0
	e.compState.Prefix = prefix
	e.compState.UserNavigated = false
}

func (e *Editor) acceptCompletion() {
	if !e.compState.Active() {
		return
	}
	sel := e.compState.Selected()
	if sel == nil {
		return
	}
	e.pushUndo()
	e.replacePrefix(e.compState.Prefix, sel.Value)
	e.clearCompletion()
	if e.searchMode {
		e.exitSearchMode()
	}
}

// acceptAndRecomplete fills the selected completion into the buffer and
// immediately re-triggers completion so the user sees further candidates.
// Used by Tab (Enter closes the popup instead).
func (e *Editor) acceptAndRecomplete() {
	if !e.compState.Active() {
		return
	}
	sel := e.compState.Selected()
	if sel == nil {
		return
	}
	e.pushUndo()
	e.replacePrefix(e.compState.Prefix, sel.Value)
	e.updateAutoComp()
}

func (e *Editor) cycleCompletion(delta int) {
	if !e.compState.Active() {
		return
	}
	e.compState.Cycle(delta)
}

func (e *Editor) clearCompletion() {
	e.compState.Clear()
}

// currentPrefix extracts the completion prefix from the text before the cursor.
// 3-tier extraction for tab completion:
//  1. Unclosed quote (`"`): capture from quote start, include trailing `@`
//  2. `@` prefix: capture from `@` (or `@"`)
//  3. Path delimiter: capture from last ` \t\n'=`
func (e *Editor) currentPrefix() string {
	text := string(e.buf[:e.pos])
	if text == "" {
		return ""
	}

	// Tier 1: Check for unclosed quote
	if quoteStart := findUnclosedQuote(text); quoteStart >= 0 {
		// If preceded by @, include it (e.g., @"path with spaces")
		if quoteStart > 0 && text[quoteStart-1] == '@' {
			if isTokenStart(text, quoteStart-1) {
				return text[quoteStart-1:]
			}
		}
		if isTokenStart(text, quoteStart) {
			return text[quoteStart:]
		}
	}

	// Tier 2: If cursor is at or after a `@` that starts a token, complete from there
	if atIdx := strings.LastIndex(text, "@"); atIdx >= 0 && isTokenStart(text, atIdx) {
		return text[atIdx:]
	}

	// Tier 3: Extract from last path delimiter
	last := strings.LastIndexAny(text, " \t\n'=")
	if last >= 0 {
		return text[last+1:]
	}
	return text
}

// findUnclosedQuote scans for an unclosed double-quote in text.
// Returns the position of the opening quote, or -1 if all quotes are closed.
func findUnclosedQuote(text string) int {
	inQuotes := false
	quoteStart := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteStart = i
			}
		}
	}
	if inQuotes {
		return quoteStart
	}
	return -1
}

// isTokenStart returns true if the position at `idx` is the start of a token.
// A token starts at the beginning of the text or after a delimiter.
func isTokenStart(text string, idx int) bool {
	if idx <= 0 {
		return true
	}
	ch := text[idx-1]
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '"' || ch == '\'' || ch == '=' || ch == '(' || ch == '['
}

func (e *Editor) replacePrefix(oldPrefix, newValue string) {
	text := string(e.buf)
	prefixLen := len([]rune(oldPrefix))
	before := e.pos - prefixLen
	if before < 0 {
		before = 0
	}
	runes := []rune(text)
	newText := string(runes[:before]) + newValue + string(runes[e.pos:])
	e.buf = []rune(newText)
	e.pos = before + len([]rune(newValue))
}