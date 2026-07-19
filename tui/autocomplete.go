// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"context"
	"os"
	"sort"
	"strings"
)

// Completer defines the tab completion interface.
type Completer interface {
	// Complete returns completion candidates for the given prefix.
	Complete(prefix string) []Completion
}

// CompCategory groups completion items by type for ordered display.
type CompCategory int

const (
	CatMostUsed CompCategory = iota // top frequent items
	CatCommand                      // base commands
	CatModifier                     // parameter variants
	CatHistory                      // history-search entries
)

// Completion represents a single completion candidate.
type Completion struct {
	Value       string       // text to insert
	Display     string       // display text (may differ from Value)
	Description string       // optional description
	Category    CompCategory // display grouping
	Score       int          // usage count (meaningful only for CatMostUsed)
}

// ArgCompletionsFunc provides argument completions for a command.
// cmdName is the command name (with leading slash), argPrefix is the partial
// argument text the user has typed so far.
type ArgCompletionsFunc func(cmdName, argPrefix string) []Completion

// CommandCompleter completes /command names and their arguments.
type CommandCompleter struct {
	commands     []string
	descriptions map[string]string  // command/alias → description
	argCompleter ArgCompletionsFunc // optional: completes args for known commands
	freqOrder    map[string]int     // command → usage count for frequency sorting
	minThreshold int                // min count for Most Used tier (0 = disable)
	maxMostUsed  int                // max items in Most Used tier
}

// NewCommandCompleter creates a CommandCompleter with command descriptions.
// commands is a list of command and alias names (with leading slash).
// descriptions maps command/alias names to short descriptions.
func NewCommandCompleter(commands []string, descriptions map[string]string) *CommandCompleter {
	if descriptions == nil {
		descriptions = make(map[string]string)
	}
	return &CommandCompleter{
		commands:     commands,
		descriptions: descriptions,
		minThreshold: 10,
		maxMostUsed:  3,
	}
}

// SetFreqOrder sets command usage frequencies for smart completion sorting.
func (c *CommandCompleter) SetFreqOrder(freqs map[string]int) {
	c.freqOrder = freqs
}

// SetMinThreshold sets the minimum usage count for the Most Used tier (0 = disable).
func (c *CommandCompleter) SetMinThreshold(n int) {
	c.minThreshold = n
}

// SetMaxMostUsed sets the maximum number of items in the Most Used tier.
func (c *CommandCompleter) SetMaxMostUsed(n int) {
	if n <= 0 {
		n = 3
	}
	c.maxMostUsed = n
}

// SetArgCompleter sets the function that provides argument completions.
func (c *CommandCompleter) SetArgCompleter(fn ArgCompletionsFunc) {
	c.argCompleter = fn
}

// Complete returns matching commands or argument completions for the given prefix.
// Results are categorized into Most Used, Commands, and Modifiers tiers.
func (c *CommandCompleter) Complete(prefix string) []Completion {
	// Colon-triggered arg completion: /cmd:argprefix
	// Only activates when the text before : is a /command, not regular text.
	colonIdx := strings.Index(prefix, ":")
	if colonIdx >= 0 && c.argCompleter != nil {
		cmdName := prefix[:colonIdx]
		if !strings.HasPrefix(cmdName, "/") {
			return nil
		}
		argPrefix := prefix[colonIdx+1:]
		results := c.argCompleter(cmdName, argPrefix)
		var expanded []Completion
		for _, r := range results {
			r.Value = cmdName + ":" + r.Value
			r.Display = r.Value
			r.Category = CatModifier
			expanded = append(expanded, r)
			// Level 2: try to get deeper completions
			rawVal := strings.TrimPrefix(r.Value, cmdName+":")
			deeper := c.argCompleter(cmdName, rawVal+":")
			for _, d := range deeper {
				d.Value = cmdName + ":" + d.Value
				d.Display = d.Value
				d.Category = CatModifier
				expanded = append(expanded, d)
			}
		}
		return expanded
	}

	// Find matching base commands
	matchedCmds := c.matchCommands(prefix)

	// Build three tiers
	mostUsed := c.buildMostUsed(matchedCmds, prefix)
	commands := c.buildCommands(matchedCmds, mostUsed)
	modifiers := c.buildModifiers(matchedCmds, mostUsed, prefix)

	// Sort Most Used by score (descending), keep other tiers in prefix-first order
	sortCompletions(mostUsed)

	// Concatenate: Most Used → Commands → Modifiers
	var result []Completion
	result = append(result, mostUsed...)
	result = append(result, commands...)
	result = append(result, modifiers...)
	return result
}

