// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// SelectorItem represents an option in a selection list.
// When AnimationFrames is set and this item is selected, the selector
// cycles through the frames on AnimationInterval to show a live preview.
type SelectorItem struct {
	Value             string
	Label             string
	Description       string
	Color             string        // optional: hex color for the label (empty = default)
	AnimationFrames   []string      // optional: animation frames (e.g., spinner preview)
	AnimationInterval time.Duration // time between animation frames
}

// Selector is a Component that shows a searchable list of options.
// When shown, the user can:
//   - Type to filter
//   - Up/Down to navigate
//   - Enter to select
//   - Escape to cancel
//   - Backspace/Delete (on a non-menu item) to trigger deletion
//
// The result is delivered through a channel.
//
// Concurrency: the commandLoop is the sole owner of Selector state.
// HandleInput, SetItems, Render and the animation-frame advance all run on
// the loop; the animation goroutine forwards each tick back to the loop via
// TUI.Apply. No mutex is required.
type Selector struct {
	Container

	title        string
	searchText   string
	items        []SelectorItem
	filtered     []SelectorItem
	selected     int
	currentValue string // the currently active option value (shown with ✓ marker)

	result chan string // delivers the selected value (empty on cancel)
	done   func()      // restores the editor

	tui *TUI // for requesting re-renders during animation

	// Animation state for the currently selected item
	animFrames    []string
	animInterval  time.Duration
	animIdx       int
	animTicker    *time.Ticker
	animStop      chan struct{}
	animItemValue string
	focused       bool
}

// NewSelector creates a Selector. Items are sorted alphabetically by Label.
// currentValue is the currently active option (shown with a ✓ marker).
// The result channel receives the selected value when the user confirms,
// or "" if cancelled.
func NewSelector(title string, items []SelectorItem, currentValue string, result chan string) *Selector {
	sorted := make([]SelectorItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Label) < strings.ToLower(sorted[j].Label)
	})

	s := &Selector{
		title:        title,
		items:        sorted,
		filtered:     sorted,
		selected:     findItemIndex(sorted, currentValue),
		currentValue: currentValue,
		result:       result,
	}
	return s
}

// SetDone sets the callback that restores the editor when selection ends.
func (s *Selector) SetDone(fn func()) { s.done = fn }

// SetTUI stores the TUI reference for triggering re-renders on animation.
func (s *Selector) SetTUI(t *TUI) { s.tui = t }

// SetItems replaces the options and resets filter, preserving sorting.
func (s *Selector) SetItems(items []SelectorItem) {
	sorted := make([]SelectorItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Label) < strings.ToLower(sorted[j].Label)
	})
	s.items = sorted
	s.filtered = sorted
	s.selected = findItemIndex(sorted, s.currentValue)
}

func findItemIndex(items []SelectorItem, value string) int {
	for i, item := range items {
		if item.Value == value {
			return i
		}
	}
	return 0
}

// HandleInput processes navigation and selection keys. Runs on the
// commandLoop (sole owner); emit runs inline after the state mutation in the
// same step — there is no lock to release first.
func (s *Selector) HandleInput(data string) {
	emitVal := s.dispatchInput(data)
	if emitVal != nil {
		s.emit(*emitVal)
	}
}

// dispatchInput mutates selector state in response to a key and returns the
// value to emit (nil if none). Caller must hold s.mu.
func (s *Selector) dispatchInput(data string) *string {
	if v := s.handlePrintable(data); v != nil {
		return v
	}
	if v := s.handleBackspace(data); v != nil {
		return v
	}
	if v := s.handleDelete(data); v != nil {
		return v
	}
	if v := s.handleNav(data); v != nil {
		return v
	}
	if v := s.handleSelect(data); v != nil {
		return v
	}
	return s.handleCancel(data)
}

func (s *Selector) handleDelete(data string) *string {
	if !(matchesKey(data, KeyBackspace) && len(s.searchText) == 0) && !matchesKey(data, KeyDelete) {
		return nil
	}
	if len(s.filtered) == 0 {
		return nil
	}
	item := s.filtered[s.selected]
	if strings.HasPrefix(item.Value, "__") {
		return nil
	}
	v := "__delete__" + item.Value
	return &v
}

func (s *Selector) handlePrintable(data string) *string {
	if len(data) == 1 && data[0] >= 32 && data[0] < 127 {
		if s.searchText == "" {
			switch data[0] {
			case '+':
				v := "__add__"
				return &v
			case '-':
				if len(s.filtered) > 0 {
					item := s.filtered[s.selected]
					if !strings.HasPrefix(item.Value, "__") {
						v := "__delete__" + item.Value
						return &v
					}
				}
			}
		}
		s.searchText += data
		s.applyFilter()
	}
	return nil
}

