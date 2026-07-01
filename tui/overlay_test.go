// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

type fakeTerminal struct {
	mu      sync.Mutex
	w, h    int
	writes  []string
	cursor  bool
	raw     bool
	onInput func(string)
}

func (f *fakeTerminal) Start(onInput func(string), onResize func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onInput = onInput
}
func (f *fakeTerminal) Stop() {}
func (f *fakeTerminal) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, string(p))
	return len(p), nil
}
func (f *fakeTerminal) Writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.writes))
	copy(out, f.writes)
	return out
}
func (f *fakeTerminal) WriteString(s string)    { f.Write([]byte(s)) }
func (f *fakeTerminal) Size() (int, int)        { return f.w, f.h }
func (f *fakeTerminal) SetRaw() (func(), error) { f.raw = true; return func() {}, nil }
func (f *fakeTerminal) HideCursor()             { f.cursor = false }
func (f *fakeTerminal) ShowCursor()             { f.cursor = true }
func (f *fakeTerminal) ClearScreen()            {}
func (f *fakeTerminal) SetTitle(title string)   {}

func TestTUI_ShowSelector_OverlayVisible(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	cv.AddSystemMessage("hello")
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	items := []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}
	ch := engine.ShowSelector("Pick:", items, "")

	// Trigger a render
	engine.RenderNow()

	var buf strings.Builder
	for _, w := range term.Writes() {
		buf.WriteString(w)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "Pick:") {
		t.Errorf("selector title not in output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "alpha") {
		t.Errorf("selector item not in output:\n%s", rendered)
	}
	_ = ch
}

func TestTUI_ShowSelector_OverlayVisibleWithLongBuffer(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	for i := 0; i < 50; i++ {
		cv.AddSystemMessage("line content")
	}
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	items := []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}
	engine.ShowSelector("Pick:", items, "")
	engine.RenderNow()

	var buf strings.Builder
	for _, w := range term.Writes() {
		buf.WriteString(w)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "Pick:") {
		t.Errorf("selector title not in output with long buffer:\n%s", rendered)
	}
	if !strings.Contains(rendered, "alpha") {
		t.Errorf("selector item not in output with long buffer:\n%s", rendered)
	}
}

func TestTUI_ShowSelector_PersistsAfterCommandOutput(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	for i := 0; i < 30; i++ {
		cv.AddSystemMessage("line content")
	}
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	items := []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}
	engine.ShowSelector("Pick:", items, "")
	cv.AddSystemMessage("command output")
	engine.RenderNow()
	engine.RenderNow()

	var buf strings.Builder
	for _, w := range term.Writes() {
		buf.WriteString(w)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "Pick:") {
		t.Errorf("selector title not in output after command output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "alpha") {
		t.Errorf("selector item not in output after command output:\n%s", rendered)
	}
}

// pasteCatcher is a focusable component that records every string it receives.
type pasteCatcher struct {
	events  []string
	focused bool
}

func (p *pasteCatcher) Render(width int) []string { return nil }
func (p *pasteCatcher) HandleInput(data string)   { p.events = append(p.events, data) }
func (p *pasteCatcher) Invalidate()               {}
func (p *pasteCatcher) SetFocused(f bool)         { p.focused = f }
func (p *pasteCatcher) Focused() bool             { return p.focused }

func TestTUI_PasteEventPassedThrough(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	pc := &pasteCatcher{}
	engine.AddChild(pc)
	engine.SetFocus(pc)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	// Bracketed-paste content arrives as a single raw multi-character string.
	term.onInput("hello world\nsecond line")

	if len(pc.events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(pc.events), pc.events)
	}
	if pc.events[0] != "hello world\nsecond line" {
		t.Errorf("event = %q, want raw paste string", pc.events[0])
	}
}

func TestTUI_SingleKeyStillDecoded(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	pc := &pasteCatcher{}
	engine.AddChild(pc)
	engine.SetFocus(pc)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	term.onInput("a")

	if len(pc.events) != 1 || pc.events[0] != "a" {
		t.Errorf("expected ['a'], got %v", pc.events)
	}
}

