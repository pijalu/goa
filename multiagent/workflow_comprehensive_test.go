// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/prompts"
)

func TestWorkflowRegistry_LoadWorkflowTree_Empty(t *testing.T) {
	dir := t.TempDir()
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	err := wr.LoadWorkflowTree(dir)
	if err != nil {
		t.Fatalf("LoadWorkflowTree on empty dir: %v", err)
	}
	if len(wr.All()) != 0 {
		t.Errorf("expected 0 workflows, got %d", len(wr.All()))
	}
}

func TestWorkflowRegistry_LoadWorkflowTree_ValidDefinition(t *testing.T) {
	dir := t.TempDir()
	wfDir := filepath.Join(dir, "test-workflow")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}
	def := `
id: test-workflow
name: Test Workflow
description: A test workflow
stages:
  - id: plan
    name: Plan
    agent: planner
    prompt: plan.md
  - id: code
    name: Implement
    agent: coder
    prompt: implement.md`
	if err := os.WriteFile(filepath.Join(wfDir, "definition.yaml"), []byte(def), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "plan.md"), []byte("You are the planner"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "implement.md"), []byte("You are the coder"), 0644); err != nil {
		t.Fatal(err)
	}

	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	if err := wr.LoadWorkflowTree(dir); err != nil {
		t.Fatalf("LoadWorkflowTree: %v", err)
	}

	wf, ok := wr.Get("test-workflow")
	if !ok {
		t.Fatal("expected workflow 'test-workflow' to be loaded")
	}
	if wf.Name != "Test Workflow" {
		t.Errorf("expected name 'Test Workflow', got %q", wf.Name)
	}
	if len(wf.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(wf.Stages))
	}
	if wf.Stages[0].Agent != "planner" {
		t.Errorf("expected stage 0 agent 'planner', got %q", wf.Stages[0].Agent)
	}
}

func TestWorkflowRegistry_LoadWorkflowTree_MissingDefinition(t *testing.T) {
	dir := t.TempDir()
	wfDir := filepath.Join(dir, "test-workflow")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}
	// No definition.yaml — should be skipped silently

	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	err := wr.LoadWorkflowTree(dir)
	if err != nil {
		t.Fatalf("LoadWorkflowTree should skip dirs without definition.yaml: %v", err)
	}
	if len(wr.All()) != 0 {
		t.Errorf("expected 0 workflows, got %d", len(wr.All()))
	}
}

func TestWorkflowRegistry_LoadWorkflowTree_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	wfDir := filepath.Join(dir, "test-workflow")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "definition.yaml"), []byte("invalid: [yaml: "), 0644); err != nil {
		t.Fatal(err)
	}

	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	err := wr.LoadWorkflowTree(dir)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestWorkflowRegistry_LoadWorkflowTree_MultipleWorkflows(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"wf1", "wf2"} {
		wfDir := filepath.Join(dir, name)
		if err := os.MkdirAll(wfDir, 0755); err != nil {
			t.Fatal(err)
		}
		def := "id: " + name + "\nname: " + name + "\nstages:\n  - {id: s1, agent: coder, prompt: do it}"
		if err := os.WriteFile(filepath.Join(wfDir, "definition.yaml"), []byte(def), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	if err := wr.LoadWorkflowTree(dir); err != nil {
		t.Fatalf("LoadWorkflowTree: %v", err)
	}

	if len(wr.All()) != 2 {
		t.Errorf("expected 2 workflows, got %d", len(wr.All()))
	}
	if _, ok := wr.Get("wf1"); !ok {
		t.Error("expected wf1")
	}
	if _, ok := wr.Get("wf2"); !ok {
		t.Error("expected wf2")
	}
}

func TestWorkflowRegistry_LoadWorkflowTree_NestedDirectories(t *testing.T) {
	dir := t.TempDir()

	// workflows/nested/test should also be discovered
	nestedDir := filepath.Join(dir, "nested", "test")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	def := "id: nested-test\nname: Nested\nstages:\n  - {id: s1, agent: coder, prompt: do it}"
	if err := os.WriteFile(filepath.Join(nestedDir, "definition.yaml"), []byte(def), 0644); err != nil {
		t.Fatal(err)
	}

	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	if err := wr.LoadWorkflowTree(dir); err != nil {
		t.Fatalf("LoadWorkflowTree: %v", err)
	}

	wf, ok := wr.Get("nested-test")
	if !ok {
		t.Fatal("expected nested workflow to be loaded")
	}
	if wf.Name != "Nested" {
		t.Errorf("expected name 'Nested', got %q", wf.Name)
	}
}

// ── Prompt Resolution Tests ──────────────────────────────────────────────

func TestResolvePrompt_RelativePath(t *testing.T) {
	workflowDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workflowDir, "plan.md"), []byte("planner content"), 0644); err != nil {
		t.Fatal(err)
	}

	stage := PipelineStage{Prompt: "plan.md"}
	prompt, err := ResolvePromptWithDir(stage, nil, workflowDir)
	if err != nil {
		t.Fatalf("ResolvePromptWithDir: %v", err)
	}
	if prompt != "planner content" {
		t.Errorf("expected 'planner content', got %q", prompt)
	}
}

