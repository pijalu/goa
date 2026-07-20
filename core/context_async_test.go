// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/tui"
)

// TestSelectOptionAsync_FallbackFetchesSynchronously verifies that without an
// async callback configured, SelectOptionAsync falls back to a synchronous
// fetch routed through SelectOptionFunc.
func TestSelectOptionAsync_FallbackFetchesSynchronously(t *testing.T) {
	var gotTitle string
	var gotItems []tui.SelectorItem
	ctx := Context{
		SelectOptionFunc: func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
			gotTitle = title
			gotItems = options
			onSelected("v1", true)
		},
	}
	var selected string
	ctx.SelectOptionAsync("Pick:", func() []tui.SelectorItem {
		return []tui.SelectorItem{{Value: "v1", Label: "v1"}}
	}, func(v string, ok bool) { selected = v })

	if gotTitle != "Pick:" {
		t.Errorf("title = %q, want Pick:", gotTitle)
	}
	if len(gotItems) != 1 || gotItems[0].Value != "v1" {
		t.Errorf("items = %+v, want [v1]", gotItems)
	}
	if selected != "v1" {
		t.Errorf("selected = %q, want v1", selected)
	}
}

// TestSelectOptionAsync_UsesAsyncCallback verifies the async callback is
// preferred when configured.
func TestSelectOptionAsync_UsesAsyncCallback(t *testing.T) {
	var asyncCalled bool
	ctx := Context{
		SelectOptionAsyncFunc: func(title string, fetch func() []tui.SelectorItem, onSelected func(string, bool)) {
			asyncCalled = true
			onSelected("", false)
		},
	}
	ctx.SelectOptionAsync("Pick:", func() []tui.SelectorItem { return nil }, func(string, bool) {})
	if !asyncCalled {
		t.Error("expected SelectOptionAsyncFunc to be invoked")
	}
}
