// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/tui"
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