func TestShowInput_SubmitHidesOverlay(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	ch := engine.ShowInput("Enter model name:", "local-model")

	// Clear the default value, type "gpt-4", and submit.
	term.onInput("ctrl+u")
	term.onInput("g")
	term.onInput("p")
	term.onInput("t")
	term.onInput("-")
	term.onInput("4")
	term.onInput("enter")

	select {
	case v := <-ch:
		if v != "gpt-4" {
			t.Errorf("channel value = %q, want gpt-4", v)
		}
	default:
		t.Fatal("expected value on result channel")
	}

	if len(engine.overlayStack) != 0 {
		t.Errorf("overlay still visible after submit: %d overlays", len(engine.overlayStack))
	}
}

func TestTUI_KeyReleaseIgnoredByOverlay(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	items := []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "c", Label: "gamma"},
	}
	result := make(chan string, 1)
	sel := NewSelector("Pick:", items, "", result)
	engine.ShowOverlay(sel, OverlayOptions{CaptureInput: true})

	// Press Down: should move selection from 0 to 1.
	term.onInput("\x1b[B")
	if sel.selected != 1 {
		t.Errorf("after press Down, selected = %d, want 1", sel.selected)
	}

	// Release Down (Kitty protocol event type 3): must be ignored.
	term.onInput("\x1b[B;1:3u")
	if sel.selected != 1 {
		t.Errorf("after release Down, selected = %d, want 1 (release was processed)", sel.selected)
	}
}

func TestShowInput_CtrlCCancelsOverlay(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	ch := engine.ShowInput("Enter model name:", "")
	term.onInput("partial")
	term.onInput("ctrl+c")

	select {
	case v := <-ch:
		if v != "" {
			t.Errorf("cancelled channel value = %q, want empty", v)
		}
	default:
		t.Fatal("expected empty value on result channel after cancel")
	}

	if len(engine.overlayStack) != 0 {
		t.Errorf("overlay still visible after cancel: %d overlays", len(engine.overlayStack))
	}
	if engine.stopped.Load() {
		t.Error("TUI was stopped by Ctrl+C in overlay")
	}
}

func TestTUI_Stopped_ClosedAfterStop(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	stopped := engine.Stopped()

	// Engine is running — channel must NOT be closed.
	select {
	case <-stopped:
		t.Fatal("Stopped() channel closed before Stop() called")
	default:
	}

	engine.Stop()

	// After Stop — channel must be closed.
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stopped() channel not closed within 1s after Stop()")
	}
}

func TestTUI_Stopped_BlocksBeforeStop(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	stopped := engine.Stopped()

	// A goroutine blocked on Stopped() must unblock when the engine stops.
	done := make(chan struct{})
	go func() {
		<-stopped
		close(done)
	}()

	engine.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goroutine blocked on Stopped() did not unblock within 1s")
	}
}

// TestOverlayHandle_IsVisible is a regression test for the review-submit bug:
// after an action closed the overlay, the host still re-captured input for the
// hidden overlay, routing all subsequent keystrokes to a dead component so the
// app appeared frozen (e.g. /quit was eaten). IsVisible must report false once
// Hide has run, and SetCaptureInput on a hidden overlay must not be有害.
func TestOverlayHandle_IsVisible(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	engine.AddChild(NewChatViewport())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	comp := NewInput()
	handle := engine.ShowOverlay(comp, OverlayOptions{CaptureInput: true, Height: 3})
	if !handle.IsVisible() {
		t.Fatal("overlay should be visible immediately after ShowOverlay")
	}

	handle.Hide()
	if handle.IsVisible() {
		t.Fatal("overlay should not be visible after Hide")
	}
	// SetCaptureInput on a hidden overlay must be a safe no-op (no panic,
	// no crash) — the host may call it from a callback that raced with Hide.
	handle.SetCaptureInput(true)
	handle.SetCaptureInput(false)
}