// matchCommands finds base commands matching the prefix.
// Returns prefix matches first, then fuzzy matches — each group sorted alphabetically.
func (c *CommandCompleter) matchCommands(prefix string) []string {
	var prefixMatched []string
	var fuzzyMatched []string
	seen := make(map[string]bool)

	for _, cmd := range c.commands {
		if seen[cmd] {
			continue
		}
		if strings.HasPrefix(cmd, prefix) {
			seen[cmd] = true
			prefixMatched = append(prefixMatched, cmd)
		} else if fuzzyMatch(prefix, cmd) {
			seen[cmd] = true
			fuzzyMatched = append(fuzzyMatched, cmd)
		}
	}

	return append(prefixMatched, fuzzyMatched...)
}

// buildMostUsed creates the Most Used tier from matched commands and their modifiers.
// Only includes items with frequency >= minThreshold. Disabled if minThreshold == 0.
func (c *CommandCompleter) buildMostUsed(matchedCmds []string, prefix string) []Completion {
	if c.minThreshold <= 0 || len(c.freqOrder) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var items []Completion
	for _, cmd := range matchedCmds {
		items = c.appendMostUsedForCommand(items, seen, cmd)
	}

	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})
	if len(items) > c.maxMostUsed {
		items = items[:c.maxMostUsed]
	}
	return items
}

// appendMostUsedForCommand adds qualifying completions for a single command.
func (c *CommandCompleter) appendMostUsedForCommand(items []Completion, seen map[string]bool, cmd string) []Completion {
	if score := c.freqOrder[cmd]; score >= c.minThreshold {
		items = appendIfNotSeen(items, seen, Completion{
			Value: cmd, Display: cmd, Description: c.descriptions[cmd],
			Category: CatMostUsed, Score: score,
		})
	}
	if c.argCompleter == nil {
		return items
	}
	for _, p := range c.argCompleter(cmd, "") {
		full := cmd + ":" + p.Value
		if score := c.freqOrder[full]; score >= c.minThreshold {
			items = appendIfNotSeen(items, seen, Completion{
				Value: full, Display: full, Description: p.Description,
				Category: CatMostUsed, Score: score,
			})
		}
		for _, n := range c.argCompleter(cmd, p.Value+":") {
			fullNested := cmd + ":" + n.Value
			if score := c.freqOrder[fullNested]; score >= c.minThreshold {
				items = appendIfNotSeen(items, seen, Completion{
					Value: fullNested, Display: fullNested, Description: n.Description,
					Category: CatMostUsed, Score: score,
				})
			}
		}
	}
	return items
}

// appendIfNotSeen adds item to items if its Value is not already in seen.
func appendIfNotSeen(items []Completion, seen map[string]bool, item Completion) []Completion {
	if seen[item.Value] {
		return items
	}
	seen[item.Value] = true
	return append(items, item)
}

// buildCommands creates the Commands tier (base commands not in mostUsed).
func (c *CommandCompleter) buildCommands(matchedCmds []string, mostUsed []Completion) []Completion {
	excluded := make(map[string]bool)
	for _, mu := range mostUsed {
		excluded[mu.Value] = true
	}

	var items []Completion
	for _, cmd := range matchedCmds {
		if excluded[cmd] {
			continue
		}
		items = append(items, Completion{
			Value:       cmd,
			Display:     cmd,
			Description: c.descriptions[cmd],
			Category:    CatCommand,
		})
	}
	return items
}

