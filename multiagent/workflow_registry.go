// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/prompts"
	"gopkg.in/yaml.v3"
)

// WorkflowRegistry loads and stores workflows (pipelines).
type WorkflowRegistry struct {
	workflows map[string]Pipeline
	promptReg *prompts.Registry
}

// NewWorkflowRegistry creates a workflow registry.
func NewWorkflowRegistry(promptReg *prompts.Registry) *WorkflowRegistry {
	return &WorkflowRegistry{
		workflows: make(map[string]Pipeline),
		promptReg: promptReg,
	}
}

// Register adds a workflow to the registry.
func (wr *WorkflowRegistry) Register(w Pipeline) {
	wr.workflows[w.ID] = w
}

// Get returns a workflow by ID.
func (wr *WorkflowRegistry) Get(id string) (Pipeline, bool) {
	w, ok := wr.workflows[id]
	return w, ok
}

// All returns all registered workflows.
func (wr *WorkflowRegistry) All() []Pipeline {
	result := make([]Pipeline, 0, len(wr.workflows))
	for _, w := range wr.workflows {
		result = append(result, w)
	}
	return result
}

// LoadDir loads all workflow YAML files from a directory.
func (wr *WorkflowRegistry) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read workflow %s: %w", path, err)
		}
		var w Pipeline
		if err := yaml.Unmarshal(data, &w); err != nil {
			return fmt.Errorf("parse workflow %s: %w", path, err)
		}
		if w.ID == "" {
			w.ID = strings.TrimSuffix(entry.Name(), ".yaml")
		}
		if err := validateWorkflow(w); err != nil {
			return fmt.Errorf("invalid workflow %s: %w", path, err)
		}
		wr.Register(w)
	}
	return nil
}

// LoadWorkflowTree scans a directory recursively for subdirectories containing
// definition.yaml files and loads each as a workflow.
func (wr *WorkflowRegistry) LoadWorkflowTree(rootDir string) error {
	return wr.loadWorkflowTreeRecursive(rootDir, rootDir)
}

func (wr *WorkflowRegistry) loadWorkflowTreeRecursive(rootDir, currentDir string) error {
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(currentDir, entry.Name())
		if err := wr.tryLoadWorkflow(subDir, entry.Name()); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			if err := wr.loadWorkflowTreeRecursive(rootDir, subDir); err != nil {
				return err
			}
		}
	}
	return nil
}

func (wr *WorkflowRegistry) tryLoadWorkflow(dir, defaultID string) error {
	defPath := filepath.Join(dir, "definition.yaml")
	data, err := os.ReadFile(defPath)
	if err != nil {
		return err
	}
	var w Pipeline
	if err := yaml.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("parse workflow %s: %w", defPath, err)
	}
	if w.ID == "" {
		w.ID = defaultID
	}
	w.Dir = dir
	if err := validateWorkflow(w); err != nil {
		return fmt.Errorf("invalid workflow %s: %w", defPath, err)
	}
	wr.Register(w)
	return nil
}

// ResolvePromptWithDir resolves a stage prompt, trying relative path against
// the workflow directory first, then falling back to prompts:// URIs.
// If workflowDir is empty, only inline text and prompts:// URIs are resolved.
func ResolvePromptWithDir(stage PipelineStage, reg *prompts.Registry, workflowDir string) (string, error) {
	prompt := stage.Prompt
	if prompt == "" {
		return "", fmt.Errorf("stage %q has no prompt", stage.ID)
	}

	// Check if it's a prompts:// URI
	if strings.HasPrefix(prompt, "prompts://") {
		if reg == nil {
			return "", fmt.Errorf("prompt registry not available for %q", prompt)
		}
		name := strings.TrimPrefix(prompt, "prompts://")
		return reg.Load(name)
	}

	// Try relative path against workflow directory
	if workflowDir != "" && !strings.HasPrefix(prompt, "/") {
		relPath := filepath.Join(workflowDir, prompt)
		if data, err := os.ReadFile(relPath); err == nil {
			return string(data), nil
		}
	}

	// If it contains no path separators, try prompt registry as fallback
	if reg != nil && !strings.ContainsAny(prompt, "/\\") {
		if content, err := reg.Load(prompt); err == nil {
			return content, nil
		}
	}

	// Return as inline text
	return prompt, nil
}

// validateWorkflow checks a pipeline for structural correctness as a workflow.
func validateWorkflow(w Pipeline) error {
	if w.ID == "" {
		return fmt.Errorf("workflow missing id")
	}
	if len(w.Stages) == 0 {
		return fmt.Errorf("workflow %q has no stages", w.ID)
	}
	for i, s := range w.Stages {
		if s.ID == "" {
			return fmt.Errorf("workflow %q stage %d missing id", w.ID, i)
		}
		if s.Agent == "" {
			return fmt.Errorf("workflow %q stage %q missing agent", w.ID, s.ID)
		}
		if s.Prompt == "" {
			return fmt.Errorf("workflow %q stage %q missing prompt", w.ID, s.ID)
		}
	}
	return nil
}

// ResolvePrompt extracts the prompt text for a stage.
// If the pipeline has a Dir set, it uses ResolvePromptWithDir for relative path resolution.
// Otherwise it falls back to prompts:// URI resolution or inline text.
func ResolvePrompt(stage PipelineStage, reg *prompts.Registry) (string, error) {
	return ResolvePromptWithDir(stage, reg, "")
}