func TestTUI_SelectorOverlayNavigatesWithoutDuplicates(t *testing.T) {
	// Regression: navigating a Selector overlay must not cause item duplication
	// when the base buffer grows. The old code could produce stale overlay
	// content at the old buffer position while the new overlay also appeared.
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	// Fill the viewport enough that an overlay would trigger viewport scrolling
	for i := 0; i < 30; i++ {
		cv.AddSystemMessage("line content")
	}
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	items := []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "c", Label: "gamma"},
	}
	result := make(chan string, 1)
	sel := NewSelector("Pick:", items, "", result)
	engine.ShowOverlay(sel, OverlayOptions{CaptureInput: true, Height: 7})
	engine.RenderNow()

	// Grow the base buffer (simulates a chat message arriving while overlay is open)
	cv.AddSystemMessage("new message while overlay active")
	engine.RenderNow()

	// Navigate down the selector
	term.onInput("\x1b[B") // Down
	engine.RenderNow()
	term.onInput("\x1b[B") // Down
	engine.RenderNow()

	// Use the virtual buffer to check for corruption
	// Buffer() returns the composited (prevLines) state
	buf := engine.Buffer()
	plainLines := make([]string, len(buf))
	for i, line := range buf {
		plainLines[i] = ansi.Strip(line)
	}
	allPlain := strings.Join(plainLines, "\n")

	// The overlay title must still be visible
	if !strings.Contains(allPlain, "Pick:") {
		t.Errorf("selector title not found in buffer:\n%s", allPlain)
	}

	// Each item should appear exactly once in the buffer
	alphaCount := strings.Count(allPlain, "alpha")
	betaCount := strings.Count(allPlain, "beta")
	gammaCount := strings.Count(allPlain, "gamma")

	if alphaCount > 1 {
		t.Errorf("alpha appeared %d times in buffer, expected at most 1", alphaCount)
	}
	if betaCount > 1 {
		t.Errorf("beta appeared %d times in buffer, expected at most 1", betaCount)
	}
	if gammaCount > 1 {
		t.Errorf("gamma appeared %d times in buffer, expected at most 1", gammaCount)
	}

	// The › marker should appear exactly once (on the selected item)
	markerCount := 0
	for _, line := range plainLines {
		if strings.Contains(line, "\u203a") {
			markerCount++
		}
	}
	if markerCount > 1 {
		t.Errorf("selection marker › appears in %d lines, expected at most 1", markerCount)
	}
}

func TestTUI_SelectorOverlaySurvivesBufferShrink(t *testing.T) {
	// Regression: when the buffer shrinks while a Selector overlay is visible,
	// the overlay must remain intact and navigable without corruption.
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	for i := 0; i < 35; i++ {
		cv.AddSystemMessage("line content")
	}
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	items := []SelectorItem{
		{Value: "x", Label: "xray"},
		{Value: "y", Label: "yankee"},
		{Value: "z", Label: "zulu"},
	}
	result := make(chan string, 1)
	sel := NewSelector("Pick:", items, "", result)
	engine.ShowOverlay(sel, OverlayOptions{CaptureInput: true, Height: 7})
	engine.RenderNow()

	// Shrink the base buffer (remove messages)
	for i := 0; i < 5; i++ {
		cv.RemoveLastMessage()
	}
	engine.RenderNow()

	// Navigate to verify overlay is still responsive
	term.onInput("\x1b[B") // Down
	engine.RenderNow()

	// Overlay should still be usable
	var buf strings.Builder
	for _, w := range term.Writes() {
		buf.WriteString(w)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "Pick:") {
		t.Errorf("selector title lost after buffer shrink:\n%s", rendered)
	}
	if !strings.Contains(rendered, "yankee") {
		t.Errorf("selector item lost after buffer shrink:\n%s", rendered)
	}
}