// buildModifiers creates the Modifiers tier (parameter variants not in mostUsed).
func (c *CommandCompleter) buildModifiers(matchedCmds []string, mostUsed []Completion, prefix string) []Completion {
	if c.argCompleter == nil {
		return nil
	}

	excluded := make(map[string]bool)
	for _, mu := range mostUsed {
		excluded[mu.Value] = true
	}

	var items []Completion
	for _, cmd := range matchedCmds {
		items = c.appendModifiersForCommand(items, excluded, cmd, prefix)
	}
	return items
}

// appendModifiersForCommand adds modifier completions for a single command.
// Prefix matches are added before fuzzy matches.
func (c *CommandCompleter) appendModifiersForCommand(items []Completion, excluded map[string]bool, cmd, prefix string) []Completion {
	var prefixItems []Completion
	var fuzzyItems []Completion

	for _, p := range c.argCompleter(cmd, "") {
		full := cmd + ":" + p.Value
		if excluded[full] {
			continue
		}
		if strings.HasPrefix(full, prefix) {
			prefixItems = append(prefixItems, Completion{
				Value: full, Display: full, Description: p.Description, Category: CatModifier,
			})
		} else if fuzzyMatch(prefix, full) {
			fuzzyItems = append(fuzzyItems, Completion{
				Value: full, Display: full, Description: p.Description, Category: CatModifier,
			})
		}
		for _, n := range c.argCompleter(cmd, p.Value+":") {
			fullNested := cmd + ":" + n.Value
			if excluded[fullNested] {
				continue
			}
			if strings.HasPrefix(fullNested, prefix) {
				prefixItems = append(prefixItems, Completion{
					Value: fullNested, Display: fullNested, Description: n.Description, Category: CatModifier,
				})
			} else if fuzzyMatch(prefix, fullNested) {
				fuzzyItems = append(fuzzyItems, Completion{
					Value: fullNested, Display: fullNested, Description: n.Description, Category: CatModifier,
				})
			}
		}
	}

	return append(items, append(prefixItems, fuzzyItems...)...)
}

func sortCompletions(result []Completion) {
	if len(result) <= 1 {
		return
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Value < result[j].Value
	})
}

// FileCompleter completes file paths using the filesystem.
type FileCompleter struct {
	workdir string
}

// NewFileCompleter creates a FileCompleter.
func NewFileCompleter(workdir string) *FileCompleter {
	return &FileCompleter{workdir: workdir}
}

// Complete returns file path completions for the given prefix.
// Only activates when prefix starts with @ (e.g., @src/main.go).
// Uses `fd` CLI for fast gitignore-aware search, falls back to os.ReadDir.
func (f *FileCompleter) Complete(prefix string) []Completion {
	if prefix == "" || !strings.HasPrefix(prefix, "@") {
		return nil
	}
	// Strip the @ prefix
	pathPrefix := prefix[1:]
	dir, partial := splitPathPrefix(pathPrefix)
	searchDir := resolveSearchDir(dir, f.workdir)

	home := os.Getenv("HOME")
	var result []Completion

	// Try fd first for fast, gitignore-aware search
	if fdAvailable {
		if comps := f.tryFdCompletion(searchDir, partial, home); comps != nil {
			return comps
		}
	}

	// Fallback: os.ReadDir
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		comp := fileCompletion(dir, partial, prefix, entry, home, f.workdir)
		if comp != nil {
			result = append(result, *comp)
		}
	}
	return result
}