func (s *Selector) handleBackspace(data string) *string {
	if !matchesKey(data, KeyBackspace) || len(s.searchText) == 0 {
		return nil
	}
	s.searchText = s.searchText[:len(s.searchText)-1]
	s.applyFilter()
	return nil
}

func (s *Selector) handleNav(data string) *string {
	switch {
	case matchesKey(data, KeyDown) && len(s.filtered) > 0:
		prev := s.selected
		s.selected = (s.selected + 1) % len(s.filtered)
		if prev != s.selected {
			s.startAnimationForSelection()
		}
	case matchesKey(data, KeyUp) && len(s.filtered) > 0:
		prev := s.selected
		s.selected = (s.selected - 1 + len(s.filtered)) % len(s.filtered)
		if prev != s.selected {
			s.startAnimationForSelection()
		}
	default:
		return nil
	}
	return nil
}

// startAnimationForSelection checks the currently selected item and starts
// its animation if it has AnimationFrames.
func (s *Selector) startAnimationForSelection() {
	if len(s.filtered) == 0 {
		s.stopAnimation()
		return
	}
	item := s.filtered[s.selected]
	if len(item.AnimationFrames) > 0 {
		if item.Value == s.animItemValue {
			return // same item, keep going
		}
		s.stopAnimation()
		s.animFrames = item.AnimationFrames
		s.animInterval = item.AnimationInterval
		s.animIdx = 0
		s.animItemValue = item.Value
		if s.tui != nil && s.animInterval > 0 {
			s.animStop = make(chan struct{})
			s.animTicker = time.NewTicker(s.animInterval)
			go s.animateLoop(s.animStop, s.animTicker.C)
		}
	} else {
		s.stopAnimation()
	}
}

func (s *Selector) animateLoop(stop chan struct{}, tick <-chan time.Time) {
	for {
		select {
		case <-tick:
			if s.tui != nil {
				s.tui.Apply(s.advanceAnimFrame)
			}
		case <-stop:
			return
		}
	}
}

// advanceAnimFrame advances the animation frame for the currently selected
// item. Runs on the commandLoop (sole owner), so it takes no lock.
func (s *Selector) advanceAnimFrame() {
	if len(s.animFrames) > 0 {
		s.animIdx++
	}
}

func (s *Selector) stopAnimation() {
	if s.animTicker != nil {
		s.animTicker.Stop()
		s.animTicker = nil
	}
	if s.animStop != nil {
		close(s.animStop)
		s.animStop = nil
	}
	s.animFrames = nil
	s.animItemValue = ""
}

func (s *Selector) handleSelect(data string) *string {
	switch {
	case matchesKey(data, KeyEnter) && len(s.filtered) > 0:
		s.stopAnimation()
		v := s.filtered[s.selected].Value
		return &v
	case matchesKey(data, KeyTab) && len(s.filtered) > 0:
		s.stopAnimation()
		v := s.filtered[0].Value
		return &v
	}
	return nil
}

func (s *Selector) handleCancel(data string) *string {
	if matchesKey(data, KeyEscape) || matchesKey(data, KeyCtrlC) {
		s.stopAnimation()
		v := ""
		return &v
	}
	return nil
}

func (s *Selector) applyFilter() {
	if s.searchText == "" {
		s.filtered = s.items
	} else {
		var f []SelectorItem
		lower := strings.ToLower(s.searchText)
		for _, item := range s.items {
			if strings.Contains(strings.ToLower(item.Label), lower) ||
				strings.Contains(strings.ToLower(item.Description), lower) {
				f = append(f, item)
			}
		}
		s.filtered = f
	}
	if s.selected >= len(s.filtered) {
		s.selected = len(s.filtered) - 1
	}
	if s.selected < 0 {
		s.selected = 0
	}
}

func (s *Selector) emit(value string) {
	if s.done != nil {
		s.done()
	}
	select {
	case s.result <- value:
	default:
	}
}

func (s *Selector) Focused() bool { return s.focused }

func (s *Selector) SetFocused(focused bool) { s.focused = focused }

func (s *Selector) Render(width int) []string {
	return s.renderLocked(width)
}

func (s *Selector) renderLocked(width int) []string {
	if width > 60 {
		width = 60
	}
	if width < 30 {
		width = 30
	}

	colors := s.getColors()
	var lines []string
	lines = append(lines, s.renderTitle(colors, width))
	lines = append(lines, s.renderSeparator(colors.sep, width))
	lines = append(lines, s.renderSearchLine(colors))
	lines = append(lines, s.renderSeparator(colors.sep, width))
	lines = append(lines, s.renderItems(colors, width)...)
	lines = append(lines, s.renderSeparator(colors.sep, width))
	lines = append(lines, s.renderHint(colors))
	return lines
}

