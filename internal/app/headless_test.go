// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// recordingRenderer captures HeadlessRenderer method calls for tests.
type recordingRenderer struct {
	calls []string
}

func (r *recordingRenderer) record(name string, args ...string) {
	call := name
	if len(args) > 0 {
		call += ":" + strings.Join(args, "|")
	}
	r.calls = append(r.calls, call)
}

func (r *recordingRenderer) UserPrompt(prompt string)        { r.record("UserPrompt", prompt) }
func (r *recordingRenderer) AssistantChunk(text string)      { r.record("AssistantChunk", text) }
func (r *recordingRenderer) ThinkingStart()                  { r.record("ThinkingStart") }
func (r *recordingRenderer) ThinkingChunk(text string)       { r.record("ThinkingChunk", text) }
func (r *recordingRenderer) ThinkingEnd()                    { r.record("ThinkingEnd") }
func (r *recordingRenderer) ToolCall(name, id, input string) { r.record("ToolCall", name, id, input) }
func (r *recordingRenderer) ToolResult(name, id, output string) {
	r.record("ToolResult", name, id, output)
}
func (r *recordingRenderer) Stats(stats sessionStats, turn int) {
	r.record("Stats", strconv.Itoa(turn), formatFooterStats(stats))
}
func (r *recordingRenderer) Summary(stats sessionStats, turns int, totalTime time.Duration) {
	r.record("Summary", strconv.Itoa(turns), formatFooterStats(stats), totalTime.String())
}
func (r *recordingRenderer) Error(msg string)    { r.record("Error", msg) }
func (r *recordingRenderer) AssistantStreamEnd() { r.record("AssistantStreamEnd") }
func (r *recordingRenderer) CompanionStart(cycle int) {
	r.record("CompanionStart", strconv.Itoa(cycle))
}
func (r *recordingRenderer) CompanionEnd(cycle int)  { r.record("CompanionEnd", strconv.Itoa(cycle)) }
func (r *recordingRenderer) CompanionThinkingStart() { r.record("CompanionThinkingStart") }
func (r *recordingRenderer) CompanionThinkingChunk(text string) {
	r.record("CompanionThinkingChunk", text)
}
func (r *recordingRenderer) CompanionThinkingEnd()      { r.record("CompanionThinkingEnd") }
func (r *recordingRenderer) CompanionChunk(text string) { r.record("CompanionChunk", text) }
func (r *recordingRenderer) Flush()                     { r.record("Flush") }

func TestToolConfirmDescription(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"bash", `{"command":"ls -la"}`, "bash: {\"command\":\"ls -la\"}"},
		{"read", "", "read"},
		{"edit", strings.Repeat("a", 100), "edit: " + strings.Repeat("a", 77) + "..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolConfirmDescription(tc.name, tc.input)
			if got != tc.want {
				t.Errorf("toolConfirmDescription(%q, %q) = %q, want %q", tc.name, tc.input, got, tc.want)
			}
		})
	}
}

func TestAutoConfirmStrategy(t *testing.T) {
	s := autoConfirmStrategy{}
	approved, err := s.Confirm("bash", "rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Error("autoConfirmStrategy should always approve")
	}
}

func TestTTYConfirmStrategy_ApprovesYes(t *testing.T) {
	in := bytes.NewBufferString("yes\n")
	out := &bytes.Buffer{}
	s := &ttyConfirmStrategy{in: bufio.NewReader(in), out: out}
	approved, err := s.Confirm("bash", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Error("expected approval for 'yes'")
	}
	if !strings.Contains(out.String(), "Approve bash: ls [y/N]?") {
		t.Errorf("unexpected prompt: %q", out.String())
	}
}

func TestTTYConfirmStrategy_RejectsNo(t *testing.T) {
	in := bytes.NewBufferString("no\n")
	out := &bytes.Buffer{}
	s := &ttyConfirmStrategy{in: bufio.NewReader(in), out: out}
	approved, err := s.Confirm("bash", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Error("expected rejection for 'no'")
	}
}

