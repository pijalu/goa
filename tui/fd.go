// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"strings"

	"github.com/pijalu/goa/internal/filefind"
)

// fdAvailable mirrors filefind.Available() and remains a package-level var
// so existing tests can force the os.ReadDir fallback exactly as before
// (`fdAvailable = false`). The search itself lives in internal/filefind;
// this file is the thin @-completer adapter that stamps the tui-specific
// Category and @/~/display decorations onto the engine's generic Entries.
var fdAvailable = filefind.Available()

// completionsFromEntries maps ranked filefind entries onto @-prefixed
// Completions: Value carries the raw path so replacePrefix inserts it, the
// display abbreviates $HOME with ~ for readability.
func completionsFromEntries(atPrefix string, entries []filefind.Entry) []Completion {
	home := os.Getenv("HOME")
	result := make([]Completion, 0, len(entries))
	for _, e := range entries {
		display := e.Path
		if home != "" && strings.HasPrefix(e.Path, home) {
			display = "~" + e.Path[len(home):]
		}
		result = append(result, Completion{
			Value:   atPrefix + e.Path,
			Display: atPrefix + display,
		})
	}
	return result
}

// rankFileCompletions orders candidates through the shared filefind engine
// (exact > case-sensitive prefix > case-insensitive prefix > fuzzy, shorter
// basenames first within a tier — Issue 8) and stamps the CatFiles category.
// Completions are carried over Entry paths, ranked, then rebuilt in ranked
// order (paths are unique among candidates).
func rankFileCompletions(comps []Completion, partial string) []Completion {
	entries := make([]filefind.Entry, len(comps))
	byPath := make(map[string]Completion, len(comps))
	for i, c := range comps {
		path := strings.TrimPrefix(c.Value, "@")
		entries[i] = filefind.Entry{Path: path}
		byPath[path] = c
	}
	ranked := filefind.Rank(entries, partial)
	out := make([]Completion, len(ranked))
	for i, e := range ranked {
		c := byPath[e.Path]
		c.Category = CatFiles
		out[i] = c
	}
	return out
}
