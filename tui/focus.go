// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// FocusStack is the single authority for which component receives keyboard
// input. It replaces the scattered focus-ownership logic that previously lived
// in overlayEntry.preFocus, App.pendingMainInput, and App.reviewOverlayRestore
// (docs/TUI-REWORK.md §1.2 Problem B).
//
// Invariants enforced by construction:
//   - Only the top of the stack receives input.
//   - Pop restores the previous focus exactly.
//   - A hidden/popped component can never be the input target.
//
// Concurrency: the commandLoop is the sole owner of the FocusStack. Every
// mutation (Push/Pop/Replace) and read (Top/Depth) happens on the loop — via
// ShowOverlay/hideOverlay/setOverlayCapture (single-ownership via commandLoop). No
// mutex is required.
type FocusStack struct {
	stack []Component
}

// NewFocusStack creates an empty stack. Push the persistent base component
// (the main editor) once at startup.
func NewFocusStack(base Component) *FocusStack {
	return &FocusStack{stack: []Component{base}}
}

// Push makes c the input target, above the current top. Returns the previous
// top so callers can restore it explicitly if needed (rare).
func (f *FocusStack) Push(c Component) Component {
	prev := f.topLocked()
	f.stack = append(f.stack, c)
	return prev
}

// Pop removes c from the top of the stack. If c is not the current top this is
// a no-op (defensive: overlays must hide in LIFO order). Returns the new top.
func (f *FocusStack) Pop(c Component) Component {
	if len(f.stack) == 0 {
		return nil
	}
	if f.stack[len(f.stack)-1] != c {
		// Out-of-order pop: remove c wherever it is to keep the stack coherent.
		for i := len(f.stack) - 1; i >= 0; i-- {
			if f.stack[i] == c {
				f.stack = append(f.stack[:i], f.stack[i+1:]...)
				break
			}
		}
		return f.topLocked()
	}
	f.stack = f.stack[:len(f.stack)-1]
	return f.topLocked()
}

// Top returns the component that should receive input, or nil if empty.
func (f *FocusStack) Top() Component {
	return f.topLocked()
}

// Replace swaps the current top with c (used when the main input line switches
// between normal entry and a modal prompt on the same editor slot). Returns
// the previous top.
func (f *FocusStack) Replace(c Component) Component {
	prev := f.topLocked()
	if len(f.stack) > 0 {
		f.stack[len(f.stack)-1] = c
	} else {
		f.stack = []Component{c}
	}
	return prev
}

// Depth returns the number of layers on the stack.
func (f *FocusStack) Depth() int { return len(f.stack) }

func (f *FocusStack) topLocked() Component {
	if len(f.stack) == 0 {
		return nil
	}
	return f.stack[len(f.stack)-1]
}
