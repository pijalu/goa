// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package prompts provides access to Goa's embedded prompt files.
// All prompts/*.md files are embedded at build time and used as system
// prompts for agent modes and pipeline stages.
package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/pijalu/goa/internal/embeddoc"
)

//go:embed *.md mode/*/*.md pair/*.md task/*.md pipeline/*.md tools/*.md orchestrate/*.md
var embeddedFS embed.FS

// LoadPipelinePrompt returns the stage prompt for a pipeline.
func LoadPipelinePrompt(pipelineID, stageID string) (string, error) {
	path := filepath.Join("pipelines", pipelineID, stageID+".md")
	data, err := embeddedFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("pipeline prompt %s/%s not found: %w", pipelineID, stageID, err)
	}
	return string(data), nil
}

// LoadTelegramPrompt returns the telegram talk style prompt.
func LoadTelegramPrompt() (string, error) {
	data, err := embeddedFS.ReadFile("telegram.md")
	if err != nil {
		return "", fmt.Errorf("telegram prompt not found: %w", err)
	}
	return string(data), nil
}

// LoadAgentDrivenPrompt returns the system prompt additions for agent-driven
// multi-agent collaboration (request_review, delegate_to tools).
func LoadAgentDrivenPrompt() (string, error) {
	data, err := embeddedFS.ReadFile("agent_driven.md")
	if err != nil {
		return "", fmt.Errorf("agent driven prompt not found: %w", err)
	}
	return string(data), nil
}

// LoadOrchestratePrompt returns an orchestrator prompt template by name.
func LoadOrchestratePrompt(name string) (string, error) {
	path := filepath.Join("orchestrate", name+".md")
	data, err := embeddedFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("orchestrate prompt %s not found: %w", name, err)
	}
	return string(data), nil
}

// LoadCompanionReviewEnabledPrompt returns the system prompt addition used
// when companion review is enabled mid-conversation.
func LoadCompanionReviewEnabledPrompt() (string, error) {
	data, err := embeddedFS.ReadFile("companion_review_enabled.md")
	if err != nil {
		return "", fmt.Errorf("companion review enabled prompt not found: %w", err)
	}
	doc, err := embeddoc.ParseDocument(data)
	if err != nil {
		return "", fmt.Errorf("companion review enabled prompt invalid: %w", err)
	}
	return doc.Body, nil
}

// LoadCompanionReviewDisabledPrompt returns the system prompt addition used
// when companion review is disabled mid-conversation.
func LoadCompanionReviewDisabledPrompt() (string, error) {
	data, err := embeddedFS.ReadFile("companion_review_disabled.md")
	if err != nil {
		return "", fmt.Errorf("companion review disabled prompt not found: %w", err)
	}
	doc, err := embeddoc.ParseDocument(data)
	if err != nil {
		return "", fmt.Errorf("companion review disabled prompt invalid: %w", err)
	}
	return doc.Body, nil
}

// EmbeddedFS returns the embedded filesystem for use by Registry.
func EmbeddedFS() fs.FS { return embeddedFS }