func TestResolvePrompt_RelativePath_Missing_FallsBackToInline(t *testing.T) {
	workflowDir := t.TempDir()

	stage := PipelineStage{Prompt: "missing.md"}
	result, err := ResolvePromptWithDir(stage, nil, workflowDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Falls back to inline text when file doesn't exist
	if result != "missing.md" {
		t.Errorf("expected inline fallback 'missing.md', got %q", result)
	}
}

func TestResolvePrompt_InlineText(t *testing.T) {
	stage := PipelineStage{Prompt: "do the work directly"}
	prompt, err := ResolvePromptWithDir(stage, nil, "")
	if err != nil {
		t.Fatalf("ResolvePromptWithDir: %v", err)
	}
	if prompt != "do the work directly" {
		t.Errorf("expected inline text, got %q", prompt)
	}
}

func TestResolvePrompt_PromptsURIFallback(t *testing.T) {
	// Relative path doesn't exist, but there's a prompts:// match
	workflowDir := t.TempDir()
	fs := fstest.MapFS{
		"pipeline.plan.md": {Data: []byte("prompt from registry")},
	}
	pr := prompts.NewRegistry(fs, "", "")
	stage := PipelineStage{Prompt: "prompts://pipeline.plan"}

	prompt, err := ResolvePromptWithDir(stage, pr, workflowDir)
	if err != nil {
		t.Fatalf("ResolvePromptWithDir: %v", err)
	}
	if prompt != "prompt from registry" {
		t.Errorf("expected 'prompt from registry', got %q", prompt)
	}
}

func TestResolvePrompt_RelativeOverridesPromptsURI(t *testing.T) {
	// Both relative and prompts:// exist — relative should win
	workflowDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workflowDir, "plan.md"), []byte("relative override"), 0644); err != nil {
		t.Fatal(err)
	}
	fs := fstest.MapFS{
		"plan.md": {Data: []byte("from registry")},
	}
	pr := prompts.NewRegistry(fs, "", "")
	stage := PipelineStage{Prompt: "plan.md"}

	prompt, err := ResolvePromptWithDir(stage, pr, workflowDir)
	if err != nil {
		t.Fatalf("ResolvePromptWithDir: %v", err)
	}
	if prompt != "relative override" {
		t.Errorf("expected 'relative override', got %q", prompt)
	}
}

// ── Pipeline Run Tests ───────────────────────────────────────────────────

func TestPipelineRun_NewAndCancel(t *testing.T) {
	p := &Pipeline{
		ID: "test", Name: "Test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
			{ID: "s2", Agent: "reviewer", Prompt: "review it"},
		},
	}
	run := NewPipelineRun(p)
	if run.Status != PipelinePending {
		t.Errorf("expected status PipelinePending, got %v", run.Status)
	}

	_, current, stages := run.StatusSnapshot()
	if current != -1 {
		t.Errorf("expected current -1, got %d", current)
	}
	if len(stages) != 2 {
		t.Errorf("expected 2 stages, got %d", len(stages))
	}
	if stages["s1"] != StagePending {
		t.Errorf("expected s1 status pending, got %v", stages["s1"])
	}

	run.Cancel()
	if run.Status != PipelineCancelled {
		t.Errorf("expected status PipelineCancelled after Cancel, got %v", run.Status)
	}
}

