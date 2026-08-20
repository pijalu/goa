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
	// PreserveOrder opts out of the default alphabetical Label sort: the
	// caller's item order is kept as-is. Use for chronologically ordered
	// lists (e.g. the /session picker) where sorting would destroy meaning.
	PreserveOrder bool
	// Editable opts the item into the 'e' edit hotkey: with an empty search
	// filter, pressing 'e' while this item is highlighted emits
	// "__edit__"+Value instead of starting a filter (used by /goal:manage to
	// edit a queued goal's description). On non-editable items 'e' keeps its
	// default filter behavior, so other pickers are unaffected.
	Editable bool
	// SearchLabel, when non-empty, replaces Label+Description as the text the
	// search filter matches against. Pickers whose Description carries
	// structural noise (e.g. the model picker's "provider=X model=Y")
	// set this to the user-meaningful terms (model name, provider name) so
	// typing "model" or "provider" does not match every row.
	SearchLabel string
}

// SelectorKeymap configures what a Selector instance's hotkeys emit. The
// zero value keeps the default bindings shared by most pickers (/provider,
// /model, …): '+' emits "__add__", '-' emits "__delete__"+value, and the
// Delete key emits "__delete__"+value. Backspace never deletes — it only
// edits the search filter.
type SelectorKeymap struct {
	// ReorderMode repurposes '+'/'-' from add/delete to direct reordering
	// (used by /goal:manage): '+' emits "__moveup__"+value and '-' emits
	// "__movedown__"+value for the highlighted non-sentinel item; on sentinel
	// rows and empty lists the keys are consumed without emitting, so they
	// never pollute the search filter. The Delete key keeps the delete
	// emit, so deletion stays available (and confirmed by the caller) in
	// reorder mode.
	ReorderMode bool
}

// Selector is a Component that shows a searchable list of options.
// When shown, the user can:
//   - Type to filter
//   - Up/Down to navigate
//   - Enter to select
//   - Escape to cancel
//   - Backspace/Delete (on a non-menu item) to trigger deletion; Backspace
//     only edits the search filter (it never deletes) and only the Delete key
//     deletes, so clearing a filter with Backspace can never emit a deletion
//   - 'e' (on an Editable item with an empty filter) to trigger editing
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
	keymap       SelectorKeymap

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

