// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// testTerminal implements tui.Terminal for testing.
type testTerminal struct {
	w, h   int
	writes []string
}

func (t *testTerminal) Start(onInput func(string), onResize func()) {}
func (t *testTerminal) Stop()                                       {}
func (t *testTerminal) Write(p []byte) (int, error) {
	t.writes = append(t.writes, string(p))
	return len(p), nil
}
func (t *testTerminal) WriteString(s string)    { t.writes = append(t.writes, s) }
func (t *testTerminal) Size() (int, int)        { return t.w, t.h }
func (t *testTerminal) SetRaw() (func(), error) { return func() {}, nil }
func (t *testTerminal) HideCursor()             {}
func (t *testTerminal) ShowCursor()             {}
func (t *testTerminal) ClearScreen()            {}
func (t *testTerminal) SetTitle(title string)   {}

// lastTool returns the most recent ToolExecutionComponent child of the chat
// viewport, or nil. Used by tests that previously inspected the removed
// subs.activeTool / subs.activeTools fields; the ToolCallTracker is now the
// single source of truth, so tests observe widgets through the chat.
func lastTool(cv *tui.ChatViewport) *tui.ToolExecutionComponent {
	if cv == nil {
		return nil
	}
	children := cv.Children()
	for i := len(children) - 1; i >= 0; i-- {
		if tc, ok := children[i].(*tui.ToolExecutionComponent); ok {
			return tc
		}
	}
	return nil
}

func testSubsystems() *subsystems {
	return &subsystems{
		chat:         tui.NewChatViewport(),
		statusMsg:    tui.NewStatusMsg(),
		footer:       tui.NewFooter(),
		events:       event.MakeBus(16, 16, 16, 16),
		agentStreams: newAgentStreamRegistry(),
		cfg: &config.Config{
			TUI: config.TUIConfig{
				Transparency: config.TransparencyConfig{
					ShowThinking: true,
				},
			},
		},
	}
}