func TestPipelineRun_NextStage(t *testing.T) {
	p := &Pipeline{
		ID: "test", Name: "Test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "planner", Prompt: "plan"},
			{ID: "s2", Agent: "coder", Prompt: "code"},
			{ID: "s3", Agent: "reviewer", Prompt: "review"},
		},
	}
	run := NewPipelineRun(p)

	assertNextStage(t, run, 0, "s1", "", "")
	assertNextStage(t, run, 1, "s2", "s1", "s2")
	assertNextStage(t, run, 2, "s3", "s2", "s3")

	_, ok := run.NextStage()
	if ok {
		t.Error("expected NextStage to return false after last stage")
	}
	if run.Status != PipelineCompleted {
		t.Errorf("expected status Completed, got %v", run.Status)
	}
}

func assertNextStage(t *testing.T, run *PipelineRun, wantCurrent int, wantID, wantPrevCompleted, wantCurrentRunning string) {
	t.Helper()
	stage, ok := run.NextStage()
	if !ok {
		t.Fatalf("expected NextStage to return stage %d", wantCurrent)
	}
	if stage.ID != wantID {
		t.Errorf("expected stage %s, got %q", wantID, stage.ID)
	}
	if run.Current != wantCurrent {
		t.Errorf("expected current %d, got %d", wantCurrent, run.Current)
	}
	if run.Status != PipelineRunning {
		t.Errorf("expected status Running, got %v", run.Status)
	}
	if wantPrevCompleted != "" && run.Stages[wantPrevCompleted] != StageCompleted {
		t.Errorf("expected %s status Completed, got %v", wantPrevCompleted, run.Stages[wantPrevCompleted])
	}
	if wantCurrentRunning != "" && run.Stages[wantCurrentRunning] != StageRunning {
		t.Errorf("expected %s status Running, got %v", wantCurrentRunning, run.Stages[wantCurrentRunning])
	}
}

func TestPipelineRun_NextStage_Cancelled(t *testing.T) {
	p := &Pipeline{
		ID: "test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
		},
	}
	run := NewPipelineRun(p)
	run.Cancel()

	_, ok := run.NextStage()
	if ok {
		t.Error("expected NextStage to return false when cancelled")
	}
}

func TestPipelineRun_CompleteStage(t *testing.T) {
	p := &Pipeline{
		ID: "test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
		},
	}
	run := NewPipelineRun(p)
	run.NextStage()
	run.CompleteStage("s1")

	if run.Stages["s1"] != StageCompleted {
		t.Errorf("expected s1 Completed, got %v", run.Stages["s1"])
	}
}

func TestPipelineRun_CompleteStage_Unknown(t *testing.T) {
	p := &Pipeline{
		ID: "test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
		},
	}
	run := NewPipelineRun(p)
	run.CompleteStage("unknown") // should not panic
}

// ── Agent Bus Multi-Agent Tests ──────────────────────────────────────────

func TestAgentBus_ThreeAgents_SendAndReceive(t *testing.T) {
	bus, coderInbox, reviewerInbox := setupThreeAgentBus(t)

	mustSend(t, bus, agentic.CommMessage{From: "planner", To: "coder", Content: "Here is the plan"})
	mustSend(t, bus, agentic.CommMessage{From: "coder", To: "reviewer", Content: "Code is ready"})
	mustSend(t, bus, agentic.CommMessage{From: "reviewer", To: "coder", Content: "Please fix edge case"})

	assertReceived(t, coderInbox, "planner", "Here is the plan")
	assertReceived(t, coderInbox, "reviewer", "Please fix edge case")
	assertReceived(t, reviewerInbox, "coder", "Code is ready")
}

