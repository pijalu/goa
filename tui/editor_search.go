// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// ── History Search ──

// HistorySearcher implements Completer for history search.
// It matches history entries by case-insensitive substring.
type HistorySearcher struct {
	history []string // copy of editor history at search start
}

// NewHistorySearcher creates a searcher from the given history.
func NewHistorySearcher(history []string) *HistorySearcher {
	cp := make([]string, len(history))
	copy(cp, history)
	return &HistorySearcher{history: cp}
}

// Complete returns history entries matching the query by substring.
// Entries are returned newest-first (reverse of internal oldest-first order).
// An empty query returns the most recent entries (up to 10) so the search
// popup is populated as soon as history search is opened.
func (s *HistorySearcher) Complete(query string) []Completion {
	if query == "" {
		return s.recent(10)
	}
	lowerQuery := strings.ToLower(query)
	var matches []Completion
	seen := make(map[string]bool)
	// Iterate newest-first: history is oldest-first, so we go backwards
	for i := len(s.history) - 1; i >= 0; i-- {
		entry := s.history[i]
		if entry == "" {
			continue
		}
		if seen[entry] {
			continue
		}
		if strings.Contains(strings.ToLower(entry), lowerQuery) {
			matches = append(matches, Completion{Value: entry})
			seen[entry] = true
		}
	}
	return matches
}

// recent returns up to n most recent unique history entries, newest-first.
func (s *HistorySearcher) recent(n int) []Completion {
	var out []Completion
	seen := make(map[string]bool)
	for i := len(s.history) - 1; i >= 0 && len(out) < n; i-- {
		entry := s.history[i]
		if entry == "" || seen[entry] {
			continue
		}
		out = append(out, Completion{Value: entry})
		seen[entry] = true
	}
	return out
}

// toggleHistorySearch enters or exits history search mode.
// In search mode, the buffer text filters the history popup.
func (e *Editor) toggleHistorySearch() {
	if e.searchMode {
		e.exitSearchMode()
		return
	}
	e.enterSearchMode()
}

// enterSearchMode switches the completer to history search.
func (e *Editor) enterSearchMode() {
	if len(e.history) == 0 {
		return
	}
	e.searchMode = true
	e.searchSearcher = NewHistorySearcher(e.history)
	// Save current buffer as the initial search query
	if e.Text() == "" {
		e.searchQuery = ""
	} else {
		e.searchQuery = e.Text()
	}
	// Trigger the completion popup with the current query
	e.updateSearchCompletion()
}

// exitSearchMode restores the normal completer and clears the popup.
func (e *Editor) exitSearchMode() {
	e.searchMode = false
	e.searchSearcher = nil
	e.searchQuery = ""
	e.clearCompletion()
}

// updateSearchCompletion updates the completion popup with history matches
// for the current search query.
func (e *Editor) updateSearchCompletion() {
	if !e.searchMode || e.searchSearcher == nil {
		return
	}
	query := e.Text()
	comps := e.searchSearcher.Complete(query)
	if len(comps) == 0 {
		e.clearCompletion()
		return
	}
	e.compState.Phase = PhaseCommand // reuse existing popup rendering
	e.compState.Items = comps
	e.compState.Idx = 0
	e.compState.Prefix = query
	e.compState.UserNavigated = false
}

// cycleSearchMatch moves to the next older match in the search results.
func (e *Editor) cycleSearchMatch() {
	if !e.searchMode || !e.compState.Active() {
		return
	}
	e.compState.Cycle(1) // cycle to next item
}