func TestRejectConfirmStrategy(t *testing.T) {
	out := &bytes.Buffer{}
	s := &rejectConfirmStrategy{out: out}
	approved, err := s.Confirm("bash", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Error("expected rejection")
	}
	if !strings.Contains(out.String(), "Rejected bash: ls") {
		t.Errorf("unexpected rejection message: %q", out.String())
	}
}

func TestRuntimeOptionsValidate(t *testing.T) {
	cases := []struct {
		name    string
		opts    RuntimeOptions
		wantErr bool
	}{
		{"valid prompt", RuntimeOptions{PromptArg: "hello"}, false},
		{"valid prompt-file", RuntimeOptions{PromptFile: "/tmp/prompt.md"}, false},
		{"TUI mode", RuntimeOptions{}, false},
		{"mutually exclusive", RuntimeOptions{PromptArg: "hello", PromptFile: "/tmp/prompt.md"}, true},
		{"bad color", RuntimeOptions{PromptArg: "hello", Color: "blue"}, true},
		{"negative memory budget", RuntimeOptions{PromptArg: "hello", MemoryBudget: -1}, true},
		{"negative max turns", RuntimeOptions{PromptArg: "hello", MaxTurns: -1}, true},
		{"negative timeout", RuntimeOptions{PromptArg: "hello", Timeout: -1 * time.Second}, true},
		{"goal without prompt", RuntimeOptions{Goal: true}, true},
		{"goal with prompt", RuntimeOptions{PromptArg: "hello", Goal: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestRuntimeOptionsUserPrompt(t *testing.T) {
	inline := RuntimeOptions{PromptArg: "hello world"}
	got, err := inline.UserPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("UserPrompt() = %q, want %q", got, "hello world")
	}

	empty := RuntimeOptions{}
	got, err = empty.UserPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("UserPrompt() = %q, want empty", got)
	}
}

func TestRuntimeOptionsUserPromptFromFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(f, []byte("prompt from file\n"), 0644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	o := RuntimeOptions{PromptFile: f}
	got, err := o.UserPrompt()
	if err != nil {
		t.Fatalf("UserPrompt() error = %v", err)
	}
	want := "prompt from file\n"
	if got != want {
		t.Errorf("UserPrompt() = %q, want %q", got, want)
	}
}

func TestRuntimeOptionsUserPromptMissingFile(t *testing.T) {
	o := RuntimeOptions{PromptFile: "/nonexistent/prompt.md"}
	_, err := o.UserPrompt()
	if err == nil {
		t.Fatal("expected error for missing prompt file")
	}
}

func TestPlainRenderer_UserPrompt(t *testing.T) {
	out := &bytes.Buffer{}
	r := newPlainRenderer(out)
	r.UserPrompt("hello world")

	want := "-- user\nhello world\n\n"
	if got := out.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPlainRenderer_AssistantAndThinking(t *testing.T) {
	out := &bytes.Buffer{}
	r := newPlainRenderer(out)
	r.AssistantChunk("Hello ")
	r.ThinkingStart()
	r.ThinkingChunk("thought")
	r.ThinkingEnd()
	r.AssistantChunk("world")
	r.AssistantStreamEnd()

	got := out.String()
	if !strings.Contains(got, "-- assistant\nHello ") {
		t.Errorf("missing assistant start: %q", got)
	}
	if !strings.Contains(got, "-- thinking start\nthought\n-- thinking end") {
		t.Errorf("missing thinking block: %q", got)
	}
	if !strings.Contains(got, "world") {
		t.Errorf("missing final content: %q", got)
	}
}

func TestPlainRenderer_ToolCallAndResult(t *testing.T) {
	out := &bytes.Buffer{}
	r := newPlainRenderer(out)
	r.ToolCall("bash", "call_1", `{"command":"ls"}`)
	r.ToolResult("bash", "call_1", "file.txt")

	got := out.String()
	if !strings.Contains(got, "-- tool call bash id=call_1\n{\"command\":\"ls\"}\n") {
		t.Errorf("missing tool call: %q", got)
	}
	if !strings.Contains(got, "-- tool result bash id=call_1\nfile.txt\n") {
		t.Errorf("missing tool result: %q", got)
	}
}

func TestPlainRenderer_StatsAndSummary(t *testing.T) {
	out := &bytes.Buffer{}
	r := newPlainRenderer(out)
	stats := sessionStats{PromptN: 10, PredictedN: 5}
	r.Stats(stats, 1)
	r.Summary(stats, 1, 2*time.Second)

	got := out.String()
	if !strings.Contains(got, "-- stats turn=1 ↑10 ↓5") {
		t.Errorf("missing stats line: %q", got)
	}
	if !strings.Contains(got, "-- summary turns=1 total_in=10 total_out=5 total_tool_calls=0 total_time=2s") {
		t.Errorf("missing summary line: %q", got)
	}
}

func TestHeadlessApp_HandleAgentEvent(t *testing.T) {
	rr := &recordingRenderer{}
	subs := &subsystems{cfg: &config.Config{}}
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi"}, rr, autoConfirmStrategy{})

	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateThinking, Role: agentic.Assistant, Text: "t1"})
	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: "a1"})
	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolCallID: "c1", ToolInput: `{"command":"ls"}`})
	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolCallID: "c1", Text: "out"})
	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventEnd})

	want := []string{
		"ThinkingStart",
		"ThinkingChunk:t1",
		"ThinkingEnd",
		"AssistantChunk:a1",
		"AssistantStreamEnd",
		"ToolCall:bash|c1|{\"command\":\"ls\"}",
		"ToolResult:bash|c1|out",
		"Stats",
	}
	if len(rr.calls) != len(want) {
		t.Fatalf("got %d calls, want %d:\n%v", len(rr.calls), len(want), rr.calls)
	}
	for i, w := range want {
		if !strings.HasPrefix(rr.calls[i], w) {
			t.Errorf("call %d = %q, want prefix %q", i, rr.calls[i], w)
		}
	}
}