func setupThreeAgentBus(t *testing.T) (*agentic.AgentBus, <-chan agentic.CommMessage, <-chan agentic.CommMessage) {
	t.Helper()
	bus := agentic.NewAgentBus()
	if _, err := bus.Register("planner"); err != nil {
		t.Fatal(err)
	}
	coderInbox, err := bus.Register("coder")
	if err != nil {
		t.Fatal(err)
	}
	reviewerInbox, err := bus.Register("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	names := bus.AgentNames()
	if len(names) != 3 {
		t.Errorf("expected 3 agents, got %d: %v", len(names), names)
	}
	return bus, coderInbox, reviewerInbox
}

func mustSend(t *testing.T, bus *agentic.AgentBus, msg agentic.CommMessage) {
	t.Helper()
	if err := bus.Send(context.Background(), msg); err != nil {
		t.Fatalf("%s→%s send: %v", msg.From, msg.To, err)
	}
}

func assertReceived(t *testing.T, inbox <-chan agentic.CommMessage, wantFrom, wantContent string) {
	t.Helper()
	msg := <-inbox
	if msg.From != wantFrom || msg.Content != wantContent {
		t.Errorf("got %+v, expected from=%s content=%s", msg, wantFrom, wantContent)
	}
}

func TestAgentBus_SendToSelf_Allowed(t *testing.T) {
	bus := agentic.NewAgentBus()
	inbox, err := bus.Register("agent")
	if err != nil {
		t.Fatal(err)
	}

	err = bus.Send(context.Background(), agentic.CommMessage{From: "agent", To: "agent", Content: "self"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg := <-inbox
	if msg.Content != "self" {
		t.Errorf("expected 'self', got %q", msg.Content)
	}
}

func TestAgentBus_SendToUnknown_Error(t *testing.T) {
	bus := agentic.NewAgentBus()
	bus.Register("agent")

	err := bus.Send(context.Background(), agentic.CommMessage{From: "agent", To: "unknown", Content: "msg"})
	if err == nil {
		t.Error("expected error sending to unknown agent")
	}
}

func TestSendMessageTool_MultiAgentEnum(t *testing.T) {
	bus := agentic.NewAgentBus()
	bus.Register("planner")
	bus.Register("coder")
	bus.Register("reviewer")

	tool := &agentic.SendMessageTool{Bus: bus, FromName: "coder"}
	schema := tool.Schema()

	props := schema.Schema["properties"].(map[string]interface{})
	toField := props["to"].(map[string]interface{})
	enum := toField["enum"].([]interface{})

	if len(enum) != 2 {
		t.Fatalf("expected 2 recipients (not self), got %d: %v", len(enum), enum)
	}
	hasPlanner := false
	hasReviewer := false
	for _, e := range enum {
		switch e.(string) {
		case "planner":
			hasPlanner = true
		case "reviewer":
			hasReviewer = true
		case "coder":
			t.Error("coder should not be in its own enum")
		}
	}
	if !hasPlanner || !hasReviewer {
		t.Error("expected planner and reviewer in enum for coder's tool")
	}
}

func TestSendMessageTool_Execute_TargetsCorrectAgent(t *testing.T) {
	bus := agentic.NewAgentBus()
	coderInbox, err := bus.Register("coder")
	if err != nil {
		t.Fatal(err)
	}
	bus.Register("reviewer")

	tool := &agentic.SendMessageTool{Bus: bus, FromName: "planner"}
	input, _ := json.Marshal(map[string]string{"to": "coder", "content": "plan details"})
	result, err := tool.Execute(string(input))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "coder") {
		t.Errorf("expected result to mention coder, got %q", result)
	}

	msg := <-coderInbox
	if msg.From != "planner" || msg.Content != "plan details" {
		t.Errorf("coder received %+v, expected planner msg", msg)
	}
}

func TestReceiveMessageTool_ReadsInbox(t *testing.T) {
	bus := agentic.NewAgentBus()
	bus.Register("planner")
	inbox, err := bus.Register("coder")
	if err != nil {
		t.Fatal(err)
	}

	bus.Send(context.Background(), agentic.CommMessage{From: "planner", To: "coder", Content: "hello from planner"})

	time.Sleep(10 * time.Millisecond) // allow send to complete

	tool := &agentic.ReceiveMessageTool{Inbox: inbox}
	result, err := tool.Execute("{}")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "planner") || !strings.Contains(result, "hello from planner") {
		t.Errorf("expected planner message, got %q", result)
	}
}

func TestReceiveMessageTool_EmptyInbox(t *testing.T) {
	bus := agentic.NewAgentBus()
	inbox, err := bus.Register("agent")
	if err != nil {
		t.Fatal(err)
	}

	tool := &agentic.ReceiveMessageTool{Inbox: inbox}
	result, err := tool.Execute("{}")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "No new messages") {
		t.Errorf("expected 'No new messages', got %q", result)
	}
}

func TestReceiveMessageTool_ClosedInbox(t *testing.T) {
	bus := agentic.NewAgentBus()
	inbox, err := bus.Register("agent")
	if err != nil {
		t.Fatal(err)
	}
	bus.Unregister("agent") // closes the channel

	tool := &agentic.ReceiveMessageTool{Inbox: inbox}
	_, err = tool.Execute("{}")
	if err == nil {
		t.Error("expected error for closed inbox")
	}
}