func containsRendered(cv *tui.ChatViewport, substr string) bool {
	lines := cv.Render(80)
	for _, line := range lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// TestClarify_InputTitleShowsProgressNotQuestion pins the fix for the
// "long series of questions" UX bug on the FREE-TEXT path: the input-line
// title must be a compact progress cue ("... — 2 of 5"), NOT the full
// question text (which lives in the card bubble). Previously the entire
// question/options were stuffed into the editor title, so a long series
// ballooned the title with no progress cue.
//
// (Cards WITH options no longer touch the editor title — they are answered
// through a navigable selector overlay; see TestClarify_OptionsUseSelector.)
func TestClarify_InputTitleShowsProgressNotQuestion(t *testing.T) {
	term := &testTerminal{w: 100, h: 30}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()
	engine.RunLoops()

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.inputEditor = inp
	app := New(subs)

	longQuestion := "Which of the many plausible approaches should the planner take when decomposing this work into independently schedulable sub-agent tasks?"
	card := tui.NewClarifyCard("Clarifications needed", "ctx", longQuestion, nil) // free-text path
	card.SetProgress(2, 5)

	done := make(chan struct{})
	go func() {
		_, _ = app.clarify(card)
		close(done)
	}()
	defer engine.ApplySync(func() { app.cancelPendingMainInput() })
	got := waitForTitle(t, engine, inp, "2 of 5")
	if strings.Contains(got, longQuestion) {
		t.Errorf("input title must NOT contain the full question text, got %q", got)
	}
	if !strings.Contains(got, "Clarifications needed") {
		t.Errorf("input title should contain the card title, got %q", got)
	}
}

// waitForTitle polls the editor title until it contains want (or the timeout
// fires), returning the last seen title. clarify() runs on a tool goroutine and
// posts its state mutations via apply, so the title is not set synchronously.
// The Editor title is commandLoop-owned state, so each read is marshalled onto
// the loop via ApplySync to stay race-free.
func waitForTitle(t *testing.T, engine *tui.TUI, inp *tui.Editor, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		engine.ApplySync(func() { got = inp.Title() })
		if strings.Contains(got, want) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("editor title never contained %q; last=%q", want, got)
	return got
}

// TestClarify_StandaloneTitleNoProgress ensures a single question (total<=1)
// does not get a spurious "1 of 1" progress label.
func TestClarify_StandaloneTitleNoProgress(t *testing.T) {
	term := &testTerminal{w: 100, h: 30}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()
	engine.RunLoops()

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.inputEditor = inp
	app := New(subs)

	card := tui.NewClarifyCard("Clarifications needed", "", "Pick one?", nil) // no progress set
	done := make(chan struct{})
	go func() {
		_, _ = app.clarify(card)
		close(done)
	}()
	defer engine.ApplySync(func() { app.cancelPendingMainInput() })
	got := waitForTitle(t, engine, inp, "Clarifications needed")
	if strings.Contains(got, " of ") {
		t.Errorf("standalone clarify should not show progress, got %q", got)
	}
	if got != "Clarifications needed" {
		t.Errorf("standalone title = %q, want %q", got, "Clarifications needed")
	}
}

// clarifyKeyTerminal is a testTerminal that captures the input handler so a
// test can drive selector keys (up/down/enter/esc) into the engine.
type clarifyKeyTerminal struct {
	testTerminal
	onInput func(string)
}

func (t *clarifyKeyTerminal) Start(onInput func(string), _ func()) { t.onInput = onInput }

func (t *clarifyKeyTerminal) sendKey(s string) {
	if t.onInput != nil {
		t.onInput(s)
	}
}

// TestClarify_OptionsUseSelector pins the Pi showAuthSelect behavior: a
// ClarifyCard WITH options is answered through a navigable selector overlay
// (arrow keys + enter), NOT the free-text main input line. The editor title
// must remain untouched and the picked option is returned.
func TestClarify_OptionsUseSelector(t *testing.T) {
	term := &clarifyKeyTerminal{}
	term.w, term.h = 100, 30
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()
	engine.RunLoops()

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.inputEditor = inp
	app := New(subs)

	card := tui.NewClarifyCard("OpenAI Codex login method", "", "Pick a login method",
		[]string{"Sign in with browser", "Use a device code"})

	type answer struct {
		text string
		ok   bool
	}
	ansCh := make(chan answer, 1)
	go func() {
		text, ok := app.clarify(card)
		ansCh <- answer{text, ok}
	}()

	// Wait for the selector overlay to appear, then navigate down → enter.
	waitForVisibleText(t, engine, "Sign in with browser")
	engine.ApplySync(func() {
		if got := inp.Title(); got != "" {
			t.Errorf("editor title must stay untouched for option cards, got %q", got)
		}
		if app.pendingInput != nil {
			t.Error("option cards must not register a main-input request")
		}
	})
	term.sendKey("\x1b[B") // down → "Use a device code"
	term.sendKey("\r")     // enter

	select {
	case got := <-ansCh:
		if !got.ok || got.text != "Use a device code" {
			t.Errorf("clarify = (%q, %v), want (%q, true)", got.text, got.ok, "Use a device code")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clarify did not return after selector confirm")
	}
}

// TestClarify_OptionsSelectorCancel verifies Esc on the option selector
// reports cancellation (ok==false) rather than blocking forever.
func TestClarify_OptionsSelectorCancel(t *testing.T) {
	term := &clarifyKeyTerminal{}
	term.w, term.h = 100, 30
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()
	engine.RunLoops()

	chat := tui.NewChatViewport()
	engine.AddChild(chat)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	app := New(subs)

	card := tui.NewClarifyCard("Pick", "", "?", []string{"a", "b"})
	type answer struct {
		text string
		ok   bool
	}
	ansCh := make(chan answer, 1)
	go func() {
		text, ok := app.clarify(card)
		ansCh <- answer{text, ok}
	}()

	waitForVisibleText(t, engine, "a")
	term.sendKey("\x1b") // esc → cancel

	select {
	case got := <-ansCh:
		if got.ok || got.text != "" {
			t.Errorf("cancelled clarify = (%q, %v), want (\"\", false)", got.text, got.ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clarify did not return after selector cancel")
	}
}

// waitForVisibleText polls the rendered frame until substr appears.
func waitForVisibleText(t *testing.T, engine *tui.TUI, substr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		engine.RenderNow()
		for _, l := range engine.AgentFrame().Visible {
			if strings.Contains(l, substr) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	var b strings.Builder
	for _, l := range engine.AgentFrame().Visible {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	t.Fatalf("frame never showed %q; visible:\n%s", substr, b.String())
}

func TestInitialFooterData_ResolvesProvider(t *testing.T) {
	cfg := &config.Config{
		Providers:   []config.ProviderConfig{{ID: "google", Preferred: true}},
		Models:      []config.ModelConfig{{ID: "gemma", ProviderID: "google", Model: "gemma-4-e4b"}},
		ActiveModel: "gemma",
	}
	subs := &subsystems{cfg: cfg, providerMgr: provider.NewProviderManager(cfg)}
	app := New(subs)
	data := app.initialFooterData()
	want := "(google) gemma-4-e4b"
	if data.Model != want {
		t.Errorf("Model = %q, want %q", data.Model, want)
	}
}

func TestHandleStreamContent_ReplayUserMessage(t *testing.T) {
	app := New(testSubsystems())
	ev := &agentic.OutputEvent{
		Type:     agentic.EventContent,
		Role:     agentic.User,
		Text:     "user replay",
		Metadata: map[string]string{"replay": "true"},
	}
	app.handleStreamContent(ev)

	if !containsRendered(app.subs.chat, "user replay") {
		t.Errorf("expected replayed user message to be rendered")
	}
}

func TestHandleStreamContent_LiveUserMessageIgnored(t *testing.T) {
	app := New(testSubsystems())
	app.subs.chat.AddUserMessage("existing")
	ev := &agentic.OutputEvent{
		Type: agentic.EventContent,
		Role: agentic.User,
		Text: "live user",
	}
	app.handleStreamContent(ev)

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	if strings.Contains(rendered, "live user") {
		t.Errorf("expected live user content event to be suppressed")
	}
}

// TestHandleStreamContent_SteeringDrainedClearsBubble is the regression test
// for the stale-steering-bubble bug: when the agent drains the mid-turn
// steering queue (weaving the text into the conversation), the app must clear
// the pending steering bubble and render the consumed text as a user message.
// Before the fix the bubble stayed up because only the turn-end leftover path
// (handleSteeringInjected) cleared it.
func TestHandleStreamContent_SteeringDrainedClearsBubble(t *testing.T) {
	app := New(testSubsystems())
	sc := tui.NewSteeringChrome()
	sc.Add("also fix the tests")
	app.subs.steeringChrome = sc

	ev := &agentic.OutputEvent{
		Type:     agentic.EventContent,
		Role:     agentic.User,
		Text:     "also fix the tests",
		Metadata: map[string]string{"steering_drained": "true"},
	}
	app.handleStreamContent(ev)

	if sc.HasPending() {
		t.Error("steering bubble should be cleared once the queue is drained")
	}
	if !containsRendered(app.subs.chat, "also fix the tests") {
		t.Error("drained steering text should render as a user message")
	}
}

func TestHandleStreamContent_SystemNotificationRendersBubble(t *testing.T) {
	app := New(testSubsystems())
	ev := &agentic.OutputEvent{
		Type:     agentic.EventContent,
		Role:     agentic.System,
		Text:     "Error: 503 - Inference is temporarily unavailable (failover_exhausted) - retrying",
		Metadata: map[string]string{"category": "system-notification"},
	}
	app.handleStreamContent(ev)

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	if !strings.Contains(rendered, "Error: 503") {
		t.Errorf("expected system notification to be rendered as chat bubble, got:\n%s", rendered)
	}
}

func TestHandleStreamContent_SystemPromptSuppressed(t *testing.T) {
	app := New(testSubsystems())
	ev := &agentic.OutputEvent{
		Type: agentic.EventContent,
		Role: agentic.System,
		Text: "You are a helpful assistant.",
	}
	app.handleStreamContent(ev)

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	if strings.Contains(rendered, "You are a helpful assistant") {
		t.Errorf("expected plain system prompt content to be suppressed")
	}
}

func TestHandleStreamContent_CreatesThinkingBlock(t *testing.T) {
	app := New(testSubsystems())
	ev := &agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateThinking,
		Role:  agentic.Assistant,
		Text:  "thinking chunk",
	}
	app.handleStreamContent(ev)

	if !containsRendered(app.subs.chat, "thinking chunk") {
		t.Errorf("expected rendered output to contain 'thinking chunk'")
	}
}

func TestHandleStreamContent_HidesThinkingWhenDisabled(t *testing.T) {
	app := New(testSubsystems())
	app.subs.cfg.TUI.Transparency.ShowThinking = false
	ev := &agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateThinking,
		Role:  agentic.Assistant,
		Text:  "thinking chunk",
	}
	app.handleStreamContent(ev)

	if containsRendered(app.subs.chat, "thinking chunk") {
		t.Errorf("expected no thinking output when ShowThinking=false")
	}
}

func TestHandleStreamContent_TransitionsToAssistant(t *testing.T) {
	app := New(testSubsystems())
	app.handleStreamContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateThinking,
		Role:  agentic.Assistant,
		Text:  "thinking",
	})
	app.handleStreamContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Role:  agentic.Assistant,
		Text:  "answer",
	})

	if !containsRendered(app.subs.chat, "thinking") {
		t.Errorf("expected thinking text to remain visible")
	}
	if !containsRendered(app.subs.chat, "answer") {
		t.Errorf("expected assistant text to be visible")
	}
}

