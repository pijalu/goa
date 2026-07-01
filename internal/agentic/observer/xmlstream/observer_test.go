// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package xmlstream

import (
	"strings"
	"testing"

	agentic "github.com/pijalu/goa/internal/agentic"
)

func TestEscapeXML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "plain text",
			input: "Hello World",
			want:  "Hello World",
		},
		{
			name:  "ampersand",
			input: "Tom & Jerry",
			want:  "Tom &amp; Jerry",
		},
		{
			name:  "less than",
			input: "a < b",
			want:  "a &lt; b",
		},
		{
			name:  "greater than",
			input: "a > b",
			want:  "a &gt; b",
		},
		{
			name:  "all special chars",
			input: "<script>if (a < b & c > d)</script>",
			want:  "&lt;script&gt;if (a &lt; b &amp; c &gt; d)&lt;/script&gt;",
		},
		{
			name:  "unicode",
			input: "Hello 👋 世界",
			want:  "Hello 👋 世界",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeXML(tt.input)
			if got != tt.want {
				t.Errorf("EscapeXML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEscapeXMLAttr(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "plain text",
			input: "Hello World",
			want:  "Hello World",
		},
		{
			name:  "ampersand",
			input: "Tom & Jerry",
			want:  "Tom &amp; Jerry",
		},
		{
			name:  "less than",
			input: "a < b",
			want:  "a &lt; b",
		},
		{
			name:  "greater than",
			input: "a > b",
			want:  "a &gt; b",
		},
		{
			name:  "double quote",
			input: `say "hello"`,
			want:  `say &quot;hello&quot;`,
		},
		{
			name:  "all special chars",
			input: `<tag attr="value">content</tag>`,
			want:  `&lt;tag attr=&quot;value&quot;&gt;content&lt;/tag&gt;`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeXMLAttr(tt.input)
			if got != tt.want {
				t.Errorf("EscapeXMLAttr(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMessageStateReset(t *testing.T) {
	ms := NewMessageState()
	ms.CurrentBlock = BlockContent
	ms.InBlocks = true
	ms.InMessage = true
	ms.SkillCallDepth = 3

	ms.Reset()

	if ms.CurrentBlock != "" {
		t.Errorf("CurrentBlock not reset")
	}
	if ms.InBlocks {
		t.Errorf("InBlocks not reset")
	}
	if ms.InMessage {
		t.Errorf("InMessage not reset")
	}
	if ms.SkillCallDepth != 0 {
		t.Errorf("SkillCallDepth not reset")
	}
}

func TestWriteOpenConversation(t *testing.T) {
	result := WriteOpenConversation("test-id", "gpt-4", "2024-01-01T00:00:00Z")

	if !strings.Contains(result, "<conversation>") {
		t.Error("missing conversation tag")
	}
	if !strings.Contains(result, "<id>test-id</id>") {
		t.Error("missing id tag")
	}
	if !strings.Contains(result, "<model>gpt-4</model>") {
		t.Error("missing model tag")
	}
	if !strings.Contains(result, "<start>2024-01-01T00:00:00Z</start>") {
		t.Error("missing start tag")
	}
}

func TestWriteCloseConversation(t *testing.T) {
	result := WriteCloseConversation()
	if result != "</conversation>\n" {
		t.Errorf("got %q", result)
	}
}

func TestWriteMessageStart(t *testing.T) {
	result := WriteMessageStart()
	if result != "    <message>\n" {
		t.Errorf("got %q", result)
	}
}

func TestWriteMessageEnd(t *testing.T) {
	result := WriteMessageEnd()
	if result != "    </message>\n" {
		t.Errorf("got %q", result)
	}
}

func TestWriteContent(t *testing.T) {
	result := WriteContent("Hello <world> & stuff")
	expected := "        <content>Hello &lt;world&gt; &amp; stuff</content>\n"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestWriteStats(t *testing.T) {
	result := WriteStats(100, 50, 10.5, 25.3)

	if !strings.Contains(result, "<stats>") {
		t.Error("missing stats tag")
	}
	if !strings.Contains(result, "<prompt>100</prompt>") {
		t.Error("missing prompt count")
	}
	if !strings.Contains(result, "<predicted>50</predicted>") {
		t.Error("missing predicted count")
	}
}

func TestWriteSkillCall(t *testing.T) {
	result := "        <skillcall>\n          <name>my-skill</name>\n"
	if !strings.Contains(result, "<skillcall>") {
		t.Error("missing skillcall tag")
	}
	if !strings.Contains(result, "<name>my-skill</name>") {
		t.Error("missing name tag")
	}
}

func TestWriteToolCall(t *testing.T) {
	result := "        <toolcall>\n          <name>calculator</name>\n"
	if !strings.Contains(result, "<toolcall>") {
		t.Error("missing toolcall tag")
	}
	if !strings.Contains(result, "<name>calculator</name>") {
		t.Error("missing name tag")
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{100, "100"},
		{-5, "-5"},
		{12345, "12345"},
	}

	for _, tt := range tests {
		got := itoa(tt.input)
		if got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFtoa(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0.0, "0.00"},
		{1.0, "1.00"},
		{10.5, "10.50"},
		{0.123, "0.12"},
	}

	for _, tt := range tests {
		got := ftoa(tt.input)
		if !strings.HasPrefix(got, tt.want[:len(tt.want)-1]) {
			t.Errorf("ftoa(%f) = %q, want prefix %q", tt.input, got, tt.want)
		}
	}
}

// testWriter captures XML chunks for testing.
type testWriter struct {
	buf strings.Builder
}

func (tw *testWriter) WriteChunk(chunk string) error {
	tw.buf.WriteString(chunk)
	return nil
}

func (tw *testWriter) Close() error   { return nil }
func (tw *testWriter) String() string { return tw.buf.String() }

func TestNestedSkillXMLStructure(t *testing.T) {
	writer := &testWriter{}
	obs, err := NewXMLStreamingObserver(Config{
		Writer:         writer,
		Model:          "test-model",
		ConversationID: "test-conv",
		IncludeTimings: false,
	})
	if err != nil {
		t.Fatalf("Failed to create observer: %v", err)
	}

	// Outer skill call
	obs.OnEvent(agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		ToolName:  "run_skill",
		ToolInput: `{"skill_name":"parse-asset"}`,
	})

	// Content in outer skill
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Text:  "Analyzing document...",
		Role:  agentic.Assistant,
	})

	// Inner skill call
	obs.OnEvent(agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		ToolName:  "run_skill",
		ToolInput: `{"skill_name":"parse-projects"}`,
	})

	// Content in inner skill
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Text:  "Found 3 projects",
		Role:  agentic.Assistant,
	})

	// Inner skill result
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventToolResult,
		Text: `{"projects":[{"name":"P1"}]}`,
	})

	// Content after inner (still in outer)
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Text:  "Continuing analysis...",
		Role:  agentic.Assistant,
	})

	// Outer skill result
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventToolResult,
		Text: `{"status":"complete"}`,
	})

	obs.Flush()

	xml := writer.String()

	// Verify outer skillcall exists
	if !strings.Contains(xml, `<skillcall><name>parse-asset</name>`) {
		t.Error("missing outer skillcall")
	}

	// Verify inner skillcall exists
	if !strings.Contains(xml, `<skillcall><name>parse-projects</name>`) {
		t.Error("missing inner skillcall")
	}

	// Verify inner is nested inside outer's conversation
	outerStart := strings.Index(xml, `<skillcall><name>parse-asset</name>`)
	innerStart := strings.Index(xml, `<skillcall><name>parse-projects</name>`)
	// Find the outer's </skillcall> by looking for the last one after outer start
	outerEnd := strings.LastIndex(xml, `</skillcall>`)

	if innerStart < outerStart {
		t.Errorf("inner skillcall (at %d) not after outer start (at %d)", innerStart, outerStart)
	}

	// Verify content after inner skill is still inside outer
	if !strings.Contains(xml, "Continuing analysis...") {
		t.Error("missing post-inner content")
	}

	// Verify the post-inner content is before the outer skillcall closes
	continuingIdx := strings.Index(xml, "Continuing analysis...")
	if continuingIdx < innerStart || continuingIdx > outerEnd {
		t.Errorf("post-inner content (at %d) not between inner start (at %d) and outer end (at %d)", continuingIdx, innerStart, outerEnd)
	}
}
