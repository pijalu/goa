// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
	goaltui "github.com/pijalu/goa/tui/goal"
)

func TestShowSendingStatus_SetsSendingLabel(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.showSendingStatus("lm-qwen")

	if app.subs.statusMsg.Text() != "Sending request..." {
		t.Errorf("status text = %q, want %q", app.subs.statusMsg.Text(), "Sending request...")
	}
	if app.subs.footer.Data().MainActivity != "Sending request..." {
		t.Errorf("footer MainActivity = %q, want %q", app.subs.footer.Data().MainActivity, "Sending request...")
	}
}

func TestSetWaitingForReplyStatus_ShowsSending(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.setWaitingForReplyStatus(&agentic.PromptProgress{})

	want := "Sending request..."
	if app.subs.statusMsg.Text() != want {
		t.Errorf("status text = %q, want %q", app.subs.statusMsg.Text(), want)
	}
}

func TestSetWaitingForReplyStatus_ShowsProgress(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.setWaitingForReplyStatus(&agentic.PromptProgress{Total: 100, Processed: 42})

	want := "Processing... 42%"
	if app.subs.statusMsg.Text() != want {
		t.Errorf("status text = %q, want %q", app.subs.statusMsg.Text(), want)
	}
	if app.subs.footer.Data().MainActivity != want {
		t.Errorf("footer MainActivity = %q, want %q", app.subs.footer.Data().MainActivity, want)
	}
}

func TestHandleStateChange_StreamingSetsActivity(t *testing.T) {
	app := New(testSubsystems())
	app.handleStateChange(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})

	if app.subs.footer.Data().Activity != "streaming" {
		t.Errorf("Activity = %q, want streaming", app.subs.footer.Data().Activity)
	}
	if app.subs.footer.Data().MainActivity != "streaming" {
		t.Errorf("MainActivity = %q, want streaming", app.subs.footer.Data().MainActivity)
	}
	if app.subs.statusMsg.Text() != "Answering..." {
		t.Errorf("status text = %q, want Answering...", app.subs.statusMsg.Text())
	}
}

