// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// viewAt returns the View of the i-th entry via the Model's snapshot ordering.
// Tests assert through the public Model API, not internal fields.
func viewAt(cv *ChatViewport, i int) Component {
	idx := 0
	var found Component
	cv.ForEach(func(e MessageEntry) {
		if idx == i {
			found = e.View
		}
		idx++
	})
	return found
}

func TestUpdateLastMessage_Companion(t *testing.T) {
	cv := NewChatViewport()
	cv.AddSystemMessage("system msg")
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: ""})

	cv.UpdateLastMessage("companion chunk 1", ConsoleCompanionMessage)

	last := cv.LastView([]ConsoleItemType{ConsoleCompanionMessage})
	g, ok := last.(*gutteredComponent)
	if !ok {
		t.Fatalf("expected *gutteredComponent, got %T", last)
	}
	if !renderContains(g.Render(40), "companion chunk 1") {
		t.Errorf("expected 'companion chunk 1', got %v", g.Render(40))
	}
}

func TestUpdateLastMessage_Companion_MultipleChunks(t *testing.T) {
	cv := NewChatViewport()
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: ""})
	cv.UpdateLastMessage("hello", ConsoleCompanionMessage)
	cv.UpdateLastMessage("hello world", ConsoleCompanionMessage)

	g := cv.LastView([]ConsoleItemType{ConsoleCompanionMessage}).(*gutteredComponent)
	if !renderContains(g.Render(40), "hello world") {
		t.Errorf("expected 'hello world', got %v", g.Render(40))
	}
}

func TestUpdateLastMessage_Companion_IgnoresOtherTypes(t *testing.T) {
	cv := NewChatViewport()
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: "initial"})
	cv.AddSystemMessage("system msg")

	cv.UpdateLastMessage("assistant text", ConsoleAssistantMessage)

	g := viewAt(cv, 0).(*gutteredComponent)
	if !renderContains(g.Render(40), "initial") {
		t.Errorf("companion should keep 'initial', got %v", g.Render(40))
	}
}

func TestAddMessage_Companion_WrapsWithGutter(t *testing.T) {
	cv := NewChatViewport()
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: "review note"})
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*gutteredComponent); !ok {
		t.Fatalf("expected *gutteredComponent, got %T", viewAt(cv, 0))
	}
}

func TestAddThinkingBlock_CreatesThinkingComponent(t *testing.T) {
	cv := NewChatViewport()
	cv.AddThinkingBlock("", true)
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*thinkingBlock); !ok {
		t.Fatalf("expected *thinkingBlock, got %T", viewAt(cv, 0))
	}
}

func TestUpdateLastMessage_ThinkingBlock(t *testing.T) {
	cv := NewChatViewport()
	cv.AddThinkingBlock("", true)
	cv.UpdateLastMessage("thinking chunk", ConsoleThinkingBlock)

	tb := cv.LastView([]ConsoleItemType{ConsoleThinkingBlock}).(*thinkingBlock)
	if !renderContains(tb.Render(40), "thinking chunk") {
		t.Errorf("expected 'thinking chunk', got %v", tb.Render(40))
	}
}

func TestAddToolExecution_NoSeparatorBeforeFirst(t *testing.T) {
	cv := NewChatViewport()
	cv.AddToolExecution("read", `{"path":"a.txt"}`)
	if cv.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", cv.Len())
	}
}

func TestAddToolExecution_ConsecutiveTools(t *testing.T) {
	cv := NewChatViewport()
	cv.AddToolExecution("read", `{"path":"a.txt"}`)
	cv.AddToolExecution("read", `{"path":"b.txt"}`)
	if cv.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", cv.Len())
	}
}

func TestAddBashExecution_UsesUnifiedToolComponent(t *testing.T) {
	cv := NewChatViewport()
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	if tc == nil || tc.toolName != "bash" {
		t.Fatalf("expected bash tool execution, got %+v", tc)
	}
}

