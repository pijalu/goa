// SPDX-License-Identifier: GPL-3.0-or-later

package protocol

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

type toolImageProtocolCase struct {
	name  string
	model schema.Model
	want  []string
}

func TestToolResultImageEncodingAllProtocols(t *testing.T) {
	blocks, wantBase64 := imageBlock(t)
	imagePath := blocks[1].ImageData
	message := schema.NewToolResultMessage("call_read", "read", "image metadata", false)
	message.Content = append(message.Content, schema.ContentBlock{
		Type: schema.ContentBlockImage, ImageData: imagePath,
	})

	tests := []toolImageProtocolCase{
		{
			name: "anthropic messages",
			model: schema.Model{
				ID: "claude-test", Api: schema.ApiAnthropicMessages,
				Provider: schema.ProviderAnthropic, InputTypes: []string{"text", "image"},
			},
			want: []string{`"type":"tool_result"`, `"type":"image"`, `"media_type":"image/png"`, wantBase64, "image metadata"},
		},
		{
			name: "openai completions",
			model: schema.Model{
				ID: "gpt-test", Api: schema.ApiOpenAICompletions,
				Provider: schema.ProviderOpenAI, InputTypes: []string{"text", "image"},
			},
			want: []string{`"role":"tool"`, `"type":"image_url"`, "data:image/png;base64," + wantBase64, "image metadata"},
		},
		{
			name: "openai responses",
			model: schema.Model{
				ID: "gpt-test", Api: schema.ApiOpenAIResponses,
				Provider: schema.ProviderOpenAI, InputTypes: []string{"text", "image"},
			},
			want: []string{`"type":"function_call_output"`, `"type":"input_text"`, `"type":"input_image"`, "data:image/png;base64," + wantBase64, "image metadata"},
		},
		{
			name: "google generative",
			model: schema.Model{
				ID: "gemini-test", Api: schema.ApiGoogleGenerativeAI,
				Provider: schema.ProviderGoogle, InputTypes: []string{"text", "image"},
			},
			want: []string{`"functionResponse"`, `"inlineData"`, `"mimeType":"image/png"`, wantBase64, "image metadata"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertToolImageProtocolPayload(t, tt, message, imagePath)
		})
	}
}

func assertToolImageProtocolPayload(t *testing.T, tt toolImageProtocolCase, message schema.Message, imagePath string) {
	t.Helper()
	protocol := ForAPI(tt.model.Api)
	if protocol == nil {
		t.Fatalf("no protocol registered for %s", tt.model.Api)
	}
	body, err := protocol.BuildRequest(tt.model, schema.Context{
		Messages: []schema.Message{message},
	}, schema.StreamOptions{}, schema.ResolveProfile(tt.model))
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	payload := string(body)
	for _, want := range tt.want {
		if !strings.Contains(payload, want) {
			t.Errorf("payload missing %q: %s", want, payload)
		}
	}
	if strings.Contains(payload, imagePath) {
		t.Errorf("payload leaked a filesystem path instead of encoded content: %s", payload)
	}
}

func TestToolResultImageTextOnlyDowngradeHasNoImagePayload(t *testing.T) {
	blocks, wantBase64 := imageBlock(t)
	imagePath := blocks[1].ImageData
	models := []schema.Model{
		{ID: "claude-test", Api: schema.ApiAnthropicMessages, Provider: schema.ProviderAnthropic},
		{ID: "gpt-test", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderOpenAI},
		{ID: "gpt-test", Api: schema.ApiOpenAIResponses, Provider: schema.ProviderOpenAI},
		{ID: "gemini-test", Api: schema.ApiGoogleGenerativeAI, Provider: schema.ProviderGoogle},
	}
	for _, model := range models {
		t.Run(string(model.Api), func(t *testing.T) {
			assertTextOnlyToolImageProtocol(t, model, imagePath, wantBase64)
		})
	}
}

func assertTextOnlyToolImageProtocol(t *testing.T, model schema.Model, imagePath, wantBase64 string) {
	t.Helper()
	model.InputTypes = []string{"text"}
	message := schema.NewToolResultMessage("call_read", "read", "image metadata", false)
	message.Content = append(message.Content, schema.ContentBlock{
		Type: schema.ContentBlockImage, ImageData: imagePath,
	})
	profile := schema.ResolveProfile(model)
	hook := &hooks.MessageHook{}
	if err := hook.Init(profile); err != nil {
		t.Fatal(err)
	}
	request := &hooks.RequestContext{
		Model: model, Context: schema.Context{Messages: []schema.Message{message}},
	}
	if err := hook.ApplyRequest(request); err != nil {
		t.Fatal(err)
	}
	body, err := ForAPI(model.Api).BuildRequest(model, request.Context, schema.StreamOptions{}, profile)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(body)
	if strings.Contains(payload, wantBase64) || strings.Contains(payload, imagePath) {
		t.Errorf("text-only payload contains image data: %s", payload)
	}
	if !strings.Contains(payload, schema.ToolImageOmittedPlaceholder) {
		t.Errorf("text-only payload lacks explicit omission notice: %s", payload)
	}
}

func TestToolResultWithoutImagesKeepsTextWireShape(t *testing.T) {
	message := schema.NewToolResultMessage("call_read", "read", "plain result", false)
	model := schema.Model{
		ID: "gpt-test", Api: schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI, InputTypes: []string{"text", "image"},
	}
	body, err := ForAPI(model.Api).BuildRequest(model, schema.Context{
		Messages: []schema.Message{message},
	}, schema.StreamOptions{}, schema.ResolveProfile(model))
	if err != nil {
		t.Fatal(err)
	}
	payload := string(body)
	if !strings.Contains(payload, `"content":"plain result"`) {
		t.Errorf("ordinary tool result no longer uses a text string: %s", payload)
	}
	if strings.Contains(payload, "image_url") {
		t.Errorf("ordinary tool result gained an image envelope: %s", payload)
	}
}
