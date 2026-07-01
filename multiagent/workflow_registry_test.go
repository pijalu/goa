// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"testing"
	"testing/fstest"

	"github.com/pijalu/goa/prompts"
)

func TestWorkflowRegistry_RegisterAndGet(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	w := Pipeline{ID: "test", Name: "Test", Stages: []PipelineStage{{ID: "s1", Agent: "coder", Prompt: "do it"}}}
	wr.Register(w)

	got, ok := wr.Get("test")
	if !ok {
		t.Fatal("expected workflow to be found")
	}
	if got.ID != "test" {
		t.Errorf("expected id 'test', got %q", got.ID)
	}
}

func TestWorkflowRegistry_Get_Missing(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	_, ok := wr.Get("missing")
	if ok {
		t.Error("expected workflow to be missing")
	}
}

func TestWorkflowRegistry_All(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	wr := NewWorkflowRegistry(pr)

	wr.Register(Pipeline{ID: "a", Name: "A", Stages: []PipelineStage{{ID: "s1", Agent: "coder", Prompt: "x"}}})
	wr.Register(Pipeline{ID: "b", Name: "B", Stages: []PipelineStage{{ID: "s1", Agent: "coder", Prompt: "x"}}})

	all := wr.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(all))
	}
}

func TestValidateWorkflow_MissingID(t *testing.T) {
	w := Pipeline{Stages: []PipelineStage{{ID: "s1", Agent: "coder", Prompt: "x"}}}
	if err := validateWorkflow(w); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestValidateWorkflow_NoStages(t *testing.T) {
	w := Pipeline{ID: "test"}
	if err := validateWorkflow(w); err == nil {
		t.Error("expected error for no stages")
	}
}

func TestValidateWorkflow_MissingStageID(t *testing.T) {
	w := Pipeline{ID: "test", Stages: []PipelineStage{{Agent: "coder", Prompt: "x"}}}
	if err := validateWorkflow(w); err == nil {
		t.Error("expected error for missing stage id")
	}
}

func TestValidateWorkflow_MissingAgent(t *testing.T) {
	w := Pipeline{ID: "test", Stages: []PipelineStage{{ID: "s1", Prompt: "x"}}}
	if err := validateWorkflow(w); err == nil {
		t.Error("expected error for missing agent")
	}
}

func TestValidateWorkflow_MissingPrompt(t *testing.T) {
	w := Pipeline{ID: "test", Stages: []PipelineStage{{ID: "s1", Agent: "coder"}}}
	if err := validateWorkflow(w); err == nil {
		t.Error("expected error for missing prompt")
	}
}

func TestValidateWorkflow_Valid(t *testing.T) {
	w := Pipeline{ID: "test", Stages: []PipelineStage{{ID: "s1", Agent: "coder", Prompt: "x"}}}
	if err := validateWorkflow(w); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolvePrompt_Inline(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	stage := PipelineStage{Prompt: "hello world"}

	got, err := ResolvePrompt(stage, pr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestResolvePrompt_Registry(t *testing.T) {
	fs := fstest.MapFS{
		"test.md": {Data: []byte("from registry")},
	}
	pr := prompts.NewRegistry(fs, "", "")
	stage := PipelineStage{Prompt: "prompts://test"}

	got, err := ResolvePrompt(stage, pr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from registry" {
		t.Errorf("expected 'from registry', got %q", got)
	}
}

func TestResolvePrompt_RegistryMissing(t *testing.T) {
	fs := fstest.MapFS{}
	pr := prompts.NewRegistry(fs, "", "")
	stage := PipelineStage{Prompt: "prompts://missing"}

	_, err := ResolvePrompt(stage, pr)
	if err == nil {
		t.Error("expected error for missing prompt ref")
	}
}