func TestRemoveLastMessageOfType_RemovesMatchingType(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	cv.AddAssistantMessage("partial assistant")

	if !cv.RemoveLastMessageOfType(ConsoleAssistantMessage) {
		t.Fatal("expected removal to return true")
	}
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry after removal, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*userMessage); !ok {
		t.Errorf("expected remaining userMessage, got %T", viewAt(cv, 0))
	}
}

func TestRemoveLastMessageOfType_KeepsNonMatchingType(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	if cv.RemoveLastMessageOfType(ConsoleAssistantMessage, ConsoleThinkingBlock) {
		t.Fatal("expected false for non-matching type")
	}
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
}

func TestRemoveLastMessageOfType_RemovesThinkingBlock(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	cv.AddThinkingBlock("partial thinking", true)
	if !cv.RemoveLastMessageOfType(ConsoleThinkingBlock) {
		t.Fatal("expected removal to return true")
	}
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
}

func TestUpdateLastMessage_ThinkingToAssistantTransition(t *testing.T) {
	cv := NewChatViewport()
	cv.AddThinkingBlock("", true)
	cv.UpdateLastMessage("thinking", ConsoleThinkingBlock)
	cv.AddAssistantMessage("")
	cv.UpdateLastMessage("assistant text", ConsoleAssistantMessage)

	if cv.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*thinkingBlock); !ok {
		t.Errorf("expected first thinkingBlock, got %T", viewAt(cv, 0))
	}
	if _, ok := viewAt(cv, 1).(*assistantMessage); !ok {
		t.Errorf("expected second assistantMessage, got %T", viewAt(cv, 1))
	}
}

// TestChatViewport_SnapshotExposesStructuredData verifies the Model exposes a
// pure-data snapshot for AI agent tooling (no View references, no ANSI).
func TestChatViewport_SnapshotExposesStructuredData(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("hi")
	cv.AddAssistantMessage("hello")
	snap := cv.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2, got %d", len(snap))
	}
	if snap[0].Type != ConsoleUserMessage || snap[0].Text != "hi" {
		t.Errorf("entry 0 wrong: %+v", snap[0])
	}
	if snap[1].Type != ConsoleAssistantMessage || snap[1].Text != "hello" {
		t.Errorf("entry 1 wrong: %+v", snap[1])
	}
	if snap[1].ID <= snap[0].ID {
		t.Errorf("IDs must be monotonic: %d then %d", snap[0].ID, snap[1].ID)
	}
}

func TestChatViewport_InvalidateRunningToolWidgets(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	tc.SetStatus(ToolRunning)

	cv.Render(80)
	genBefore := cv.generation
	cacheBefore := cv.renderCache.lines

	currentSpinnerFrame = "1"
	cv.InvalidateRunningToolWidgets()
	cv.Render(80) // patch happens on the render goroutine
	rendered1 := tc.Render(80)
	if len(rendered1) < 2 {
		t.Fatalf("tool widget rendered fewer than 2 lines: %v", rendered1)
	}

	currentSpinnerFrame = "2"
	cv.InvalidateRunningToolWidgets()
	cv.Render(80) // patch happens on the render goroutine
	rendered2 := tc.Render(80)
	if len(rendered2) < 2 {
		t.Fatalf("tool widget rendered fewer than 2 lines: %v", rendered2)
	}
	if rendered1[1] == rendered2[1] {
		t.Errorf("running tool widget header did not change after spinner tick: %q", rendered1[1])
	}
	if cv.generation != genBefore {
		t.Errorf("InvalidateRunningToolWidgets should not increment generation; got %d, want %d", cv.generation, genBefore)
	}
	if &cv.renderCache.lines[0] != &cacheBefore[0] {
		t.Errorf("InvalidateRunningToolWidgets should patch the frame cache in place, not reallocate")
	}

	// Non-running tools should not be invalidated again.
	tc.SetStatus(ToolSuccess)
	genBefore = cv.generation
	cv.InvalidateRunningToolWidgets()
	cv.Render(80)
	if cv.generation != genBefore {
		t.Errorf("non-running tools should not be invalidated")
	}
}

