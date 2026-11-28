// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestSelectorSearchCursorMatchesRenderedLine drives the complete selector
// render path with pinned bottom chrome. The compositor clamps the selector
// above that chrome; the hardware cursor must land after the typed filter on
// the search row that is actually painted, not at the overlay's unclamped Y.
func TestSelectorSearchCursorMatchesRenderedLine(t *testing.T) {
	const w, h = 80, 24
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	chat := NewChatViewport()
	for i := 0; i < 50; i++ {
		chat.AddSystemMessage("transcript line")
	}
	engine.AddChild(chat)
	engine.AddChild(NewEditor())
	engine.AddChild(NewFooter())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Establish a real scrollback watermark before opening the menu.
	engine.RenderNow()
	engine.ShowSelector("Settings:", []SelectorItem{
		{Value: "compression", Label: "Compression"},
		{Value: "config", Label: "Config"},
	}, "")
	engine.SendKey("c")
	engine.SendKey("f")

	emu := NewTermEmulator(h, w)
	for _, write := range term.Writes() {
		emu.Process(write)
	}

	searchRow := -1
	for row := 0; row < h; row++ {
		if strings.Contains(emu.Visible(row), "search> cf") {
			searchRow = row
			break
		}
	}
	if searchRow < 0 {
		t.Fatalf("rendered selector search line not found:\n%s", dumpEmuScreen(emu))
	}
	if emu.row != searchRow {
		t.Errorf("hardware cursor row = %d, want %d (rendered search line):\n%s",
			emu.row, searchRow, dumpEmuScreen(emu))
	}
	if wantCol := visibleWidth("search> cf"); emu.col != wantCol {
		t.Errorf("hardware cursor col = %d, want %d (after typed filter)", emu.col, wantCol)
	}
}