func TestHandleStreamContent_ToolCallBreaksThinkingBlock(t *testing.T) {
	app := New(testSubsystems())

	app.handleStreamContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateThinking,
		Role:  agentic.Assistant,
		Text:  "first thought",
	})
	app.handleStateChange(&agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateToolCall,
	})
	app.handleStreamContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateThinking,
		Role:  agentic.Assistant,
		Text:  "second thought",
	})

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	count := strings.Count(rendered, "thinking...")
	if count < 2 {
		t.Errorf("expected two separate thinking blocks, found %d 'thinking...' headers in:\n%s", count, rendered)
	}
}

func TestHandleToolCall_EndsActiveThinkingStream(t *testing.T) {
	app := New(testSubsystems())

	app.handleStreamContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateThinking,
		Role:  agentic.Assistant,
		Text:  "first thought",
	})
	app.handleToolCall(&agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		State:     agentic.StateToolCall,
		ToolName:  "bash",
		ToolInput: `{"command":"ls"}`,
	})
	app.handleStreamContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateThinking,
		Role:  agentic.Assistant,
		Text:  "second thought",
	})

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	count := strings.Count(rendered, "thinking...")
	if count < 2 {
		t.Errorf("expected two separate thinking blocks after tool call, found %d in:\n%s", count, rendered)
	}
}

func TestHandleStreamContent_ThinkingAndContentAlternate(t *testing.T) {
	app := New(testSubsystems())

	app.handleStreamContent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateThinking, Role: agentic.Assistant, Text: "thought1 "})
	app.handleStreamContent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: "answer1 "})
	app.handleStreamContent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateThinking, Role: agentic.Assistant, Text: "thought2 "})
	app.handleStreamContent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: "answer2"})

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	thinkingCount := strings.Count(rendered, "thinking...")
	if thinkingCount < 2 {
		t.Errorf("expected two thinking blocks, got %d in:\n%s", thinkingCount, rendered)
	}
}