func TestHandleStateChange_ThinkingSetsActivity(t *testing.T) {
	app := New(testSubsystems())
	app.handleStateChange(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	if app.subs.footer.Data().Activity != "thinking" {
		t.Errorf("Activity = %q, want thinking", app.subs.footer.Data().Activity)
	}
	if app.subs.statusMsg.Text() != "Thinking..." {
		t.Errorf("status text = %q, want Thinking...", app.subs.statusMsg.Text())
	}
}

func TestClearToolBusy_ShowsSendingRequest(t *testing.T) {
	app := New(testSubsystems())
	app.subs.statusMsg = tui.NewStatusMsg()
	app.subs.footer = tui.NewFooter()
	app.subs.footer.SetModelBusy(true)

	app.clearToolBusy()

	if app.subs.statusMsg.Text() != "Sending request..." {
		t.Errorf("status text = %q, want %q", app.subs.statusMsg.Text(), "Sending request...")
	}
	if app.subs.footer.Data().ModelBusy {
		t.Error("expected model busy cleared after clearToolBusy")
	}
}

func TestToolCallProgressLabel_NoAgent(t *testing.T) {
	app := New(testSubsystems())
	if got := app.toolCallProgressLabel(); got != "Tool calling" {
		t.Errorf("toolCallProgressLabel() = %q, want Tool calling", got)
	}
}

func TestHandleThinkingContent_SetsStatus(t *testing.T) {
	app := New(testSubsystems())
	app.subs.cfg = &config.Config{TUI: config.TUIConfig{Transparency: config.TransparencyConfig{ShowThinking: true}}}
	app.handleThinkingContent(&agentic.OutputEvent{
		Type:  agentic.EventContent,
		Role:  agentic.Assistant,
		State: agentic.StateThinking,
		Text:  "step one",
	})

	if app.subs.statusMsg.Text() != "Thinking..." {
		t.Errorf("status text = %q, want %q", app.subs.statusMsg.Text(), "Thinking...")
	}
}

func TestToolCallProgressLabel_WithAgentBatch(t *testing.T) {
	app := New(testSubsystems())
	cfg := agentic.Config{
		Model: provider.Model{ID: "test", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderLMStudio},
		Tools: []agentic.Tool{},
	}
	agent := agentic.NewAgent(cfg)
	app.subs.agentMgr = core.NewAgentManager(&config.Config{}, nil, nil, nil, nil, "")
	app.subs.agentMgr.SetActiveAgentForTest(agent)

	agent.SetBufferedToolCallCountForTest(3)
	// No results have completed yet, so the first call is shown as 1/3.
	want := "Tool calling (1/3)"
	if got := app.toolCallProgressLabel(); got != want {
		t.Errorf("toolCallProgressLabel() = %q, want %q", got, want)
	}

	// Simulate one completed result.
	app.toolResultsSeen = 1
	want = "Tool calling (2/3)"
	if got := app.toolCallProgressLabel(); got != want {
		t.Errorf("toolCallProgressLabel() = %q, want %q", got, want)
	}
}

func TestHandleToolCall_StatusBarSpinnerVisible(t *testing.T) {
	// Ensure the active spinner has animated frames.
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	// Build the full production component tree.
	header := tui.NewHeader("goa", "test")
	chat := tui.NewChatViewport()
	pending := tui.NewStatusMsg()
	statusBar := tui.NewStatusMsg()
	goal := goaltui.NewBubble()
	inp := tui.NewEditor()
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pending)
	engine.AddChild(statusBar)
	engine.AddChild(goal)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)

	pending.SetTUI(engine)
	statusBar.SetTUI(engine)
	inp.SetTUI(engine)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = statusBar
	subs.footer = footer
	subs.goalBubble = goal
	app := New(subs)

	// Simulate a thinking phase so the spinner is already active, then a tool call.
	app.handleStateChange(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	engine.RenderNow()

	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`})
	engine.RenderNow()

	frame := engine.AgentFrame()
	visible := strings.Join(frame.Visible, "\n")

	if !strings.Contains(visible, "Tool calling") {
		t.Errorf("expected status bar to show 'Tool calling', visible:\n%s", visible)
	}

	hasFrame := false
	for _, f := range def.Frames {
		if strings.Contains(visible, f) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Errorf("expected status bar to contain an animated spinner frame, visible:\n%s", visible)
	}
}

func TestHandleToolCall_FooterBusyIndicator(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	header := tui.NewHeader("goa", "test")
	chat := tui.NewChatViewport()
	pending := tui.NewStatusMsg()
	statusBar := tui.NewStatusMsg()
	goal := goaltui.NewBubble()
	inp := tui.NewEditor()
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pending)
	engine.AddChild(statusBar)
	engine.AddChild(goal)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)

	pending.SetTUI(engine)
	statusBar.SetTUI(engine)
	inp.SetTUI(engine)

	subs := testSubsystems()
	subs.cfg.ActiveModel = "test-model"
	subs.cfg.ActiveProvider = "test-provider"
	subs.cfg.Models = []config.ModelConfig{{ID: "test-model", Model: "test-model", ProviderID: "test-provider"}}
	subs.cfg.Providers = []config.ProviderConfig{{ID: "test-provider"}}
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = statusBar
	subs.footer = footer
	subs.goalBubble = goal
	app := New(subs)

	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`})
	engine.RenderNow()

	if !app.subs.footer.Data().ModelBusy {
		t.Error("expected footer ModelBusy to be true during a tool call")
	}

	rendered := strings.Join(app.subs.footer.Render(80), "\n")
	hasFrame := false
	for _, f := range def.Frames {
		if strings.Contains(rendered, f) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Errorf("expected footer render to contain animated spinner frame, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "tool calling") {
		t.Errorf("expected footer render to contain 'tool calling' activity, got:\n%s", rendered)
	}
}

func TestHandleToolCall_ToolWidgetShowsRunningSpinner(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	app := New(testSubsystems())
	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`})

	tc := app.subs.activeTool
	if tc == nil {
		t.Fatal("expected activeTool to be set after handleToolCall")
	}
	if tc.Status() != tui.ToolRunning {
		t.Errorf("expected tool widget status ToolRunning, got %v", tc.Status())
	}

	rendered := strings.Join(tc.Render(80), "\n")
	hasFrame := false
	for _, f := range def.Frames {
		if strings.Contains(rendered, f) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Errorf("expected tool widget to show running spinner frame, got:\n%s", rendered)
	}
}

func TestHandleToolCall_StatusBarVisible_WithTallChat(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	header := tui.NewHeader("goa", "test")
	chat := tui.NewChatViewport()
	pending := tui.NewStatusMsg()
	statusBar := tui.NewStatusMsg()
	goal := goaltui.NewBubble()
	inp := tui.NewEditor()
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pending)
	engine.AddChild(statusBar)
	engine.AddChild(goal)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)

	pending.SetTUI(engine)
	statusBar.SetTUI(engine)
	inp.SetTUI(engine)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = statusBar
	subs.footer = footer
	subs.goalBubble = goal
	app := New(subs)

	// Fill the chat with many messages so the viewport scrolls.
	for i := 0; i < 50; i++ {
		chat.AddAssistantMessage(fmt.Sprintf("line %d: %s", i, strings.Repeat("x ", 40)))
	}
	engine.RenderNow()

	// Start a tool call.
	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`})
	engine.RenderNow()

	frame := engine.AgentFrame()
	visible := strings.Join(frame.Visible, "\n")

	if !strings.Contains(visible, "Tool calling") {
		t.Errorf("expected status bar to remain visible in scrolled viewport, visible:\n%s", visible)
	}

	hasFrame := false
	for _, f := range def.Frames {
		if strings.Contains(visible, f) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Errorf("expected spinner frame to remain visible in scrolled viewport, visible:\n%s", visible)
	}
}

