// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"time"
)

// Input is a single-line text input component with readline-like behavior.
// It wraps LineEditor, adding history, prompt rendering, and submit handling.
// For multi-line editing, use Editor.
//
// Concurrency: the commandLoop is the sole owner of Input state. HandleInput,
// Text/SetText/Clear, Render and SetFocused all run on the loop
// (serialized by the commandLoop); the onSubmit callback runs inline after the
// state mutation in the same step. No mutex is required.
type Input struct {
	editor *LineEditor

	history []string
	histIdx int // -1 = editing new
	prompt  string

	onSubmit func(string)
	focused  bool
	keybind  *KeybindingsManager

	// Completion debounce (currently unused, kept for API compatibility)
	compDebounce time.Duration
	compAbort    chan struct{}
}

// NewInput creates an Input component.
func NewInput() *Input {
	return &Input{
		editor:       NewLineEditor(),
		histIdx:      -1,
		prompt:       "> ",
		keybind:      DefaultKeybindingsManager(),
		compDebounce: 150 * time.Millisecond,
		compAbort:    make(chan struct{}),
	}
}

// SetOnSubmit sets the submit callback.
func (in *Input) SetOnSubmit(fn func(string)) {
	in.onSubmit = fn
}

// SetCompleter sets the tab completion provider.
func (in *Input) SetCompleter(c Completer) {
	in.editor.SetCompleter(c)
}

// SetTUI sets the TUI reference (for overlay support).
func (in *Input) SetTUI(t *TUI) { _ = t }

// SetMaxLines is kept for API compatibility — Input is single-line.
func (in *Input) SetMaxLines(int) {}

// Text returns the current buffer content.
func (in *Input) Text() string { return in.editor.Text() }

// SetText replaces the buffer content.
func (in *Input) SetText(s string) { in.editor.SetText(s) }

// Clear empties the buffer and resets history.
func (in *Input) Clear() {
	in.editor.Clear()
	in.histIdx = -1
}

// HandleInput processes keyboard input. Runs on the commandLoop; the onSubmit
// callback runs inline after the state mutation in the same step.
func (in *Input) HandleInput(data string) {
	if !in.focused {
		return
	}
	submitText, hasSubmit := in.dispatchInput(data)
	if hasSubmit && in.onSubmit != nil {
		in.onSubmit(submitText)
	}
}

// dispatchInput mutates input state in response to a key and returns the text
// to submit (and whether to submit).
func (in *Input) dispatchInput(data string) (string, bool) {
	switch {
	case in.keybind.Matches(data, KbSubmit):
		return in.collectSubmit()
	case in.keybind.Matches(data, KbSelectUp) || data == KeyUp:
		in.historyUp()
	case in.keybind.Matches(data, KbSelectDown) || data == KeyDown:
		in.historyDown()
	default:
		in.editor.HandleKey(data)
	}
	return "", false
}

// collectSubmit trims, records history, clears the buffer, and returns the
// submitted text plus true (or "", false when there is nothing to submit).
func (in *Input) collectSubmit() (string, bool) {
	text := strings.TrimSpace(in.editor.Text())
	if text == "" {
		return "", false
	}
	in.addHistory(text)
	in.editor.Clear()
	in.histIdx = -1
	return text, true
}

func (in *Input) historyUp() {
	if len(in.history) == 0 {
		return
	}
	if in.histIdx < 0 {
		in.histIdx = len(in.history) - 1
	} else if in.histIdx > 0 {
		in.histIdx--
	} else {
		return
	}
	in.editor.SetText(in.history[in.histIdx])
}

func (in *Input) historyDown() {
	if in.histIdx < 0 {
		return
	}
	in.histIdx++
	if in.histIdx >= len(in.history) {
		in.histIdx = -1
		in.editor.Clear()
		return
	}
	in.editor.SetText(in.history[in.histIdx])
}

func (in *Input) addHistory(s string) {
	if s == "" {
		return
	}
	if len(in.history) > 0 && in.history[len(in.history)-1] == s {
		return
	}
	in.history = append(in.history, s)
	if len(in.history) > 100 {
		in.history = in.history[1:]
	}
}

// Render renders the input as a single line.
func (in *Input) Render(width int) []string {
	if width <= 0 {
		return nil
	}

	text := in.prompt + in.editor.Text()
	vw := visibleWidth(text)
	if vw >= width {
		cursorEnd := visibleWidth(in.prompt + in.editor.TextBeforeCursor())
		if cursorEnd >= width {
			visible := sliceByColumn(text, len(text)-width, width)
			return []string{padToWidth(visible, width)}
		}
	}
	return []string{padToWidth(text, width)}
}

// Focused returns whether this component has keyboard focus.
func (in *Input) Focused() bool { return in.focused }

// SetFocused sets the focus state.
func (in *Input) SetFocused(focused bool) { in.focused = focused }

// Invalidate is a no-op.
func (in *Input) Invalidate() {}