func TestHeadlessApp_HandleOrchestratorMessage(t *testing.T) {
	rr := &recordingRenderer{}
	subs := &subsystems{cfg: &config.Config{}}
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi"}, rr, autoConfirmStrategy{})

	app.handleOrchestratorMessage(multiagent.OrchestratorMessage{Kind: "thinking_start"})
	app.handleOrchestratorMessage(multiagent.OrchestratorMessage{Kind: "thinking_chunk", Content: "ct"})
	app.handleOrchestratorMessage(multiagent.OrchestratorMessage{Kind: "thinking_end"})
	app.handleOrchestratorMessage(multiagent.OrchestratorMessage{Kind: "content", To: "stream_start"})
	app.handleOrchestratorMessage(multiagent.OrchestratorMessage{Kind: "content", To: "stream_chunk", Content: "cc"})

	want := []string{
		"CompanionThinkingStart",
		"CompanionThinkingChunk:ct",
		"CompanionThinkingEnd",
		"CompanionChunk:",
		"CompanionChunk:cc",
	}
	if len(rr.calls) != len(want) {
		t.Fatalf("got %d calls, want %d:\n%v", len(rr.calls), len(want), rr.calls)
	}
	for i, w := range want {
		if rr.calls[i] != w {
			t.Errorf("call %d = %q, want %q", i, rr.calls[i], w)
		}
	}
}

func TestHeadlessApp_ToolResultLooksUpToolName(t *testing.T) {
	rr := &recordingRenderer{}
	subs := &subsystems{cfg: &config.Config{}}
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi"}, rr, autoConfirmStrategy{})

	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolCallID: "c1", ToolInput: `{"command":"ls"}`})
	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolCallID: "c1", Text: "out"})

	found := false
	for _, c := range rr.calls {
		if c == "ToolResult:bash|c1|out" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ToolResult with looked-up name, calls: %v", rr.calls)
	}
}

