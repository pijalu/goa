// SPDX-License-Identifier: GPL-3.0-or-later

package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

type messageToolImageCase struct {
	name      string
	model     schema.Model
	wantImage bool
}

func TestMessageHookToolImageUsesActiveModelCapability(t *testing.T) {
	tests := []messageToolImageCase{
		{name: "vision input", model: schema.Model{InputTypes: []string{"text", "image"}}, wantImage: true},
		{name: "explicit vision flag", model: schema.Model{IsVisionModel: true}, wantImage: true},
		{name: "text only", model: schema.Model{InputTypes: []string{"text"}}},
		{name: "unknown capability fails closed", model: schema.Model{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runMessageToolImageCase(t, tt) })
	}
}

func runMessageToolImageCase(t *testing.T, tt messageToolImageCase) {
	t.Helper()
	hook := &MessageHook{}
	if err := hook.Init(schema.VariantProfile{
		Compat: schema.CompatFlags{ImageURLScheme: "data"},
	}); err != nil {
		t.Fatal(err)
	}
	message := schema.NewToolResultMessage("call_read", "read", "metadata", false)
	message.Content = append(message.Content, schema.ContentBlock{
		Type: schema.ContentBlockImage, ImageData: "data:image/png;base64,SECRET_IMAGE_BYTES",
	})
	ctx := &RequestContext{Model: tt.model, Context: schema.Context{Messages: []schema.Message{message}}}
	if err := hook.ApplyRequest(ctx); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(ctx.Context.Messages)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(encoded)
	hasImage := strings.Contains(payload, "SECRET_IMAGE_BYTES")
	if hasImage != tt.wantImage {
		t.Errorf("image presence = %v, want %v; payload = %s", hasImage, tt.wantImage, payload)
	}
	if !tt.wantImage && !strings.Contains(payload, schema.ToolImageOmittedPlaceholder) {
		t.Errorf("text-only transform lacks explicit omission notice: %s", payload)
	}
}

func TestToolHookToolResultAsUserPreservesImageBlocks(t *testing.T) {
	hook := &ToolHook{}
	if err := hook.Init(schema.VariantProfile{
		ToolCompat: schema.ToolCompat{ToolResultAsUser: true},
	}); err != nil {
		t.Fatal(err)
	}
	message := schema.NewToolResultMessage("call_read", "read", "metadata", false)
	message.Content = append(message.Content, schema.ContentBlock{
		Type: schema.ContentBlockImage, ImageData: "/tmp/image.png",
	})
	ctx := &RequestContext{Context: schema.Context{Messages: []schema.Message{message}}}
	if err := hook.ApplyRequest(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := ctx.Context.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("content blocks = %d, want tool text and image", len(blocks))
	}
	if blocks[0].Type != schema.ContentBlockText || !strings.Contains(blocks[0].Text, "<tool_result") {
		t.Errorf("tool result was not converted to user text: %#v", blocks[0])
	}
	if blocks[1].Type != schema.ContentBlockImage || blocks[1].ImageData != "/tmp/image.png" {
		t.Errorf("image attachment was destroyed: %#v", blocks[1])
	}
}