func TestSetupCommAgent_WiresAgentToBus(t *testing.T) {
	bus := agentic.NewAgentBus()
	agent := agentic.NewAgent(agentic.Config{
		SystemPrompt: "test agent",
	})
	agent.Output = make(chan agentic.Message, 10)

	inbox, sendTool, connector, err := agentic.SetupCommAgent(bus, "coder", agent, true)
	if err != nil {
		t.Fatalf("SetupCommAgent: %v", err)
	}
	defer connector.Stop()

	if inbox == nil {
		t.Error("expected non-nil inbox")
	}
	if sendTool == nil {
		t.Error("expected non-nil send tool")
	}
	if connector == nil {
		t.Error("expected non-nil connector")
	}

	// Verify agent is registered
	names := bus.AgentNames()
	found := false
	for _, n := range names {
		if n == "coder" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'coder' to be registered on bus")
	}
}

// ── Orchestrator Workflow Tests ──────────────────────────────────────────

func TestForegroundOrchestrator_RunWorkflow_MissingWorkflow(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	err := orch.RunWorkflow(context.Background(), wr, "missing", "input")
	if err == nil {
		t.Error("expected error for missing workflow")
	}
}

func TestForegroundOrchestrator_RunWorkflow_SingleStage(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)
	wr.Register(Pipeline{
		ID: "test", Name: "Test",
		Stages: []PipelineStage{
			{ID: "s1", Name: "Stage 1", Agent: "coder", Prompt: "do it"},
		},
	})

	model := testModel("test-model")
	pool := NewAgentPool(model, provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.RunWorkflow(ctx, wr, "test", "user input")
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
}

func TestForegroundOrchestrator_RunWorkflow_Stopped(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)
	wr.Register(Pipeline{
		ID: "test", Name: "Test",
		Stages: []PipelineStage{
			{ID: "s1", Name: "Stage 1", Agent: "coder", Prompt: "do it"},
		},
	})

	model := testModel("test-model")
	pool := NewAgentPool(model, provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.Stop()

	err := orch.RunWorkflow(context.Background(), wr, "test", "input")
	if err == nil {
		t.Error("expected error when orchestrator is stopped")
	}
}

func TestForegroundOrchestrator_RunWorkflow_MultiStage(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)
	wr.Register(Pipeline{
		ID: "test", Name: "Test",
		Stages: []PipelineStage{
			{ID: "plan", Name: "Plan", Agent: "planner", Prompt: "plan it"},
			{ID: "code", Name: "Code", Agent: "coder", Prompt: "code it"},
		},
	})

	model := testModel("test-model")
	pool := NewAgentPool(model, provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := orch.RunWorkflow(ctx, wr, "test", "implement feature")
	if err != nil {
		t.Fatalf("RunWorkflow multi-stage: %v", err)
	}
}

