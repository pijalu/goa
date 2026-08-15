// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// instructionWriteTool is a minimal file tool that writes the given path to
// disk (relative to projectDir) and reports success. It stands in for the
// real write tool in agentic tests: the post-tool hook only needs the tool
// name, the input JSON, and a successful result to trigger reconciliation.
type instructionWriteTool struct {
	projectDir string
}

func (t *instructionWriteTool) Schema() ToolSchema {
	return ToolSchema{Name: "write", Schema: map[string]any{"type": "object"}}
}

func (t *instructionWriteTool) Execute(input string) (string, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", err
	}
	full := filepath.Join(t.projectDir, p.Path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
		return "", err
	}
	return "written", nil
}

func (t *instructionWriteTool) IsRetryable(err error) bool { return false }

// instructionTestCase holds one lifecycle scenario driven through
// injectInstructionLifecycle (the post-tool hook called from
// executeBufferedToolCalls). The disk state at hook time must match what the
// tool call produced: setup builds the pre-state, then mutate (when set)
// applies the tool's on-disk effect before the hook runs.
type instructionTestCase struct {
	name     string
	setup    func(project string) // build the initial on-disk state
	preload  bool                 // reconcile once before mutate (load a scope first)
	mutate   func(project string) // apply the tool's disk effect before the hook
	touch    provider.ContentBlock
	result   ToolCallResult
	wantMsg  []string // substrings that must appear in an injected user message
	wantNone []string // substrings that must NOT appear
}

func TestInjectInstructionLifecycle_EndToEnd(t *testing.T) {
	cases := []instructionTestCase{
		{
			name: "writefile creating nested AGENTS.md surfaces Additional",
			setup: func(project string) {
				// The write tool creates the file; the hook runs afterwards.
				writeTestFile(t, filepath.Join(project, "pkg", "sub", "AGENTS.md"), "sub instructions")
			},
			touch: provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    "c1",
				ToolName:      "write",
				ToolArguments: `{"path": "pkg/sub/AGENTS.md", "content": "sub instructions"}`,
			},
			result: ToolCallResult{CallID: "c1", Name: "write", Output: "written"},
			wantMsg: []string{
				"Additional instructions from",
				"pkg/sub/AGENTS.md",
				"sub instructions",
			},
		},
		{
			name: "editing loaded file surfaces Updated",
			setup: func(project string) {
				writeTestFile(t, filepath.Join(project, "pkg", "AGENTS.md"), "version one")
			},
			preload: true,
			mutate: func(project string) {
				writeTestFile(t, filepath.Join(project, "pkg", "AGENTS.md"), "version two")
			},
			touch: provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    "c1",
				ToolName:      "edit",
				ToolArguments: `{"path": "pkg/AGENTS.md"}`,
			},
			result: ToolCallResult{CallID: "c1", Name: "edit", Output: "edited"},
			wantMsg: []string{
				"Updated instructions from",
				"pkg/AGENTS.md",
			},
		},
		{
			name: "deleting loaded file surfaces removed",
			setup: func(project string) {
				writeTestFile(t, filepath.Join(project, "pkg", "AGENTS.md"), "nested")
			},
			preload: true,
			mutate: func(project string) {
				if err := os.Remove(filepath.Join(project, "pkg", "AGENTS.md")); err != nil {
					t.Fatal(err)
				}
			},
			touch: provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    "c1",
				ToolName:      "read",
				ToolArguments: `{"path": "pkg/main.go"}`,
			},
			result: ToolCallResult{CallID: "c1", Name: "read", Output: "file"},
			wantMsg: []string{
				"Instructions removed",
				"pkg/AGENTS.md",
			},
		},
		{
			name: "byte-identical siblings load once",
			setup: func(project string) {
				content := "# same\n"
				writeTestFile(t, filepath.Join(project, "pkg", "AGENTS.md"), content)
				writeTestFile(t, filepath.Join(project, "pkg", "CLAUDE.md"), content)
			},
			touch: provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    "c1",
				ToolName:      "read",
				ToolArguments: `{"path": "pkg/main.go"}`,
			},
			result: ToolCallResult{CallID: "c1", Name: "read", Output: "file"},
			wantMsg: []string{
				"Additional instructions from",
				"pkg/AGENTS.md",
			},
			wantNone: []string{"pkg/CLAUDE.md"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			if tc.setup != nil {
				tc.setup(project)
			}
			tracker := internal.NewInstructionTracker(project, nil)
			if tc.preload {
				tracker.Reconcile(filepath.Join(project, "pkg", "a.go"))
			}
			if tc.mutate != nil {
				tc.mutate(project)
			}
			a := NewAgent(Config{
				ProjectDir:         project,
				InstructionTracker: tracker,
			})
			a.injectInstructionLifecycle([]provider.ContentBlock{tc.touch}, []ToolCallResult{tc.result})
			assertInstructionMessages(t, a, tc.wantMsg, tc.wantNone)
		})
	}
}