type selectorColors struct {
	title string
	sep   string
	sys   string
	suc   string
	ast   string
}

func (s *Selector) getColors() selectorColors {
	return selectorColors{
		title: TheTheme.ColorHex("assistant_msg"),
		sep:   TheTheme.ColorHex("separator"),
		sys:   TheTheme.ColorHex("system_msg"),
		suc:   TheTheme.ColorHex("tool_success"),
		ast:   TheTheme.ColorHex("assistant_msg"),
	}
}

func (s *Selector) renderTitle(c selectorColors, width int) string {
	return ansi.Bold + ansi.Fg(c.title) + s.title + ansi.Reset
}

func (s *Selector) renderSeparator(color string, width int) string {
	return ansi.Fg(color) + strings.Repeat("─", width) + ansi.Reset
}

func (s *Selector) renderSearchLine(c selectorColors) string {
	prompt := ansi.Fg(c.sys) + ansi.Faint + "search> " + ansi.Reset
	if s.searchText != "" {
		prompt += s.searchText
	}
	prompt += CURSOR_MARKER
	return prompt
}

func (s *Selector) renderHint(c selectorColors) string {
	hint := "  ↑↓ nav  /  type filter  /  enter  /  esc"
	if hasDeletableItems(s.items) {
		hint += "  /  " + ansi.Fg(c.suc) + "+ add / - delete" + ansi.Reset
	}
	return ansi.Fg(c.sys) + ansi.Faint + hint + ansi.Reset
}

func hasDeletableItems(items []SelectorItem) bool {
	for _, item := range items {
		if !strings.HasPrefix(item.Value, "__") {
			return true
		}
	}
	return false
}

func (s *Selector) renderItems(c selectorColors, width int) []string {
	if len(s.filtered) == 0 {
		return []string{padToWidth(ansi.Fg(c.sys)+ansi.Faint+"  no matches"+ansi.Reset, width)}
	}

	var lines []string
	maxShow := s.visibleCount()
	start := s.itemWindowStart(maxShow)

	for i := start; i < start+maxShow && i < len(s.filtered); i++ {
		lines = append(lines, s.renderItem(c, i, width))
	}

	if len(s.filtered) > maxShow {
		more := len(s.filtered) - maxShow
		lines = append(lines, padToWidth(
			ansi.Fg(c.sys)+ansi.Faint+"("+itoa(more)+" more)"+ansi.Reset, width))
	}

	return lines
}

func (s *Selector) visibleCount() int {
	maxShow := 8
	if maxShow > len(s.filtered) {
		maxShow = len(s.filtered)
	}
	return maxShow
}

func (s *Selector) itemWindowStart(maxShow int) int {
	start := s.selected - maxShow/2
	if start < 0 {
		start = 0
	}
	if start+maxShow > len(s.filtered) {
		start = len(s.filtered) - maxShow
		if start < 0 {
			start = 0
		}
	}
	return start
}

func (s *Selector) renderItem(c selectorColors, idx, width int) string {
	item := s.filtered[idx]

	marker := ""
	if item.Value == s.currentValue {
		marker = ansi.Fg(c.suc) + "✓ " + ansi.Reset
	}

	// Build description: show animation frame for selected animated item
	desc := item.Description
	if idx == s.selected && len(s.animFrames) > 0 && item.Value == s.animItemValue {
		frame := s.animFrames[s.animIdx%len(s.animFrames)]
		desc = frame
	}

	if idx == s.selected {
		labelColor := c.ast
		if item.Color != "" {
			labelColor = item.Color
		}
		line := ansi.Fg(c.suc) + "› " + ansi.Reset + marker + ansi.Fg(labelColor) + item.Label + ansi.Reset
		if desc != "" {
			line += "  " + ansi.Fg(c.sys) + dimText(desc)
		}
		return padToWidth(line, width)
	}

	labelColor := c.sys
	if item.Color != "" {
		labelColor = item.Color
	}
	label := ansi.Fg(labelColor) + ansi.Faint + item.Label + ansi.Reset
	line := "  " + marker + label
	if desc != "" {
		line += "  " + dimText(ansi.Fg(c.sys)+desc)
	}
	return padToWidth(line, width)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	r := ""
	for n > 0 {
		r = string(rune('0'+n%10)) + r
		n /= 10
	}
	return r
}