func (f *FileCompleter) tryFdCompletion(searchDir, partial, home string) []Completion {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries, err := fdSearch(ctx, searchDir, partial, 50)
	if err != nil || len(entries) == 0 {
		return nil
	}
	result := make([]Completion, 0, len(entries))
	for _, entry := range entries {
		value := entry.Path
		display := entry.Path
		if home != "" && strings.HasPrefix(value, home) {
			display = "~" + value[len(home):]
		}
		result = append(result, Completion{
			Value:   "@" + value,
			Display: "@" + display,
		})
	}
	return result
}

func splitPathPrefix(prefix string) (dir, partial string) {
	if strings.Contains(prefix, "/") {
		idx := strings.LastIndex(prefix, "/")
		return prefix[:idx+1], prefix[idx+1:]
	}
	return "", prefix
}

func resolveSearchDir(dir, workdir string) string {
	searchDir := dir
	if !strings.HasPrefix(dir, "/") && workdir != "" {
		searchDir = workdir + "/" + dir
	}
	if searchDir == "" {
		return "."
	}
	return searchDir
}

func fileCompletion(dir, partial, origPrefix string, entry os.DirEntry, home string, workdir string) *Completion {
	name := entry.Name()
	if !strings.HasPrefix(name, partial) && !fuzzyMatch(partial, name) {
		return nil
	}
	// Build full path: use absolute or relative to workdir
	pathVal := dir + name
	if entry.IsDir() {
		pathVal += "/"
	}
	// Make relative to workdir for cleaner display
	value := pathVal
	display := pathVal
	if workdir != "" && strings.HasPrefix(pathVal, workdir+"/") {
		rel := pathVal[len(workdir)+1:]
		value = rel
		display = rel
		if entry.IsDir() {
			display += "/"
		}
	} else if home != "" && strings.HasPrefix(pathVal, home) {
		display = "~" + pathVal[len(home):]
	}
	// Prepend @ for completion value so replacePrefix keeps it
	return &Completion{Value: "@" + value, Display: "@" + display}
}

// CombinedCompleter combines multiple completers, removing duplicates.
type CombinedCompleter struct {
	completers []Completer
}

// NewCombinedCompleter creates a CombinedCompleter.
func NewCombinedCompleter(completers ...Completer) *CombinedCompleter {
	return &CombinedCompleter{completers: completers}
}

// Complete returns merged results from all completers (no duplicates).
func (c *CombinedCompleter) Complete(prefix string) []Completion {
	var result []Completion
	seen := make(map[string]bool)
	for _, comp := range c.completers {
		for _, item := range comp.Complete(prefix) {
			if !seen[item.Value] {
				seen[item.Value] = true
				result = append(result, item)
			}
		}
	}
	return result
}

// fuzzyMatch implements simple prefix + character-in-order fuzzy matching.
// Returns true if:
//   - pattern matches the START of candidate (prefix match on name part)
//   - OR all characters in pattern appear in order in candidate
//     (e.g., "md" matches "/mode")
//
// Does NOT do substring matching — "res" should not match "/compress"
// even though "compress" contains "res" as a substring.
func fuzzyMatch(pattern, candidate string) bool {
	patternLower := strings.ToLower(pattern)
	candLower := strings.ToLower(candidate)

	// Only fuzzy match on the name part after /, not the leading slash itself.
	namePart := candLower
	if strings.HasPrefix(candLower, "/") && len(candLower) > 1 {
		namePart = candLower[1:]
	}
	patternName := patternLower
	if strings.HasPrefix(patternLower, "/") && len(patternLower) > 1 {
		patternName = patternLower[1:]
	}

	// Minimum meaningful pattern length for fuzzy matching —
	// single-char patterns only do prefix match.
	if len(patternName) < 2 {
		return false
	}

	// Prefix match on the name part (not substring match).
	// "res" matches "restore" but NOT "compress".
	if strings.HasPrefix(namePart, patternName) {
		return true
	}

	// Character-in-order match (e.g., "md" matches "mode")
	pi := 0
	for _, c := range namePart {
		if pi < len(patternName) && c == rune(patternName[pi]) {
			pi++
		}
	}
	return pi == len(patternName)
}