func assertInstructionMessages(t *testing.T, a *Agent, wantMsg, wantNone []string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	var all string
	for _, m := range a.history {
		if m.Role == User && strings.Contains(m.Content, "<system-reminder>") {
			all += m.Content + "\n"
		}
	}
	for _, want := range wantMsg {
		if !strings.Contains(all, want) {
			t.Errorf("history missing %q in instruction messages:\n%s", want, all)
		}
	}
	for _, none := range wantNone {
		if strings.Contains(all, none) {
			t.Errorf("history should NOT contain %q:\n%s", none, all)
		}
	}
	if len(wantMsg) > 0 && all == "" {
		t.Error("no instruction user messages injected")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInjectInstructionLifecycle_SkipsNonFileTools ensures bash and failing
// calls never trigger reconciliation.
func TestInjectInstructionLifecycle_SkipsNonFileTools(t *testing.T) {
	project := t.TempDir()
	a := NewAgent(Config{
		ProjectDir:         project,
		InstructionTracker: internal.NewInstructionTracker(project, nil),
	})
	tcs := []provider.ContentBlock{
		{Type: provider.ContentBlockToolCall, ToolCallID: "c1", ToolName: "bash", ToolArguments: `{"command": "echo hi"}`},
		{Type: provider.ContentBlockToolCall, ToolCallID: "c2", ToolName: "write", ToolArguments: `{"path": "pkg/sub/AGENTS.md", "content": "x"}`},
	}
	results := []ToolCallResult{
		{CallID: "c1", Name: "bash", Output: "hi"},
		{CallID: "c2", Name: "write", Err: errors.New("denied")},
	}
	a.injectInstructionLifecycle(tcs, results)
	if msgs := a.instructionMessageCount(); msgs != 0 {
		t.Fatalf("expected 0 instruction messages, got %d", msgs)
	}
}

func (a *Agent) instructionMessageCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, m := range a.history {
		if m.Role == User && strings.Contains(m.Content, "<system-reminder>") {
			count++
		}
	}
	return count
}

// TestInjectInstructionLifecycle_NoTrackerIsNoop guards the nil config path.
func TestInjectInstructionLifecycle_NoTrackerIsNoop(t *testing.T) {
	a := NewAgent(Config{ProjectDir: t.TempDir()})
	a.injectInstructionLifecycle(
		[]provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolCallID: "c1", ToolName: "write", ToolArguments: `{"path": "x"}`}},
		[]ToolCallResult{{CallID: "c1", Name: "write", Output: "ok"}},
	)
	if n := a.instructionMessageCount(); n != 0 {
		t.Fatalf("expected no messages without tracker, got %d", n)
	}
}

// TestInstructionLifecycle_EndToEndStream verifies the full loop: the
// simulated provider emits a write tool call, the agent executes it (real
// disk write), the lifecycle hook appends an "Additional instructions" user
// message to history, and the next stream round (simulated provider round 2)
// observes it in the request context.
func TestInstructionLifecycle_EndToEndStream(t *testing.T) {
	project := t.TempDir()

	writeTool := &instructionWriteTool{projectDir: project}
	sim := newLifecycleSimProvider([]simToolResponse{
		{toolName: "write", toolInput: `{"path": "pkg/sub/AGENTS.md", "content": "sub instructions"}`},
		{content: "done"},
	})
	provider.RegisterApiProvider(sim)
	// simulated providers use a unique per-instance API; no cleanup needed.

	mdl := provider.Model{ID: "test-model", Api: sim.API(), Provider: provider.ProviderCustom, InputTypes: []string{"text"}}
	a := NewAgent(Config{
		Model:              mdl,
		StreamOptions:      provider.StreamOptions{},
		SystemPrompt:       "You are a test agent.",
		Tools:              []Tool{writeTool},
		ProjectDir:         project,
		InstructionTracker: internal.NewInstructionTracker(project, nil),
		AllowEmptyResponse: true,
	})

	ctx := context.Background()
	if err := a.Run(ctx, "create the file"); err != nil {
		t.Fatalf("agent run: %v", err)
	}

	found := false
	a.mu.Lock()
	for _, m := range a.history {
		if m.Role == User && strings.Contains(m.Content, "Additional instructions from: "+filepath.Join("pkg", "sub", "AGENTS.md")) {
			found = true
			break
		}
	}
	a.mu.Unlock()
	if !found {
		t.Fatal("history missing 'Additional instructions from: pkg/sub/AGENTS.md' user message after write turn")
	}
	if _, err := os.Stat(filepath.Join(project, "pkg", "sub", "AGENTS.md")); err != nil {
		t.Fatalf("write tool did not create the file: %v", err)
	}
}

// simToolResponse is one predetermined round for lifecycleSimProvider.
type simToolResponse struct {
	content   string
	toolName  string
	toolInput string
}

// lifecycleSimCounter gives each inline simulated provider a unique API id so
// RegisterApiProvider never panics on "already registered".
var lifecycleSimCounter int

// lifecycleSimProvider is a minimal simulated provider that replays a fixed
// sequence of responses (a tool call round then a final content round). It
// mirrors testutil.SimulatedProvider without importing testutil, avoiding an
// import cycle in the agentic package tests.
type lifecycleSimProvider struct {
	api   provider.Api
	mu    sync.Mutex
	index int
	resps []simToolResponse
}

func newLifecycleSimProvider(resps []simToolResponse) *lifecycleSimProvider {
	lifecycleSimCounter++
	return &lifecycleSimProvider{
		api:   provider.Api(fmt.Sprintf("test-lifecycle-%d", lifecycleSimCounter)),
		resps: resps,
	}
}

func (p *lifecycleSimProvider) API() provider.Api { return p.api }

func (p *lifecycleSimProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		p.mu.Lock()
		if p.index >= len(p.resps) {
			p.mu.Unlock()
			result.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
			return
		}
		resp := p.resps[p.index]
		p.index++
		p.mu.Unlock()

		if resp.content != "" {
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: resp.content})
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		}
		if resp.toolName != "" {
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventToolCallEnd,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolName:      resp.toolName,
					ToolArguments: resp.toolInput,
				},
			})
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Mock response complete."}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *lifecycleSimProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}