func TestChatViewport_SteeringPending_StaysAtBottom(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("hello")
	cv.AddSteeringPending("fix this")
	if cv.pendingSteering < 0 {
		t.Fatal("expected pending steering entry")
	}
	cv.AddAssistantMessage("working...")
	if cv.pendingSteering < 0 {
		t.Fatal("pending steering index should remain after adding message")
	}
	if cv.entries[cv.pendingSteering].Data.Type != ConsoleSteeringPending {
		t.Errorf("last entry should be steering pending, got %d", cv.entries[cv.pendingSteering].Data.Type)
	}
	if cv.entries[cv.pendingSteering].Data.Text != "fix this" {
		t.Errorf("pending text wrong: %q", cv.entries[cv.pendingSteering].Data.Text)
	}
	cv.ClearSteeringPending()
	if cv.pendingSteering >= 0 {
		t.Error("pending steering should be cleared")
	}
}

func renderContains(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// TestSteeringPending_Render_MultiLine: a steering message pasted with
// embedded newlines must render one box row per visual line — ansi.Wrap only
// accepts single paragraphs, and a returned "line" containing '\n' paints as
// several terminal rows, desyncing the compositor (overlapping redraw bug).
// TestSteeringPending_Render_LeadingBlanksSkipped: a message starting with
// blank lines must preview real content, not a blank row. The bubble is
// capped at ONE preview line; every additional wrapped line is reported as
// "+N lines" in the footer stat.
func TestSteeringPending_Render_LeadingBlanksSkipped(t *testing.T) {
	m := newSteeringPending("\n\nalpha\nbeta\ngamma\ndelta\nepsilon\nzeta")
	lines := m.Render(40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "alpha") {
		t.Errorf("expected first content line 'alpha' in preview, got:\n%s", joined)
	}
	// Only one preview line: the second content line must NOT be shown.
	if strings.Contains(joined, "beta") {
		t.Errorf("expected preview capped at 1 line, got:\n%s", joined)
	}
	// 8 wrapped visual lines total (2 leading blanks + 6 content), 1 shown:
	// footer must report "+7 lines".
	if !strings.Contains(joined, "+7 lines") {
		t.Errorf("expected '+7 lines' footer stat, got:\n%s", joined)
	}
}

func TestSteeringPending_Render_MultiLine(t *testing.T) {
	m := newSteeringPending("first line\nsecond line\n\nthird line")
	width := 40
	lines := m.Render(width)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	for i, l := range lines {
		if strings.ContainsRune(l, '\n') {
			t.Errorf("rendered line %d contains an embedded newline: %q", i, l)
		}
	}
	// Box structure: top border + 1 preview row + footer + bottom border.
	if got, want := len(lines), 4; got != want {
		t.Errorf("expected %d rows (border + 1 preview + footer + border), got %d: %q", want, got, lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✎ first line") {
		t.Errorf("expected preview line %q in render, got:\n%s", "✎ first line", joined)
	}
	// 4 wrapped visual lines, 1 shown → "+3 lines" stat.
	if !strings.Contains(joined, "+3 lines") {
		t.Errorf("expected '+3 lines' footer stat, got:\n%s", joined)
	}
}

// TestSteeringPending_Render_ShowsEditAffordance is the P2 regression test:
// the pending-steering bubble must advertise the Alt+E edit hotkey so users
// can discover that a queued steering message is editable.
func TestSteeringPending_Render_ShowsEditAffordance(t *testing.T) {
	m := newSteeringPending("please also fix the tests")
	out := ansi.Strip(strings.Join(m.Render(80), "\n"))
	if !strings.Contains(out, "Alt+E") {
		t.Errorf("steering bubble should advertise the Alt+E edit affordance, got:\n%s", out)
	}
}

// TestSteeringPending_Render_SanitizesControlBytes: pasted steering text may
// carry raw ESC bytes (e.g. a copied terminal log). The box must show them as
// literal text, never forward them to the terminal.
func TestSteeringPending_Render_SanitizesControlBytes(t *testing.T) {
	m := newSteeringPending("look: \x1b[2Kbad")
	lines := m.Render(60)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "\x1b[2K") {
		t.Errorf("raw clear-line sequence leaked into render: %q", joined)
	}
	if !strings.Contains(joined, `\e[2K`) {
		t.Errorf("expected literal \\e[2K in render, got:\n%s", joined)
	}
}

// TestSteeringPending_Render_BordersAndDefaultBackground: every row of the
// box must be delimited by │ side borders (the old renderer drew ┌/└ corners
// but left content rows open), and no row may carry a background SGR fill
// (the old renderer painted input_bg only on the text span, leaving a
// default-background stripe on the right edge).
func TestSteeringPending_Render_BordersAndDefaultBackground(t *testing.T) {
	m := newSteeringPending("border check")
	lines := m.Render(40)
	if len(lines) != 4 {
		t.Fatalf("expected 4 rows (border+preview+footer+border), got %d", len(lines))
	}
	for i, l := range lines {
		if strings.Contains(l, "\x1b[48") {
			t.Errorf("line %d carries a background fill; bubble must use default bg: %q", i, l)
		}
		if vw := visibleWidth(l); vw != 40 {
			t.Errorf("line %d visible width = %d, want exactly 40: %q", i, vw, l)
		}
	}
	// Content rows (preview + footer) must open AND close with │.
	for _, i := range []int{1, 2} {
		plain := ansi.Strip(lines[i])
		if !strings.HasPrefix(plain, "│") || !strings.HasSuffix(plain, "│") {
			t.Errorf("content row %d must be delimited by │ on both sides: %q", i, plain)
		}
	}
}

// TestSteeringPending_Render_MessageCountStat: with more than one queued
// message the footer must show "(N messages)" so the user sees the whole
// queue is pending, not just the last submission.
func TestSteeringPending_Render_MessageCountStat(t *testing.T) {
	m := newSteeringPending("first")
	m.SetMessages([]string{"first", "second", "third"})
	out := ansi.Strip(strings.Join(m.Render(50), "\n"))
	if !strings.Contains(out, "(3 messages)") {
		t.Errorf("expected '(3 messages)' stat for a 3-message queue, got:\n%s", out)
	}
	// Single message: no count stat.
	m.SetMessages([]string{"only"})
	out = ansi.Strip(strings.Join(m.Render(50), "\n"))
	if strings.Contains(out, "messages)") {
		t.Errorf("single-message queue must not show a count stat, got:\n%s", out)
	}
}

// TestChatViewport_AddSteeringPending_MergesIntoSingleBubble: a second
// steering submission must merge into the existing bubble (one entry, two
// queued messages) instead of replacing it and hiding the first message.
func TestChatViewport_AddSteeringPending_MergesIntoSingleBubble(t *testing.T) {
	cv := NewChatViewport()
	cv.AddSteeringPending("first steering")
	cv.AddSteeringPending("second steering")

	if cv.pendingSteering < 0 {
		t.Fatal("expected pending steering entry")
	}
	e := cv.entries[cv.pendingSteering]
	if e.Data.Type != ConsoleSteeringPending {
		t.Fatalf("pending entry type = %d, want ConsoleSteeringPending", e.Data.Type)
	}
	sv, ok := e.View.(*steeringPending)
	if !ok {
		t.Fatalf("pending view type = %T, want *steeringPending", e.View)
	}
	msgs := sv.Messages()
	if len(msgs) != 2 || msgs[0] != "first steering" || msgs[1] != "second steering" {
		t.Errorf("merged messages = %v, want [first steering, second steering]", msgs)
	}
	if e.Data.Text != "first steering\n\nsecond steering" {
		t.Errorf("entry data text = %q, want merged content", e.Data.Text)
	}
	// Only ONE steering entry may exist in the conversation.
	count := 0
	for _, entry := range cv.entries {
		if entry.Data.Type == ConsoleSteeringPending {
			count++
		}
	}
	if count != 1 {
		t.Errorf("steering entries = %d, want exactly 1 (merged bubble)", count)
	}
}

// TestChatViewport_Append_PendingSteeringReappendInvalidatesCache is the
// screen-corruption regression: when a new message arrives while the
// steering bubble is pending, Append removes and re-appends the bubble. The
// re-appended entry must be fully invalidated — a stale lineOffset made
// updateLastEntry patch the frame cache at the OLD position, leaving ghost
// content on screen until the next resize forced a full rebuild.
func TestChatViewport_Append_PendingSteeringReappendInvalidatesCache(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("hello")
	cv.AddSteeringPending("fix this")

	// Render so every entry has a populated cache (this is when stale
	// lineOffset values do damage).
	cv.Render(80)

	cv.AddAssistantMessage("working...")
	if cv.pendingSteering < 0 {
		t.Fatal("pending steering index should remain after adding message")
	}
	e := cv.entries[cv.pendingSteering]
	if !e.dirty || e.renderedLines != nil || e.renderedWidth != 0 {
		t.Errorf("re-appended pending entry must be re-rendered, got dirty=%v lines=%v w=%d",
			e.dirty, e.renderedLines, e.renderedWidth)
	}

	// The frame cache must render the bubble at the true bottom position.
	lines := cv.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "working...") || !strings.Contains(joined, "fix this") {
		t.Errorf("frame must contain both the new message and the bubble, got:\n%s", joined)
	}
	// The bubble's top border must come AFTER the assistant message.
	if strings.Index(joined, "working...") > strings.Index(joined, "fix this") {
		t.Errorf("bubble must render below the new message, got:\n%s", joined)
	}
}

