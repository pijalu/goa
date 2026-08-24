// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// configMenuItems mirrors the /config top-level menu shape: 23 items whose
// alphabetical window matches the bugs.md capture.
func configMenuItems() []SelectorItem {
	labels := []struct{ label, desc string }{
		{"Mode", "default"}, {"Active model", "x-preview-f-free"},
		{"Provider", "openai"}, {"Manage models", "Add, edit, remove, or select models"},
		{"Execution mode", "yolo"}, {"Retry settings", "3 retries"},
		{"Compression", "2 active"}, {"Theme", "dark"}, {"Spinner", "dots"},
		{"Spinner location", "status bar"}, {"Thinking level", "high"},
		{"Thinking blocks", "hidden"}, {"Show thinking", "off"},
		{"Multi-agent", "off"}, {"Orchestrator", "none"}, {"Teams", "none"},
		{"Tools", "all on"}, {"Bash", "warn on shell file edits: on"},
		{"MCP servers", "none installed"}, {"Sandbox", "off"},
		{"Loop detection", "warn:10 stop:15"}, {"Skills", "embedded 3 · local 2"},
		{"Goals", "7 days"},
	}
	items := make([]SelectorItem, 0, len(labels))
	for _, l := range labels {
		items = append(items, SelectorItem{Value: l.label, Label: l.label, Description: l.desc})
	}
	return items
}

// dumpEmuScreen renders every emulator row so blank rows stand out in
// assertion output.
func dumpEmuScreen(emu *TermEmulator) string {
	var b strings.Builder
	for r := 0; r < emu.h; r++ {
		b.WriteString(ansi.Strip(emu.Visible(r)))
		b.WriteString("\n")
	}
	return b.String()
}

// assertNoBlankRowsInSelector fails when a fully-blank row sits strictly
// between the search line and the bottom hint of a visible selector.
func assertNoBlankRowsInSelector(t *testing.T, step, screen string) {
	t.Helper()
	lines := strings.Split(screen, "\n")
	searchIdx, hintIdx := -1, -1
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "search>"):
			searchIdx = i
		case strings.Contains(ln, "type filter"):
			hintIdx = i
		}
	}
	if searchIdx < 0 || hintIdx < 0 || hintIdx <= searchIdx {
		return // selector not (fully) visible this frame
	}
	for i := searchIdx + 1; i < hintIdx; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			t.Errorf("step %s: blank row %d inside selector:\n%s", step, i, screen)
			return
		}
	}
}

// TestSelectorItemTextSanitized verifies the ingestion-boundary flattening:
// multi-line Labels/Descriptions/SearchLabels become single-line display text
// through both constructors of the item list (NewSelector and SetItems),
// while semantic Values stay untouched.
func TestSelectorItemTextSanitized(t *testing.T) {
	poison := []SelectorItem{
		{Value: "v\n1", Label: "a\r\nb", Description: "x\n\ty"},
		{Value: "v2", Label: "clean", Description: "also clean", SearchLabel: "s\nl"},
	}
	s := NewSelector("T", poison, "", nil)
	for _, item := range s.items {
		for name, text := range map[string]string{"Label": item.Label, "Description": item.Description, "SearchLabel": item.SearchLabel} {
			if strings.ContainsAny(text, "\n\r\t") {
				t.Errorf("%s not flattened: %q", name, text)
			}
		}
	}
	// Values are semantic IDs: they must pass through unsanitized.
	sawPoisonValue := false
	for _, item := range s.items {
		if item.Value == "v\n1" {
			sawPoisonValue = true
		}
	}
	if !sawPoisonValue {
		t.Errorf("Value was altered: want %q preserved", "v\n1")
	}
	s.SetItems(poison)
	for _, item := range s.items {
		if strings.ContainsAny(item.Description, "\n") && item.Value != "" {
			t.Errorf("SetItems did not flatten Description: %q", item.Description)
		}
	}
}

// TestSelectorMultilineDescriptionRendersInline is the end-to-end regression
// for the reported blank lines between palette entries: an item whose
// dynamic text contains newlines (malformed config value, external name)
// renders FLATTENED onto its row through the real compositor → terminal
// pipeline — full text visible, no line feeds reaching the screen, and no
// blank rows between entries.
func TestSelectorMultilineDescriptionRendersInline(t *testing.T) {
	const w, h = 80, 24
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	cv := NewChatViewport()
	engine.AddChild(cv)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	items := append(configMenuItems(), SelectorItem{
		Value: "poison", Label: "Keymap",
		Description: "custom\n\nbindings", // sorts between Goals and Loop detection
	})
	engine.ShowSelector("Settings:", items, "")
	engine.RenderNow()

	emu := NewTermEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	screen := dumpEmuScreen(emu)
	assertNoBlankRowsInSelector(t, "multiline-desc", screen)

	foundRow := false
	for r := 0; r < h; r++ {
		row := ansi.Strip(emu.Visible(r))
		if strings.Contains(row, "Keymap") {
			foundRow = true
			if !strings.Contains(row, "custom") || !strings.Contains(row, "bindings") {
				t.Fatalf("multi-line description lost content on row %d: %q", r, strings.TrimSpace(row))
			}
		}
	}
	if !foundRow {
		t.Fatal("poisoned item missing from rendered popup")
	}
}

// TestSelectorOverlayContiguityUnderChurn drives a production-like TUI
// (transcript + editor + footer chrome) with the selector open through a
// seeded, bounded random sequence of keys, background transcript growth, and
// idle frames, asserting after EVERY frame that no blank row appears between
// the search line and the bottom hint.
func TestSelectorOverlayContiguityUnderChurn(t *testing.T) {
	const w, h = 80, 24
	rng := rand.New(rand.NewSource(20260824)) // deterministic sequence

	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	cv := NewChatViewport()
	engine.AddChild(cv)
	engine.AddChild(NewEditor())
	engine.AddChild(NewFooter())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	engine.ShowSelector("Settings:", configMenuItems(), "")
	engine.RenderNow()

	emu := NewTermEmulator(h, w)
	check := func(step string) {
		for _, wr := range term.Writes() {
			emu.Process(wr)
		}
		term.writes = nil
		assertNoBlankRowsInSelector(t, step, dumpEmuScreen(emu))
	}
	check("open")

	keys := []string{
		KeyDown, KeyUp, "a", "b", "c", "e", "g", "l", "m", "o", "s",
		"t", KeyBackspace, KeyEscape, KeyDown, KeyDown,
	}
	for step := 0; step < 40; step++ {
		name := fmt.Sprintf("step%d", step)
		switch rng.Intn(10) {
		case 0, 1:
			cv.AddSystemMessage(fmt.Sprintf("stream line %d", step))
		case 2:
			// idle render with no mutation
		default:
			engine.SendKey(keys[rng.Intn(len(keys))])
			var closed bool
			engine.ApplySync(func() { closed = len(engine.overlayStack) == 0 })
			if closed {
				engine.ShowSelector("Settings:", configMenuItems(), "")
			}
		}
		engine.RenderNow()
		check(name)
	}
}
