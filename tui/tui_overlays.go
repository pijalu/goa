// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// ShowConfirm displays a plugin confirmation modal (goa.ui.confirm, plan §4).
// The result channel delivers the chosen option ID, or "" when the user
// dismissed the dialog (Esc / implicit Cancel row). The returned handle hides
// the card programmatically — the presenter uses it when the job ends
// out-of-band (bridge timeout / shutdown) so no ghost modal lingers. Per §9
// Q3 this takes input focus like ShowSelector — see ConfirmCard's doc
// comment for the decision record.
func (t *TUI) ShowConfirm(title, body string, options []ConfirmOption, defaultID string, allowCancel bool) (<-chan string, *OverlayHandle) {
	result := make(chan string, 1)
	card := NewConfirmCard(title, body, options, defaultID, allowCancel, func(id string, cancelled bool) {
		if cancelled {
			id = ""
		}
		select {
		case result <- id:
		default:
		}
	})
	handle := t.ShowOverlay(card, OverlayOptions{
		CaptureInput: true,
		Center:       true,
	})
	card.SetDone(func() { handle.Hide() })
	return result, handle
}

func (t *TUI) ShowSelector(title string, items []SelectorItem, currentValue string) <-chan string {
	_, result := t.showSelector(title, items, currentValue)
	return result
}

// ShowSelectorKeyed is ShowSelector with per-instance hotkey bindings (see
// SelectorKeymap): /goal:manage uses it to repurpose '+'/'-' from add/delete
// to direct reordering. Pickers using ShowSelector keep the default bindings.
func (t *TUI) ShowSelectorKeyed(title string, items []SelectorItem, currentValue string, keys SelectorKeymap) <-chan string {
	sel, result := t.showSelector(title, items, currentValue)
	sel.SetKeymap(keys)
	return result
}

// ShowSelectorLoading displays a selector pre-populated with a single
// "Loading…" placeholder item and returns both the live *Selector and the
// result channel. The caller fetches items asynchronously and pushes them via
// TUI.Apply(func() { sel.SetItems(realItems) }) when ready, giving the user
// immediate feedback instead of a frozen UI while a remote list (e.g. a
// provider's GET /models) is retrieved. On a tiny terminal it behaves like
// ShowSelector (inline cancel).
func (t *TUI) ShowSelectorLoading(title, loadingLabel string) (*Selector, <-chan string) {
	if loadingLabel == "" {
		loadingLabel = "Loading…"
	}
	placeholder := []SelectorItem{{Value: "", Label: loadingLabel, Description: "please wait"}}
	return t.showSelector(title, placeholder, "")
}

func (t *TUI) showSelector(title string, items []SelectorItem, currentValue string) (*Selector, <-chan string) {
	result := make(chan string, 1)
	sel := NewSelector(title, items, currentValue, result)
	sel.SetTUI(t)
	_, termH := t.terminal.Size()
	if termH < 4 {
		// Terminal too small for overlay — render inline instead
		result := make(chan string, 1)
		go func() {
			result <- ""
		}()
		return sel, result
	}
	h := len(items) + 4
	if h > termH {
		h = termH
	}
	opts := OverlayOptions{
		CaptureInput: true,
		Height:       h,
	}
	handle := t.ShowOverlay(sel, opts)
	sel.SetDone(func() {
		handle.Hide()
	})
	return sel, result
}

// ShowInput displays a single-line input prompt as an overlay.
// The caller blocks on the returned channel until the user submits or cancels.
//
// Deprecated: this spawns a throwaway overlay Input that bypasses the main
// input zone. New code must capture text via the main input line
// (App.requestMainInput / core.Context.RequestMainInput) per the "Input
// discipline" guideline in docs/TUI.md. Retained for tests and any external
// callers; production code no longer invokes it.
func (t *TUI) ShowInput(prompt, current string) <-chan string {
	result := make(chan string, 1)
	in := NewInput()
	in.SetText(current)
	comp := &inputOverlay{prompt: prompt, input: in, result: result}
	in.SetOnSubmit(func(text string) {
		select {
		case result <- text:
		default:
		}
		if comp.done != nil {
			comp.done()
		}
	})
	opts := OverlayOptions{
		CaptureInput: true,
		Height:       3,
	}
	handle := t.ShowOverlay(comp, opts)
	comp.SetDone(func() {
		handle.Hide()
	})
	return result
}

// inputOverlay wraps an Input with a prompt label for use as an overlay.
type inputOverlay struct {
	prompt string
	input  *Input
	result chan string
	done   func()
}

func (o *inputOverlay) SetDone(fn func()) { o.done = fn }
func (o *inputOverlay) Render(width int) []string {
	var lines []string
	lines = append(lines, padToWidth(o.prompt, width))
	lines = append(lines, o.input.Render(width)...)
	return lines
}
func (o *inputOverlay) HandleInput(data string) {
	if matchesKey(data, KeyEscape) || matchesKey(data, KeyCtrlC) {
		if o.done != nil {
			o.done()
		}
		select {
		case o.result <- "":
		default:
		}
		return
	}
	o.input.HandleInput(data)
}
func (o *inputOverlay) SetFocused(f bool) { o.input.SetFocused(f) }
func (o *inputOverlay) Focused() bool     { return o.input.Focused() }
func (o *inputOverlay) Invalidate()       {}

// handleToggleExpand handles Ctrl+O to toggle ALL tool components between
// Summary (collapsed, N-line preview) and Full (expanded) view for the
// running session. Previously it toggled only the last widget; the global
// toggle matches the spec (one key flips every tool block) and is honored by
// every widget via the ChatViewport's ToolViewPolicy.