func TestHandleToolResult_BashCompletesOnExitLine(t *testing.T) {
	app := New(testSubsystems())
	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"echo hi"}`})
	tc := lastTool(app.subs.chat)

	app.handleToolResult(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "bash", Text: "hi\nDuration: 0.01s\n"})

	if tc.Status() != tui.ToolSuccess {
		t.Errorf("expected ToolSuccess, got %v", tc.Status())
	}
}

func TestHandleToolResult_NonBashMarksErrorOnErrorPrefix(t *testing.T) {
	app := New(testSubsystems())
	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read", ToolInput: `{"path":"missing.txt"}`})
	tc := lastTool(app.subs.chat)

	app.handleToolResult(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "read", Text: "Error: file not found\nHint: See /docs TOOLS"})

	if tc.Status() != tui.ToolError {
		t.Errorf("expected ToolError, got %v", tc.Status())
	}
}

func TestHandleToolResult_NonBashMarksSuccess(t *testing.T) {
	app := New(testSubsystems())
	app.subs.statusMsg = tui.NewStatusMsg()
	app.subs.footer = tui.NewFooter()
	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read", ToolInput: `{"path":"ok.txt"}`})
	tc := lastTool(app.subs.chat)
	app.subs.footer.SetModelBusy(true)

	app.handleToolResult(&agentic.OutputEvent{Type: agentic.EventToolResult, Text: "file contents"})

	if tc.Status() != tui.ToolSuccess {
		t.Errorf("expected ToolSuccess, got %v", tc.Status())
	}
	if app.subs.footer.Data().ModelBusy {
		t.Errorf("expected model busy cleared after tool result")
	}
}

func TestHandleToolResult_MultipleToolsWithIDs(t *testing.T) {
	app := New(testSubsystems())
	app.subs.statusMsg = tui.NewStatusMsg()
	app.subs.footer = tui.NewFooter()

	app.handleToolCall(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "bash",
		ToolInput:  `{"command":"echo a"}`,
		ToolCallID: "c1",
	})
	app.handleToolCall(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "bash",
		ToolInput:  `{"command":"echo b"}`,
		ToolCallID: "c2",
	})

	app.handleToolResult(&agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		ToolCallID: "c1",
		Text:       "result a",
	})
	app.handleToolResult(&agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		ToolCallID: "c2",
		Text:       "result b",
	})

	children := app.subs.chat.Children()
	if len(children) != 2 {
		t.Fatalf("expected 2 tool children, got %d", len(children))
	}
	tc1, ok := children[0].(*tui.ToolExecutionComponent)
	if !ok {
		t.Fatalf("expected first child to be ToolExecutionComponent, got %T", children[0])
	}
	tc2, ok := children[1].(*tui.ToolExecutionComponent)
	if !ok {
		t.Fatalf("expected second child to be ToolExecutionComponent, got %T", children[1])
	}
	if tc1.Status() != tui.ToolSuccess {
		t.Errorf("expected tc1 status ToolSuccess, got %v", tc1.Status())
	}
	if tc2.Status() != tui.ToolSuccess {
		t.Errorf("expected tc2 status ToolSuccess, got %v", tc2.Status())
	}
}

func TestHandleToolResult_MultipleToolsWithoutIDs(t *testing.T) {
	app := New(testSubsystems())
	app.subs.statusMsg = tui.NewStatusMsg()
	app.subs.footer = tui.NewFooter()

	app.handleToolCall(&agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		ToolName:  "bash",
		ToolInput: `{"command":"echo a"}`,
	})
	app.handleToolCall(&agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		ToolName:  "bash",
		ToolInput: `{"command":"echo b"}`,
	})

	app.handleToolResult(&agentic.OutputEvent{
		Type: agentic.EventToolResult,
		Text: "result a",
	})
	app.handleToolResult(&agentic.OutputEvent{
		Type: agentic.EventToolResult,
		Text: "result b",
	})

	children := app.subs.chat.Children()
	if len(children) != 2 {
		t.Fatalf("expected 2 tool children, got %d", len(children))
	}
	tc1, ok := children[0].(*tui.ToolExecutionComponent)
	if !ok {
		t.Fatalf("expected first child to be ToolExecutionComponent, got %T", children[0])
	}
	tc2, ok := children[1].(*tui.ToolExecutionComponent)
	if !ok {
		t.Fatalf("expected second child to be ToolExecutionComponent, got %T", children[1])
	}
	if tc1.Status() != tui.ToolSuccess {
		t.Errorf("expected tc1 status ToolSuccess, got %v", tc1.Status())
	}
	if tc2.Status() != tui.ToolSuccess {
		t.Errorf("expected tc2 status ToolSuccess, got %v", tc2.Status())
	}
}

func TestHandleSessionEnd_Cancelled_RemovesPartialAssistant(t *testing.T) {
	app := New(testSubsystems())
	app.subs.chat.AddUserMessage("user question")
	app.handleStreamContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Role:  agentic.Assistant,
		Text:  "partial answer",
	})

	app.handleSessionEnd(&agentic.OutputEvent{
		Type:     agentic.EventEnd,
		Metadata: map[string]string{"cancelled": "true"},
	})

	if containsRendered(app.subs.chat, "partial answer") {
		t.Errorf("expected partial assistant message to be removed after cancellation")
	}
	if !containsRendered(app.subs.chat, "Generation stopped by user.") {
		t.Errorf("expected 'Generation stopped by user.' system message")
	}
	children := app.subs.chat.Children()
	if len(children) != 2 {
		t.Errorf("expected 2 chat children (user msg + system msg), got %d", len(children))
	}
}

func TestHandleSessionEnd_Cancelled_WithoutActiveStream_KeepsUserMessage(t *testing.T) {
	app := New(testSubsystems())
	app.subs.chat.AddUserMessage("user question")

	app.handleSessionEnd(&agentic.OutputEvent{
		Type:     agentic.EventEnd,
		Metadata: map[string]string{"cancelled": "true"},
	})

	if !containsRendered(app.subs.chat, "user question") {
		t.Errorf("expected user message to remain after cancellation")
	}
	if !containsRendered(app.subs.chat, "Generation stopped by user.") {
		t.Errorf("expected 'Generation stopped by user.' system message")
	}
}

func TestHandleSessionEnd_ConnectionError_ShowsHint(t *testing.T) {
	app := New(testSubsystems())
	app.subs.chat.AddUserMessage("user question")

	app.handleSessionEnd(&agentic.OutputEvent{
		Type: agentic.EventEnd,
		Text: "connection reset by peer",
	})

	if !containsRendered(app.subs.chat, "[connection error]") {
		t.Errorf("expected connection error hint, got: %s", strings.Join(app.subs.chat.Render(80), "\n"))
	}
}

func TestHandleToolResult_EmptyResultClearsBusy(t *testing.T) {
	app := New(testSubsystems())
	app.subs.statusMsg = tui.NewStatusMsg()
	app.subs.footer = tui.NewFooter()
	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read", ToolInput: `{"path":"empty.txt"}`})
	tc := lastTool(app.subs.chat)
	app.subs.footer.SetModelBusy(true)

	app.handleToolResult(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "read", Text: ""})

	if lastTool(app.subs.chat) == nil {
		t.Error("expected tool widget to remain in chat after empty result")
	}
	if app.subs.footer.Data().ModelBusy {
		t.Error("expected model busy cleared after empty tool result")
	}
	if tc.Status() != tui.ToolSuccess {
		t.Errorf("expected ToolSuccess for empty result, got %v", tc.Status())
	}
}

func TestBuildAgentLogger_CreatesFileAndLogs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "logs", "goa.log")
	cfg := &config.Config{Logging: config.LoggingConfig{File: logPath, Level: "info"}}

	logger := buildAgentLogger(cfg, dir)
	if logger == nil {
		t.Fatal("expected logger, got nil")
	}
	logger.Log(agentic.Debug, "test debug line")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Goa agent log started") {
		t.Errorf("expected startup message in log, got: %s", content)
	}
	if !strings.Contains(content, "test debug line") {
		t.Errorf("expected debug line in log, got: %s", content)
	}
}

func TestBuildAgentLogger_EmptyFileReturnsNil(t *testing.T) {
	cfg := &config.Config{Logging: config.LoggingConfig{File: ""}}
	if logger := buildAgentLogger(cfg, t.TempDir()); logger != nil {
		t.Errorf("expected nil logger for empty file, got %v", logger)
	}
}

func TestFormatContextUsage(t *testing.T) {
	cases := []struct {
		name     string
		estimate int
		max      int
		wantSub  string
	}{
		{"low", 30, 100, "30.0%/100"},
		{"warning", 75, 100, "75.0%/100"},
		{"critical", 95, 100, "95.0%/100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatContextUsage(tc.estimate, tc.max)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("formatContextUsage(%d,%d) = %q, want substring %q", tc.estimate, tc.max, got, tc.wantSub)
			}
		})
	}
}

func TestFormatFooterStats(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1500,
		PredictedN:      800,
		ContextEstimate: 2500,
		ContextMax:      10000,
	})
	if !strings.Contains(stats, "↑1.5K") {
		t.Errorf("expected prompt token indicator, got %q", stats)
	}
	if !strings.Contains(stats, "↓800") {
		t.Errorf("expected predicted token indicator, got %q", stats)
	}
	if !strings.Contains(stats, "25.0%/10.0K") {
		t.Errorf("expected context usage, got %q", stats)
	}
}

// TestFormatFooterStats_ShowsProjectedContext is P20/CX8 acceptance criterion
// 2: the footer's occupancy display reads the projected figure (the
// provider-anchored next-request projection), not the stale estimate. The
// estimate and the projection are deliberately different here: the footer must
// show the projection's percentage.
func TestFormatFooterStats_ShowsProjectedContext(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:          1500,
		PredictedN:       800,
		ContextEstimate:  2500, // stale estimate: 25%
		ContextProjected: 8000, // projection: 80%
		ContextMax:       10000,
	})
	if !strings.Contains(stats, "80.0%/10.0K") {
		t.Errorf("expected projected context usage (80.0%%/10.0K), got %q", stats)
	}
	if strings.Contains(stats, "25.0%") {
		t.Errorf("footer shows the stale estimate (25.0%%) instead of the projection, got %q", stats)
	}
}

// TestFormatFooterStats_ProjectedFallsBackToEstimate verifies the footer
// falls back to the estimate when no projection has been recorded (they are
// equal then anyway).
func TestFormatFooterStats_ProjectedFallsBackToEstimate(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		ContextEstimate:  2500,
		ContextProjected: 0,
		ContextMax:       10000,
	})
	if !strings.Contains(stats, "25.0%/10.0K") {
		t.Errorf("expected fallback context usage (25.0%%/10.0K), got %q", stats)
	}
}

func TestFormatFooterStats_ToolCalls(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1500,
		PredictedN:      800,
		ContextEstimate: 2500,
		ContextMax:      10000,
		ToolCalls:       7,
	})
	if !strings.Contains(stats, "TC:7") {
		t.Errorf("expected tool call indicator, got %q", stats)
	}
}

func TestFormatFooterStats_NoToolCalls_OmitsTC(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1500,
		PredictedN:      800,
		ContextEstimate: 2500,
		ContextMax:      10000,
		ToolCalls:       0,
	})
	if strings.Contains(stats, "TC:") {
		t.Errorf("expected no tool call indicator for zero calls, got %q", stats)
	}
}

func TestFormatFooterStats_CacheHitPercentage(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1000,
		PredictedN:      500,
		CacheReadTotal:  300,
		CacheWriteTotal: 200,
		LastCacheHit:    CacheHitTrend{Pct: 60, Seen: true, window: []float64{60}},
		ContextEstimate: 2000,
		ContextMax:      10000,
	})
	// 300 / (300+200) = 60% (cache hit = reads / (reads + writes)).
	// Format: CH:<avg>%▸<last>% — single observation, avg=last=60.
	if !strings.Contains(stats, "CH:60.0%") {
		t.Errorf("expected CH avg 60%%, got %q", stats)
	}
	if !strings.Contains(stats, "▸60.0%") {
		t.Errorf("expected last cache hit 60%%, got %q", stats)
	}
	// Cache hit is shown even when PromptN is 0, as long as cache ops exist.
	noPrompt := formatFooterStats(sessionStats{
		PromptN:         0,
		CacheReadTotal:  300,
		CacheWriteTotal: 200,
		LastCacheHit:    CacheHitTrend{Pct: 60, Seen: true, window: []float64{60}},
	})
	if !strings.Contains(noPrompt, "CH:60.0%") {
		t.Errorf("expected CH avg 60%% when PromptN is 0, got %q", noPrompt)
	}
	if !strings.Contains(noPrompt, "▸60.0%") {
		t.Errorf("expected last cache hit 60%% when PromptN is 0, got %q", noPrompt)
	}
	// No cache ops at all should not show a cache-hit rate.
	noCache := formatFooterStats(sessionStats{
		PromptN:    1000,
		PredictedN: 500,
	})
	if strings.Contains(noCache, "▸") {
		t.Errorf("expected no cache hit display when no cache ops, got %q", noCache)
	}
}

// TestFormatFooterStats_LastCacheHit locks the status-bar cache-hit contract:
// the format is CH:<avg>%▸<last>% where avg is the rolling average of the
// last 10 observations and last is the most recent per-completion rate.
func TestFormatFooterStats_LastCacheHit(t *testing.T) {
	withLast := formatFooterStats(sessionStats{
		LastCacheHit: CacheHitTrend{Pct: 41.9, Seen: true, window: []float64{41.9}},
	})
	if !strings.Contains(withLast, "CH:41.9%") {
		t.Errorf("expected CH avg, got %q", withLast)
	}
	if !strings.Contains(withLast, "▸41.9%") {
		t.Errorf("expected per-completion rate, got %q", withLast)
	}
	// No per-completion observation → no CH/▸ part.
	noLast := formatFooterStats(sessionStats{
		CacheReadTotal:  900,
		CacheWriteTotal: 100,
	})
	if strings.Contains(noLast, "▸") {
		t.Errorf("expected no per-completion rate without observation, got %q", noLast)
	}
	if strings.Contains(noLast, "CH:") {
		t.Errorf("expected no CH without observation, got %q", noLast)
	}
}

// TestFormatCacheHitPart_Colors locks the CH evolution coloring:
// bold green growing / green stable or minor change (<5pts drop) / red
// significant drop (>=5pts); first observation (no baseline) is green.
// The prefix checked is the AVG color (first element in the output).
func TestFormatCacheHitPart_Colors(t *testing.T) {
	const (
		green = "\x1b[38;2;63;185;80m" // ansi.Fg("#3fb950")
		red   = "\x1b[38;2;248;81;73m" // ansi.Fg("#f85149")
	)
	cases := []struct {
		name string
		tr   CacheHitTrend
		want string // SGR prefix (avg color, first element)
	}{
		{"first observation is stable green", CacheHitTrend{Pct: 50, Seen: true, window: []float64{50}}, green},
		{"growing is bold green", CacheHitTrend{Pct: 52, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 52}}, ansi.Bold + green},
		{"stable is green", CacheHitTrend{Pct: 50, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 50}}, green},
		{"slight grow is green", CacheHitTrend{Pct: 50.5, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 50.5}}, green},
		{"minor drop <5pts stays green", CacheHitTrend{Pct: 47, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 47}}, green},
		{"drop of exactly 5pts: avg green, last red", CacheHitTrend{Pct: 45, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 45}}, green}, // avg delta -2.5 → green, last delta -5 → red
		{"drop >5pts is red", CacheHitTrend{Pct: 10, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 10}}, red},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLastCacheHitPart(tc.tr)
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("formatLastCacheHitPart(%+v) = %q, want prefix %q", tc.tr, got, tc.want)
			}
		})
	}
}

// TestFormatCacheHitPart_PerElementColors verifies that avg and last are
// colored independently: the avg reflects its own trend, and the last
// reflects its own trend, each with the >=5pt threshold.
func TestFormatCacheHitPart_PerElementColors(t *testing.T) {
	const (
		green = "\x1b[38;2;63;185;80m" // ansi.Fg("#3fb950")
		red   = "\x1b[38;2;248;81;73m" // ansi.Fg("#f85149")
		reset = "\x1b[0m"
	)
	cases := []struct {
		name     string
		tr       CacheHitTrend
		wantAvg  string // expected SGR for the CH:<avg>% element
		wantLast string // expected SGR for the ▸<last>% element
	}{
		{
			name:     "avg minor drop, last significant drop",
			tr:       CacheHitTrend{Pct: 40, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 50, 40}},
			wantAvg:  green, // avg: (50+50+40)/3 = 46.67, prevAvg: 50, delta = -3.3 (< 5)
			wantLast: red,   // last: 40 vs prev 50, delta = -10 (>= 5)
		},
		{
			name:     "avg significant drop, last stable",
			tr:       CacheHitTrend{Pct: 50, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{80, 80, 50}},
			wantAvg:  red,   // avg: (80+80+50)/3 = 70, prevAvg: 80, delta = -10 (>= 5)
			wantLast: green, // last: 50 vs prev 50, delta = 0
		},
		{
			name:     "both stable",
			tr:       CacheHitTrend{Pct: 75, PrevPct: 75, Seen: true, HasPrev: true, window: []float64{75, 75}},
			wantAvg:  green,
			wantLast: green,
		},
		{
			name:     "both significant drop",
			tr:       CacheHitTrend{Pct: 10, PrevPct: 50, Seen: true, HasPrev: true, window: []float64{50, 50, 10}},
			wantAvg:  red, // avg: (50+50+10)/3 = 36.67, prevAvg: 50, delta = -13.3 (>= 5)
			wantLast: red, // last: 10 vs prev 50, delta = -40 (>= 5)
		},
		{
			name:     "minor fluctuation stays green",
			tr:       CacheHitTrend{Pct: 73, PrevPct: 75, Seen: true, HasPrev: true, window: []float64{75, 74, 73}},
			wantAvg:  green, // avg: 74, prevAvg: 74.5, delta = -0.5 (< 5)
			wantLast: green, // last: 73 vs 75, delta = -2 (< 5)
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLastCacheHitPart(tc.tr)
			// Format: <avgColor>CH:<avg>%<reset><lastColor>▸<last>%<reset>
			// Find the reset between avg and last to split them.
			idx := strings.Index(got, reset)
			if idx < 0 {
				t.Fatalf("formatLastCacheHitPart missing reset: %q", got)
			}
			avgPart := got[:idx+len(reset)]
			lastPart := got[idx+len(reset):]
			if !strings.HasPrefix(avgPart, tc.wantAvg) {
				t.Errorf("avg part = %q, want prefix %q", avgPart, tc.wantAvg)
			}
			if !strings.HasPrefix(lastPart, tc.wantLast) {
				t.Errorf("last part = %q, want prefix %q", lastPart, tc.wantLast)
			}
		})
	}
}

func TestLogTurnStats_UsesPerTurnCounts(t *testing.T) {
	app := New(testSubsystems())
	app.lastTurnPromptN = 100
	app.lastTurnPredictedN = 50
	app.lastTurnSpeed = 12.5
	app.tokenSessionMax = 10000
	app.tokenSessionEstimate = 150
	app.turnCount = 1
	app.turnStatsSeen = true // simulate a turn that emitted token stats

	logger := agentic.NewLogger(agentic.Info)
	logPath := filepath.Join(t.TempDir(), "stats.log")
	file, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	file.Close()

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	logger.SetOutput(logFile)
	app.subs.logger = logger

	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})
	logFile.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	want := "[stats] turn 1: in=100 out=50 speed=12.5 ctx=1.5%/10000"
	if !strings.Contains(content, want) {
		t.Errorf("log line mismatch\nwant substring: %q\ngot: %q", want, content)
	}
}

func TestHandleOrchestratorStreamMsg_CompanionSection(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())

	var section *tui.CompanionSectionComponent
	var cycle int
	var thinkingBuf strings.Builder
	var messageBuf strings.Builder

	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_start"}, &section, &cycle, &thinkingBuf, &messageBuf)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_chunk", Content: "reasoning..."}, &section, &cycle, &thinkingBuf, &messageBuf)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_end"}, &section, &cycle, &thinkingBuf, &messageBuf)

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	if !strings.Contains(rendered, "reasoning...") {
		t.Errorf("expected thinking text while expanded, got:\n%s", rendered)
	}

	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_start"}, &section, &cycle, &thinkingBuf, &messageBuf)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_chunk", Content: "review"}, &section, &cycle, &thinkingBuf, &messageBuf)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_end"}, &section, &cycle, &thinkingBuf, &messageBuf)

	if section != nil {
		t.Error("expected section to be cleared after stream_end")
	}

	rendered = strings.Join(app.subs.chat.Render(80), "\n")
	companionCount := strings.Count(rendered, "companion ·")
	if companionCount != 1 {
		t.Errorf("expected exactly one companion section, got %d in:\n%s", companionCount, rendered)
	}
}

func TestHandleOrchestratorStreamMsg_TwoCyclesTwoSections(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())

	var section *tui.CompanionSectionComponent
	var cycle int
	var thinkingBuf strings.Builder
	var messageBuf strings.Builder

	runCycle := func(n int) {
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_start"}, &section, &cycle, &thinkingBuf, &messageBuf)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_chunk", Content: fmt.Sprintf("think%d", n)}, &section, &cycle, &thinkingBuf, &messageBuf)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_end"}, &section, &cycle, &thinkingBuf, &messageBuf)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_start"}, &section, &cycle, &thinkingBuf, &messageBuf)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_chunk", Content: fmt.Sprintf("msg%d", n)}, &section, &cycle, &thinkingBuf, &messageBuf)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_end"}, &section, &cycle, &thinkingBuf, &messageBuf)
	}

	runCycle(1)
	runCycle(2)

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	companionCount := strings.Count(rendered, "companion ·")
	if companionCount != 2 {
		t.Errorf("expected two companion sections, got %d in:\n%s", companionCount, rendered)
	}
}

func TestToolStatusFromResult(t *testing.T) {
	cases := []struct {
		name string
		text string
		want tui.ToolStatus
	}{
		{"error prefix", "Error: oops", tui.ToolError},
		{"error with whitespace", "  Error: oops", tui.ToolError},
		{"budget exceeded", agentic.ToolBudgetResultPrefix, tui.ToolError},
		{"success", "ok", tui.ToolSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := New(testSubsystems())
			got := app.toolStatusFromResult(tc.text)
			if got != tc.want {
				t.Errorf("toolStatusFromResult(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestTelegramSkillEmbedded(t *testing.T) {
	reg := skills.NewSkillRegistry(nil)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	skill, ok := reg.Get("telegram")
	if !ok {
		t.Fatal("telegram skill not found in embedded skills")
	}
	if skill.Meta.Command != "telegram" {
		t.Errorf("telegram skill missing 'command: telegram' frontmatter, got %q", skill.Meta.Command)
	}
	if !skill.Meta.Inline {
		t.Errorf("telegram skill should be inline")
	}
}

func TestSetupEventHandlers_ClosesDoneWhenEngineStops(t *testing.T) {
	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Stop engine at end so the goroutines exit cleanly.
	defer engine.Stop()

	subs := testSubsystems()
	app := New(subs)

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()

	done := app.setupEventHandlers(engine, chat, inp)

	// Engine is running — done must NOT be closed yet.
	select {
	case <-done:
		t.Fatal("done channel closed before engine.Stop()")
	default:
	}

	// Stop the engine (simulates Ctrl+C).
	engine.Stop()

	// done must be closed promptly after engine stops.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("done channel not closed within 1s after engine.Stop()")
	}
}

func TestSetupEventHandlers_DoneNotClosedBeforeEngineStop(t *testing.T) {
	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	subs := testSubsystems()
	app := New(subs)

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()

	done := app.setupEventHandlers(engine, chat, inp)

	// The goroutine must block until engine.Stop() — done must NOT be closed.
	select {
	case <-done:
		t.Fatal("done channel closed before engine.Stop()")
	default:
	}
}

// TestLogTurnStats_NoStatsTurnAnnotated is the regression test for the
// identical-stats anomaly (runaway-loop entry): turns that never
// reached the LLM (guardrail latch, connection error) must log a distinct
// "no LLM call" line instead of re-logging the previous turn's stale,
// byte-identical token counts.
func TestLogTurnStats_NoStatsTurnAnnotated(t *testing.T) {
	app := New(testSubsystems())
	app.turnCount = 7
	// turnStatsSeen deliberately false: no EventTokenStats arrived this turn.

	logger := agentic.NewLogger(agentic.Info)
	logPath := filepath.Join(t.TempDir(), "stats.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	logger.SetOutput(logFile)
	app.subs.logger = logger

	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})
	logFile.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	want := "[stats] turn 7: no LLM call (no token stats this turn)"
	if !strings.Contains(content, want) {
		t.Errorf("log line mismatch\nwant substring: %q\ngot: %q", want, content)
	}
}

// TestLogTurnStats_StatsSeenResetsAfterTurnEnd verifies the per-turn stats
// flag lifecycle: a turn WITH token stats logs the normal line and the flag
// resets, so the following stats-less turn is annotated instead of re-logging
// identical numbers.
func TestLogTurnStats_StatsSeenResetsAfterTurnEnd(t *testing.T) {
	app := New(testSubsystems())
	app.tokenSessionMax = 10000
	app.turnCount = 1

	logger := agentic.NewLogger(agentic.Info)
	logPath := filepath.Join(t.TempDir(), "stats.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	logger.SetOutput(logFile)
	app.subs.logger = logger
	defer logFile.Close()

	// Turn 1: stats arrive, then the turn ends.
	app.handleTokenStats(&agentic.OutputEvent{
		Type:    agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{PromptN: 100, PredictedN: 50},
	})
	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Turn 2: no stats arrive (e.g. latched guardrail turn).
	app.turnCount = 2
	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})
	logFile.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[stats] turn 1: in=100 out=50") {
		t.Errorf("turn 1 line missing normal stats: %q", content)
	}
	if !strings.Contains(content, "[stats] turn 2: no LLM call") {
		t.Errorf("turn 2 line should be annotated as stats-less, got: %q", content)
	}
	if strings.Count(content, "in=100 out=50") > 1 {
		t.Errorf("stale stats re-logged for the stats-less turn: %q", content)
	}
}