// NewSelector creates a Selector. Items are sorted alphabetically by Label
// unless every item sets PreserveOrder, in which case the caller's order is
// kept (used for chronological lists such as the /session picker).
// currentValue is the currently active option (shown with a ✓ marker).
// The result channel receives the selected value when the user confirms,
// or "" if cancelled.
func NewSelector(title string, items []SelectorItem, currentValue string, result chan string) *Selector {
	sorted := sortSelectorItems(items)

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

// sortSelectorItems returns the items in display order: alphabetical by
// Label by default, or the caller's original order when every item opts out
// via PreserveOrder.
func sortSelectorItems(items []SelectorItem) []SelectorItem {
	out := make([]SelectorItem, len(items))
	copy(out, items)
	if preserveSelectorOrder(out) {
		return out
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	return out
}

// preserveSelectorOrder reports whether the caller's order should be kept:
// true only when the list is non-empty and every item sets PreserveOrder.
func preserveSelectorOrder(items []SelectorItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		if !it.PreserveOrder {
			return false
		}
	}
	return true
}

// SetDone sets the callback that restores the editor when selection ends.
func (s *Selector) SetDone(fn func()) { s.done = fn }

// SetKeymap sets the per-instance hotkey bindings (see SelectorKeymap). The
// default (never calling this) keeps '+' = add and '-' = delete.
func (s *Selector) SetKeymap(k SelectorKeymap) { s.keymap = k }

// SetTUI stores the TUI reference for triggering re-renders on animation.
func (s *Selector) SetTUI(t *TUI) { s.tui = t }

// SetItems replaces the options and resets filter, preserving the same
// ordering rules as NewSelector (alphabetical unless PreserveOrder is set).
func (s *Selector) SetItems(items []SelectorItem) {
	sorted := sortSelectorItems(items)
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

// handleDelete emits "__delete__"+value for the highlighted item when the
// user presses the Delete key. Backspace is intentionally NOT a delete
// trigger: it is exclusively the filter-editing key (see handleBackspace), so
// pressing Backspace to clear the search filter must never surface a deletion
// (previously Backspace with an empty filter emitted __delete__ — that made
// /model propose to delete the highlighted model, and closed the /config
// menu). Only KeyDelete and the '-' hotkey (handleHotkey) delete.
func (s *Selector) handleDelete(data string) *string {
	if !matchesKey(data, KeyDelete) {
		return nil
	}
	if len(s.filtered) == 0 {
		return nil
	}
	item := s.filtered[s.selected]
	if isSelectorSentinel(item.Value) {
		return nil
	}
	v := "__delete__" + item.Value
	return &v
}

// isSelectorSentinel reports whether v is a selector action row rather than a
// deletable data item. The previous guard blocked deletion of any value with
// a "__" prefix; that also trapped legitimately persisted entries whose IDs
// began with "__delete__" (a leaked sentinel saved as a model ID, see
// Model delete), making them impossible to remove via '-'.
// Only exact sentinel action rows are non-deletable; a "__delete__"-prefixed
// value stays deletable so polluted entries can be removed.
func isSelectorSentinel(v string) bool {
	switch v {
	case "__add__", "__custom__":
		return true
	// /goal:manage action rows: the add-at-start/end rows and the Done row are
	// never deletable or movable — the reorder/delete keys are consumed on
	// them (the Done row previously emitted "__delete____done__", which
	// surfaced a bogus "queued goal not found" error).
	case "__add_first__", "__add_last__", "__done__":
		return true
	}
	return false
}

func (s *Selector) handlePrintable(data string) *string {
	if len(data) != 1 || data[0] < 32 || data[0] >= 127 {
		return nil
	}
	if s.searchText == "" {
		if emit, consumed := s.handleHotkey(data[0]); emit != nil || consumed {
			return emit
		}
	}
	s.searchText += data
	s.applyFilter()
	return nil
}

// handleHotkey processes the empty-filter hotkeys ('+', '-', 'e'). It returns
// the value to emit (nil when the hotkey does not apply) and whether the key
// is consumed (consumed keys never reach the search filter).
func (s *Selector) handleHotkey(key byte) (emit *string, consumed bool) {
	switch key {
	case '+':
		if s.keymap.ReorderMode {
			return s.moveHotkey("__moveup__"), true
		}
		v := "__add__"
		return &v, true
	case '-':
		if s.keymap.ReorderMode {
			return s.moveHotkey("__movedown__"), true
		}
		// '-' is always consumed: when deletion does not apply (sentinel
		// item or empty list) the key must not pollute the search filter.
		return s.deleteHotkey(), true
	case 'e':
		// 'e' is consumed ONLY by an editable row; otherwise it falls
		// through and starts a filter like any other letter.
		return s.editHotkey(), false
	}
	return nil, false
}

// moveHotkey implements the '+/-' hotkeys in ReorderMode (/goal:manage): emit
// prefix+value for the highlighted item, unless it is a sentinel action row
// or the list is empty.
func (s *Selector) moveHotkey(prefix string) *string {
	if len(s.filtered) == 0 || isSelectorSentinel(s.filtered[s.selected].Value) {
		return nil
	}
	v := prefix + s.filtered[s.selected].Value
	return &v
}

// deleteHotkey implements the '-' hotkey: emit "__delete__"+value for the
// highlighted item, unless it is a sentinel action row or the list is empty.
func (s *Selector) deleteHotkey() *string {
	if len(s.filtered) == 0 || isSelectorSentinel(s.filtered[s.selected].Value) {
		return nil
	}
	v := "__delete__" + s.filtered[s.selected].Value
	return &v
}

// editHotkey implements the 'e' hotkey: emit "__edit__"+value when the
// highlighted item opts in via SelectorItem.Editable.
func (s *Selector) editHotkey() *string {
	if len(s.filtered) == 0 || !s.filtered[s.selected].Editable {
		return nil
	}
	v := "__edit__" + s.filtered[s.selected].Value
	return &v
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

// selectorItemMatches reports whether the lowercase search term matches the
// item. When the item sets SearchLabel, only that text is searched (the
// Description is excluded); otherwise the filter matches Label or
// Description as before.
func selectorItemMatches(item SelectorItem, lowerTerm string) bool {
	if item.SearchLabel != "" {
		return strings.Contains(strings.ToLower(item.SearchLabel), lowerTerm)
	}
	return strings.Contains(strings.ToLower(item.Label), lowerTerm) ||
		strings.Contains(strings.ToLower(item.Description), lowerTerm)
}

func (s *Selector) applyFilter() {
	if s.searchText == "" {
		s.filtered = s.items
	} else {
		var f []SelectorItem
		lower := strings.ToLower(s.searchText)
		for _, item := range s.items {
			if selectorItemMatches(item, lower) {
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
	if s.keymap.ReorderMode {
		if hasDeletableItems(s.items) {
			hint += "  /  " + ansi.Fg(c.suc) + "+ up / - down / del delete" + ansi.Reset
		}
	} else if hasDeletableItems(s.items) {
		hint += "  /  " + ansi.Fg(c.suc) + "+ add / - delete" + ansi.Reset
	}
	if hasEditableItems(s.items) {
		hint += "  /  " + ansi.Fg(c.suc) + "e edit" + ansi.Reset
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

func hasEditableItems(items []SelectorItem) bool {
	for _, item := range items {
		if item.Editable {
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