// TestChatViewport_HasRunningToolWidgets verifies B002: the viewport correctly
// reports whether any tool widget is in ToolRunning state, which the render
// loop uses to decide whether to keep the live refresh ticker alive.
func TestChatViewport_HasRunningToolWidgets(t *testing.T) {
	cv := NewChatViewport()

	// No tools: false.
	if cv.HasRunningToolWidgets() {
		t.Error("expected false when no tool widgets exist")
	}

	// Add a pending tool: false (not running yet).
	tc1 := cv.AddToolExecution("read", `{"path":"a.go"}`)
	if cv.HasRunningToolWidgets() {
		t.Error("expected false for pending tool")
	}

	// Set to running: true.
	tc1.SetStatus(ToolRunning)
	if !cv.HasRunningToolWidgets() {
		t.Error("expected true when a tool is running")
	}

	// Add a second running tool: still true.
	tc2 := cv.AddToolExecution("bash", `{"command":"make"}`)
	tc2.SetStatus(ToolRunning)
	if !cv.HasRunningToolWidgets() {
		t.Error("expected true with two running tools")
	}

	// Complete first tool: still true (second is running).
	tc1.SetStatus(ToolSuccess)
	if !cv.HasRunningToolWidgets() {
		t.Error("expected true when one tool is still running")
	}

	// Complete all: false.
	tc2.SetStatus(ToolSuccess)
	if cv.HasRunningToolWidgets() {
		t.Error("expected false when all tools are complete")
	}

	// Error state also counts as not running.
	tc3 := cv.AddToolExecution("write", `{"path":"b.go"}`)
	tc3.SetStatus(ToolError)
	if cv.HasRunningToolWidgets() {
		t.Error("expected false for errored tool")
	}
}
