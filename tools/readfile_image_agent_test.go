// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

var readImageProviderID atomic.Int64

type readImageRecordingProvider struct {
	api      provider.Api
	input    string
	contexts []provider.Context
	round    int
}

func (p *readImageRecordingProvider) API() provider.Api { return p.api }

func (p *readImageRecordingProvider) Stream(_ provider.Model, ctx provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.contexts = append(p.contexts, ctx)
	round := p.round
	p.round++
	stream := provider.NewAssistantMessageEventStream(8)
	go func() {
		if round == 0 {
			stream.Push(provider.AssistantMessageEvent{
				Type: provider.EventToolCallEnd,
				ToolCall: &provider.ContentBlock{
					Type: provider.ContentBlockToolCall, ToolCallID: "call_read_image",
					ToolName: "read", ToolArguments: p.input,
				},
			})
		} else {
			stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Image received."})
		}
		stream.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
	}()
	return stream, nil
}

func (p *readImageRecordingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func TestReadFileIncludeImageReachesNextVisionModelRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(path, encodeReadImage(t, png.Encode), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &readImageRecordingProvider{
		api:   provider.Api(fmt.Sprintf("read-image-%d", readImageProviderID.Add(1))),
		input: readImageInput(t, path, true),
	}
	provider.RegisterApiProvider(mock)
	model := provider.Model{
		ID: "vision-test", Api: mock.API(), Provider: provider.ProviderCustom,
		InputTypes: []string{"text", "image"},
	}
	agent := agentic.NewAgent(agentic.Config{
		Model: model, Tools: []agentic.Tool{&ReadFileTool{}}, Logger: agentic.NewLogger(agentic.Error),
	})
	if _, err := agent.RunAndCollect(context.Background(), "inspect the screenshot"); err != nil {
		t.Fatalf("RunAndCollect: %v", err)
	}
	if len(mock.contexts) != 2 {
		t.Fatalf("provider rounds = %d, want 2", len(mock.contexts))
	}

	assertReadImageToolResult(t, mock.contexts[1].Messages, path)
}

func assertReadImageToolResult(t *testing.T, messages []provider.Message, path string) {
	t.Helper()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.RoleToolResult {
			continue
		}
		assertReadImageToolResultBlocks(t, messages[i].Content, path)
		return
	}
	t.Fatal("next provider request has no tool result")
}

func assertReadImageToolResultBlocks(t *testing.T, blocks []provider.ContentBlock, path string) {
	t.Helper()
	var images, toolResults, genericText int
	for _, block := range blocks {
		switch block.Type {
		case provider.ContentBlockImage:
			images++
			if block.ImageData != path {
				t.Errorf("ImageData = %q, want %q", block.ImageData, path)
			}
		case provider.ContentBlockToolResult:
			toolResults++
		case provider.ContentBlockText:
			genericText++
		}
	}
	if images != 1 || toolResults != 1 || genericText != 0 {
		t.Errorf("tool result blocks: images=%d tool_results=%d generic_text=%d; content=%#v", images, toolResults, genericText, blocks)
	}
}
