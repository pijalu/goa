// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

type imageResultTool struct {
	path string
}

func (imageResultTool) Schema() ToolSchema {
	return ToolSchema{Name: "image_result", Description: "return an image", Schema: map[string]any{"type": "object"}}
}
func (imageResultTool) Execute(string) (string, error) { return "image metadata", nil }
func (imageResultTool) IsRetryable(error) bool         { return false }
func (t imageResultTool) ExecuteWithResult(string) (ToolResult, error) {
	return ToolResult{Output: "image metadata", Images: []string{t.path}}, nil
}

type agentToolImageCase struct {
	name        string
	inputTypes  []string
	wantImages  int
	wantOmitted bool
}

func TestAgentToolImageAttachmentHonorsActiveModelCapability(t *testing.T) {
	tests := []agentToolImageCase{
		{name: "vision model receives attachment", inputTypes: []string{"text", "image"}, wantImages: 1},
		{name: "text-only model receives explicit omission", inputTypes: []string{"text"}, wantOmitted: true},
		{name: "unknown capability is text-only", wantOmitted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runAgentToolImageCase(t, tt) })
	}
}

func runAgentToolImageCase(t *testing.T, tt agentToolImageCase) {
	t.Helper()
	const imagePath = "/tmp/read-image.png"
	p := registerRecordingProvider("tool-image", []provider.AssistantMessageEvent{
		toolCallEvent("call_image", "image_result", `{}`),
	})
	model := testModel(p.API())
	model.InputTypes = tt.inputTypes
	var events []OutputEvent
	agent := NewAgent(Config{
		Model:  model,
		Tools:  []Tool{imageResultTool{path: imagePath}},
		Logger: NewLogger(Error),
	})
	agent.AddObserver(&testObserver{events: &events})
	if _, err := agent.RunAndCollect(context.Background(), "inspect the image"); err != nil {
		t.Fatalf("RunAndCollect: %v", err)
	}
	if len(p.ctxs) != 2 {
		t.Fatalf("provider rounds = %d, want 2", len(p.ctxs))
	}
	assertProviderToolImage(t, tt, lastToolResultMessage(t, p.ctxs[1].Messages), imagePath)
	historyMessage := lastAgentToolResult(t, agent.GetHistory())
	if len(historyMessage.Images) != tt.wantImages {
		t.Errorf("history Images = %q, want %d attachments", historyMessage.Images, tt.wantImages)
	}
	assertObservedToolImage(t, events, tt.wantImages)
}

func assertProviderToolImage(t *testing.T, tt agentToolImageCase, message provider.Message, imagePath string) {
	t.Helper()
	images := imageBlocks(message.Content)
	if len(images) != tt.wantImages {
		t.Fatalf("provider image blocks = %d, want %d; content = %#v", len(images), tt.wantImages, message.Content)
	}
	if tt.wantImages == 1 && images[0].ImageData != imagePath {
		t.Errorf("ImageData = %q, want %q", images[0].ImageData, imagePath)
	}
	text := toolResultTextForTest(message.Content)
	if tt.wantOmitted && !strings.Contains(text, "model does not support images") {
		t.Errorf("text-only result %q lacks explicit unsupported-model notice", text)
	}
	if tt.wantOmitted && strings.Contains(text, imagePath) {
		t.Errorf("text-only result leaked image reference: %q", text)
	}
}

func assertObservedToolImage(t *testing.T, events []OutputEvent, wantImages int) {
	t.Helper()
	for _, event := range events {
		if event.Type != EventToolResult || event.ToolCallID != "call_image" {
			continue
		}
		if len(event.Images) != wantImages {
			t.Errorf("observer event Images count = %d, want %d", len(event.Images), wantImages)
		}
		return
	}
	t.Error("missing tool-result observer event")
}

func TestToolSchedulerPreservesImageAttachments(t *testing.T) {
	scheduler := NewToolScheduler(context.Background())
	defer scheduler.Shutdown()
	scheduler.Add(&ToolCallTask{
		Name:   "image_result",
		CallID: "call_image",
		Execute: func(context.Context) (ToolResult, error) {
			return ToolResult{Output: "metadata", Images: []string{"/tmp/one.png"}}, nil
		},
	})
	results := scheduler.Collect()
	if len(results) != 1 || len(results[0].Images) != 1 || results[0].Images[0] != "/tmp/one.png" {
		t.Fatalf("scheduler dropped image attachment: %#v", results)
	}
}

func lastToolResultMessage(t *testing.T, messages []provider.Message) provider.Message {
	t.Helper()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == provider.RoleToolResult {
			return messages[i]
		}
	}
	t.Fatal("provider context has no tool result")
	return provider.Message{}
}

func imageBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	var images []provider.ContentBlock
	for _, block := range blocks {
		if block.Type == provider.ContentBlockImage {
			images = append(images, block)
		}
	}
	return images
}

func toolResultTextForTest(blocks []provider.ContentBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == provider.ContentBlockToolResult || block.Type == provider.ContentBlockText {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

func lastAgentToolResult(t *testing.T, history []Message) Message {
	t.Helper()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == ToolRole {
			return history[i]
		}
	}
	t.Fatal("agent history has no tool result")
	return Message{}
}