func TestANSIRenderer_AssistantAndTool(t *testing.T) {
	out := &bytes.Buffer{}
	r := newANSIRenderer(out)
	r.UserPrompt("hi")
	r.AssistantChunk("hello ")
	r.AssistantStreamEnd()
	r.ToolCall("bash", "c1", `{"command":"ls"}`)

	got := out.String()
	if !strings.Contains(got, "User:") {
		t.Errorf("missing User marker: %q", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("missing assistant content: %q", got)
	}
	if !strings.Contains(got, "Tool call bash") {
		t.Errorf("missing tool call marker: %q", got)
	}
}

func TestResolveRenderer_PlainFlag(t *testing.T) {
	r := resolveRenderer(RuntimeOptions{Plain: true})
	if _, ok := r.(*plainRenderer); !ok {
		t.Errorf("expected plainRenderer, got %T", r)
	}
}

func TestResolveRenderer_ColorNever(t *testing.T) {
	r := resolveRenderer(RuntimeOptions{Color: "never"})
	if _, ok := r.(*plainRenderer); !ok {
		t.Errorf("expected plainRenderer for never, got %T", r)
	}
}

func TestResolveRenderer_ColorAlways(t *testing.T) {
	r := resolveRenderer(RuntimeOptions{Color: "always"})
	if _, ok := r.(*ansiRenderer); !ok {
		t.Errorf("expected ansiRenderer for always, got %T", r)
	}
}

func TestHeadlessApp_HandleEndEvent_Cancelled(t *testing.T) {
	rr := &recordingRenderer{}
	subs := &subsystems{cfg: &config.Config{}}
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi"}, rr, autoConfirmStrategy{})

	app.handleEndEvent(&agentic.OutputEvent{
		Type:     agentic.EventEnd,
		Metadata: map[string]string{"cancelled": "true"},
	})

	found := false
	for _, c := range rr.calls {
		if strings.HasPrefix(c, "Error:Generation stopped by user.") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'Generation stopped by user.' error, calls: %v", rr.calls)
	}
}

func TestHeadlessApp_HandleEndEvent_ConnectionError(t *testing.T) {
	rr := &recordingRenderer{}
	subs := &subsystems{cfg: &config.Config{}}
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi"}, rr, autoConfirmStrategy{})

	app.handleEndEvent(&agentic.OutputEvent{
		Type: agentic.EventEnd,
		Text: "connection reset by peer",
	})

	found := false
	for _, c := range rr.calls {
		if strings.HasPrefix(c, "Error:[connection error]") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected connection error message, calls: %v", rr.calls)
	}
}

func TestHeadlessApp_StatsAccumulation(t *testing.T) {
	rr := &recordingRenderer{}
	subs := &subsystems{cfg: &config.Config{}}
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi"}, rr, autoConfirmStrategy{})

	app.handleAgentEvent(&agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN:            100,
			PredictedN:         50,
			PredictedPerSecond: 25,
			CacheReadTokens:    10,
			CacheWriteTokens:   5,
		},
	})
	app.handleAgentEvent(&agentic.OutputEvent{Type: agentic.EventEnd})

	stats := app.buildStats()
	if stats.PromptN != 100 {
		t.Errorf("PromptN = %d, want 100", stats.PromptN)
	}
	if stats.PredictedN != 50 {
		t.Errorf("PredictedN = %d, want 50", stats.PredictedN)
	}
	if stats.SpeedTokPerSec != 25 {
		t.Errorf("SpeedTokPerSec = %f, want 25", stats.SpeedTokPerSec)
	}
	if stats.ToolCalls != 0 {
		t.Errorf("ToolCalls = %d, want 0", stats.ToolCalls)
	}
	if app.turnCount != 1 {
		t.Errorf("turnCount = %d, want 1", app.turnCount)
	}
}