func TestForegroundOrchestrator_RunWorkflow_EmitsProgress(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)
	wr.Register(Pipeline{
		ID: "test", Name: "Test",
		Stages: []PipelineStage{
			{ID: "s1", Name: "Stage 1", Agent: "coder", Prompt: "do it"},
		},
	})

	model := testModel("test-model")
	pool := NewAgentPool(model, provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range orch.Events() {
			if ev.From == "system" && strings.Contains(ev.Content, "complete") {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := orch.RunWorkflow(ctx, wr, "test", "input"); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for completion event")
	}
}

func TestForegroundOrchestrator_RunWorkflow_WithAgentBus(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)
	wr.Register(Pipeline{
		ID: "multi-agent", Name: "Multi Agent Test",
		Stages: []PipelineStage{
			{ID: "plan", Name: "Plan", Agent: "planner", Prompt: "create plan"},
			{ID: "code", Name: "Code", Agent: "coder", Prompt: "implement plan"},
		},
	})

	model := testModel("test-model")
	pool := NewAgentPool(model, provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// Set up bus with all workflow agents
	bus := agentic.NewAgentBus()
	bus.Register("planner")
	bus.Register("coder")
	orch.SetAgentBus(bus)

	// Bus names should include both agents
	names := bus.AgentNames()
	if len(names) != 2 {
		t.Errorf("expected 2 agents on bus, got %d: %v", len(names), names)
	}
}

func TestForegroundOrchestrator_Progress_NoOrchestrator(t *testing.T) {
	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	progress := orch.Progress()
	if progress.Status != "" {
		t.Errorf("expected empty initial progress, got %q", progress.Status)
	}
}

func TestForegroundOrchestrator_Progress_TracksStages(t *testing.T) {
	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// Simulate stage progression
	orch.setProgress(WorkflowProgress{
		StageIndex: 0, TotalStages: 3, StageName: "Plan", StageID: "plan", Status: "running",
	})
	p := orch.Progress()
	if p.StageIndex != 0 || p.TotalStages != 3 || p.StageName != "Plan" {
		t.Errorf("unexpected progress: %+v", p)
	}

	orch.setProgress(WorkflowProgress{
		StageIndex: 1, TotalStages: 3, StageName: "Code", StageID: "code", Status: "running",
	})
	p = orch.Progress()
	if p.StageIndex != 1 {
		t.Errorf("expected stage index 1, got %d", p.StageIndex)
	}
}

// ── WorkflowNextTool Tests ───────────────────────────────────────────────

func TestWorkflowNextTool_Schema(t *testing.T) {
	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	run := NewPipelineRun(&Pipeline{
		ID: "test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
			{ID: "s2", Agent: "reviewer", Prompt: "review it"},
		},
	})

	tool := &WorkflowNextTool{Orchestrator: orch, Run: run}
	schema := tool.Schema()
	if schema.Name != "workflows_next" {
		t.Errorf("expected name 'workflows_next', got %q", schema.Name)
	}
	if schema.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestWorkflowNextTool_Execute_SignalsStageDone(t *testing.T) {
	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	run := NewPipelineRun(&Pipeline{
		ID: "test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "planner", Prompt: "plan"},
			{ID: "s2", Agent: "coder", Prompt: "code"},
		},
	})

	// Start first stage
	stage, ok := run.NextStage()
	if !ok || stage.ID != "s1" {
		t.Fatalf("expected stage s1, got %+v", stage)
	}

	cancelled := false
	orch.stageCancel = func() { cancelled = true }

	tool := &WorkflowNextTool{Orchestrator: orch, Run: run}
	orch.SetStageToolCount(1)
	result, err := tool.Execute("{}")
	if err != nil {
		t.Fatalf("WorkflowNextTool.Execute: %v", err)
	}

	// The tool must NOT advance the run itself; that is RunWorkflow's job.
	if run.Current != 0 {
		t.Errorf("expected current unchanged (0), got %d", run.Current)
	}
	if !cancelled {
		t.Error("expected stage context to be cancelled")
	}
	if !strings.Contains(result, "Stage complete") {
		t.Errorf("expected result to mention stage completion, got %q", result)
	}
}

func TestWorkflowNextTool_Execute_LastStage(t *testing.T) {
	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	run := NewPipelineRun(&Pipeline{
		ID: "test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
		},
	})

	run.NextStage() // start the only stage

	cancelled := false
	orch.stageCancel = func() { cancelled = true }

	tool := &WorkflowNextTool{Orchestrator: orch, Run: run}
	orch.SetStageToolCount(1)
	result, err := tool.Execute("{}")
	if err != nil {
		t.Fatalf("WorkflowNextTool.Execute: %v", err)
	}

	// The tool only signals completion; it does not mark the run completed.
	if run.Current != 0 {
		t.Errorf("expected current unchanged (0), got %d", run.Current)
	}
	if run.Status == PipelineCompleted {
		t.Error("tool should not mark run as completed")
	}
	if !cancelled {
		t.Error("expected stage context to be cancelled")
	}
	if !strings.Contains(result, "Stage complete") {
		t.Errorf("expected result to mention stage completion, got %q", result)
	}
}

func TestWorkflowNextTool_Execute_NoRun(t *testing.T) {
	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	tool := &WorkflowNextTool{Orchestrator: orch, Run: nil}
	_, err := tool.Execute("{}")
	if err == nil {
		t.Error("expected error when Run is nil")
	}
}

func TestWorkflowNextTool_Execute_CancelledRun(t *testing.T) {
	pool := NewAgentPool(testModel("test"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	run := NewPipelineRun(&Pipeline{
		ID: "test",
		Stages: []PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
		},
	})
	run.Cancel()

	orch.SetStageToolCount(1)
	tool := &WorkflowNextTool{Orchestrator: orch, Run: run}
	orch.SetStageToolCount(1)
	_, err := tool.Execute("{}")
	if err == nil {
		t.Error("expected error when run is cancelled")
	}
}

// ── Workflow Command Tests ───────────────────────────────────────────────
