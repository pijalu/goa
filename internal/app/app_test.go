// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/provider"
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
