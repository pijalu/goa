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
func (t *testTerminal) WriteString(s string)                        { t.writes = append(t.writes, s) }
func (t *testTerminal) Size() (int, int)                            { return t.w, t.h }
func (t *testTerminal) SetRaw() (func(), error)                     { return func() {}, nil }
func (t *testTerminal) HideCursor()                                 {}
func (t *testTerminal) ShowCursor()                                 {}
func (t *testTerminal) ClearScreen()                                {}
func (t *testTerminal) SetTitle(title string)                       {}

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
	tc := app.subs.chat.AddToolExecution("bash", `{"command":"echo hi"}`)
	app.subs.activeTool = tc

	app.handleToolResult(&agentic.OutputEvent{Type: agentic.EventToolResult, Text: "hi\nDuration: 0.01s\n"})

	if tc.Status() != tui.ToolSuccess {
		t.Errorf("expected ToolSuccess, got %v", tc.Status())
	}
}

func TestHandleToolResult_NonBashMarksErrorOnErrorPrefix(t *testing.T) {
	app := New(testSubsystems())
	tc := app.subs.chat.AddToolExecution("read", `{"path":"missing.txt"}`)
	app.subs.activeTool = tc

	app.handleToolResult(&agentic.OutputEvent{Type: agentic.EventToolResult, Text: "Error: file not found\nHint: See /docs TOOLS"})

	if tc.Status() != tui.ToolError {
		t.Errorf("expected ToolError, got %v", tc.Status())
	}
}

func TestHandleToolResult_NonBashMarksSuccess(t *testing.T) {
	app := New(testSubsystems())
	app.subs.statusMsg = tui.NewStatusMsg()
	app.subs.footer = tui.NewFooter()
	tc := app.subs.chat.AddToolExecution("read", `{"path":"ok.txt"}`)
	app.subs.activeTool = tc
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
	tc := app.subs.chat.AddToolExecution("read", `{"path":"empty.txt"}`)
	app.subs.activeTool = tc
	app.subs.footer.SetModelBusy(true)

	app.handleToolResult(&agentic.OutputEvent{Type: agentic.EventToolResult, Text: ""})

	if app.subs.activeTool != nil {
		t.Error("expected activeTool to be cleared after empty result")
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
			got := formatContextUsage(tc.estimate, tc.max, false)
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
		ContextEstimate: 2000,
		ContextMax:      10000,
	})
	// 300 / (300+200) = 60% (cache hit = reads / (reads + writes))
	if !strings.Contains(stats, "CH60.0%") {
		t.Errorf("expected cache hit 60%%, got %q", stats)
	}
	// Cache hit is shown even when PromptN is 0, as long as cache ops exist.
	noPrompt := formatFooterStats(sessionStats{
		PromptN:         0,
		CacheReadTotal:  300,
		CacheWriteTotal: 200,
	})
	if !strings.Contains(noPrompt, "CH60.0%") {
		t.Errorf("expected cache hit 60%% when PromptN is 0, got %q", noPrompt)
	}
	// No cache ops at all should not show CH.
	noCache := formatFooterStats(sessionStats{
		PromptN:    1000,
		PredictedN: 500,
	})
	if strings.Contains(noCache, "CH") {
		t.Errorf("expected no cache hit display when no cache ops, got %q", noCache)
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