func TestHandleToolCall_StatusBarSpinnerVisible_AfterAnswering(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	header := tui.NewHeader("goa", "test")
	chat := tui.NewChatViewport()
	pending := tui.NewStatusMsg()
	statusBar := tui.NewStatusMsg()
	goal := goaltui.NewBubble()
	inp := tui.NewEditor()
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pending)
	engine.AddChild(statusBar)
	engine.AddChild(goal)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)

	pending.SetTUI(engine)
	statusBar.SetTUI(engine)
	inp.SetTUI(engine)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = statusBar
	subs.footer = footer
	subs.goalBubble = goal
	app := New(subs)

	// Simulate content streaming so the status bar shows "Answering...".
	app.handleStreamContent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: "Here is the answer..."})
	engine.RenderNow()

	// Now a tool call arrives.
	app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`})
	engine.RenderNow()

	frame := engine.AgentFrame()
	visible := strings.Join(frame.Visible, "\n")

	if !strings.Contains(visible, "Tool calling") {
		t.Errorf("expected status bar to show 'Tool calling' after answering, visible:\n%s", visible)
	}

	hasFrame := false
	for _, f := range def.Frames {
		if strings.Contains(visible, f) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Errorf("expected status bar to contain an animated spinner frame after answering, visible:\n%s", visible)
	}
}

func TestHandleToolCall_ToolWidgetAnimates(t *testing.T) {
	anim := spinner.Definition{
		Interval: 1,
		Frames:   []string{"◜", "◠", "◝", "◞", "◡", "◟"},
	}
	tui.SetSpinner(anim)
	defer tui.SetSpinner(spinner.Definition{})

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	engine.RunLoops()
	defer func() {
		engine.Stop()
		select {
		case <-engine.Stopped():
		case <-time.After(time.Second):
			t.Fatal("engine did not stop")
		}
	}()

	header := tui.NewHeader("goa", "test")
	chat := tui.NewChatViewport()
	pending := tui.NewStatusMsg()
	statusBar := tui.NewStatusMsg()
	goal := goaltui.NewBubble()
	inp := tui.NewEditor()
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pending)
	engine.AddChild(statusBar)
	engine.AddChild(goal)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)

	pending.SetTUI(engine)
	statusBar.SetTUI(engine)
	statusBar.SetOnFrameChange(func() {
		chat.InvalidateRunningToolWidgets()
	})
	inp.SetTUI(engine)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = statusBar
	subs.footer = footer
	subs.goalBubble = goal
	app := New(subs)

	engine.ApplySync(func() {
		app.handleToolCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`})
	})

	// Render once to get the initial frame via the command loop.
	frame1Data := engine.AgentFrame()
	render1 := strings.Join(frame1Data.Visible, "\n")

	var frame1 string
	for _, f := range anim.Frames {
		if strings.Contains(render1, f) {
			frame1 = f
			break
		}
	}
	if frame1 == "" {
		t.Fatalf("expected tool widget to render a spinner frame, got:\n%s", render1)
	}

	// Wait for at least one spinner tick and re-render via the command loop.
	time.Sleep(20 * time.Millisecond)
	frame2Data := engine.AgentFrame()
	render2 := strings.Join(frame2Data.Visible, "\n")

	var frame2 string
	for _, f := range anim.Frames {
		if strings.Contains(render2, f) {
			frame2 = f
			break
		}
	}
	if frame2 == "" {
		t.Fatalf("expected tool widget to render a spinner frame after tick, got:\n%s", render2)
	}
	if frame1 == frame2 {
		t.Fatalf("tool widget spinner did not animate: frame stayed %q\nrender1:\n%s\n\nrender2:\n%s", frame1, render1, render2)
	}
}
