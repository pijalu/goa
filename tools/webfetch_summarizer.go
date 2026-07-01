// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// AgentRunner is the subset of agentic.Agent needed for summarization.
type AgentRunner interface {
	Run(ctx context.Context, input string) error
	GetHistory() []agentic.Message
}

// AgentPool is the subset of multiagent.AgentPool needed by WebSummarizer.
type AgentPool interface {
	GetOrCreate(role string) (AgentRunner, error)
}

// WebSummarizer delegates cached Markdown to a sub-agent for summarization.
type WebSummarizer struct {
	Pool          AgentPool
	Role          string
	DefaultPrompt string
	MaxInputLines int
}

// Summarize runs the sub-agent over the given content.
func (s *WebSummarizer) Summarize(ctx context.Context, url, content, prompt string) (string, error) {
	if s.Pool == nil {
		return "", fmt.Errorf("agent pool not configured")
	}
	if s.Role == "" {
		s.Role = "companion"
	}

	agent, err := s.Pool.GetOrCreate(s.Role)
	if err != nil {
		return "", fmt.Errorf("create sub-agent: %w", err)
	}

	input := s.buildPrompt(content, prompt)
	if err := agent.Run(ctx, input); err != nil {
		return "", fmt.Errorf("sub-agent run: %w", err)
	}

	return collectAgentOutput(agent), nil
}

func (s *WebSummarizer) buildPrompt(content, prompt string) string {
	if s.MaxInputLines > 0 {
		lines := splitLines(content)
		if len(lines) > s.MaxInputLines {
			content = strings.Join(lines[:s.MaxInputLines], "\n") + "\n..."
		}
	}

	var b strings.Builder
	if s.DefaultPrompt != "" {
		b.WriteString(s.DefaultPrompt)
		b.WriteString("\n\n")
	}
	if prompt != "" {
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	b.WriteString("URL: ")
	b.WriteString(content)
	b.WriteString("\n\n")
	b.WriteString(content)
	return b.String()
}

func collectAgentOutput(agent AgentRunner) string {
	history := agent.GetHistory()
	if len(history) == 0 {
		return ""
	}
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role == agentic.Assistant && msg.Content != "" {
			return msg.Content
		}
	}
	return ""
}
