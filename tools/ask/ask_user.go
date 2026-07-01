// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ask

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/common"
)

// AskUserQuestionTool asks the user a multiple-choice or free-form question
// and returns the answer as a tool result.
type AskUserQuestionTool struct {
	agentic.BaseTool
	// Ask handles the actual question delivery. The implementation may block
	// until the user answers.
	Ask func(question string, options []string) (string, error)

	mu sync.Mutex
}

type askUserInput struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// Schema returns the tool schema.
func (t *AskUserQuestionTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "ask_user_question",
		Description: "Ask the user a question and return their answer. Use 1-4 concise options when possible.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "Question to ask the user.",
				},
				"options": map[string]any{
					"type":        "array",
					"description": "Optional answer options (1-4 items).",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"question"},
		},
	}
}

// Execute asks the user and returns the answer.
func (t *AskUserQuestionTool) Execute(input string) (string, error) {
	var p askUserInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "ask_user_question", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide valid JSON with a question field.",
		}
	}
	if strings.TrimSpace(p.Question) == "" {
		return "", &internal.ToolError{Tool: "ask_user_question", Type: "missing_question", Detail: "question is required", HintText: "Provide a question to ask the user."}
	}
	t.mu.Lock()
	ask := t.Ask
	if ask == nil {
		ask = t.defaultAsk
	}
	t.mu.Unlock()

	answer, err := ask(p.Question, p.Options)
	if err != nil {
		return "", &internal.ToolError{Tool: "ask_user_question", Type: "ask_failed", Detail: err.Error(), HintText: "Retry the question or provide a default answer."}
	}
	return fmt.Sprintf("[ask_user_question] %s\nAnswer: %s", p.Question, answer), nil
}

func (t *AskUserQuestionTool) defaultAsk(question string, options []string) (string, error) {
	return "", fmt.Errorf("no question handler configured")
}

// IsRetryable returns false.
func (t *AskUserQuestionTool) IsRetryable(err error) bool { return false }

// ShortDoc returns a short doc string.
//
//go:embed ask_user.short.md ask_user.long.md
var ask_userDocs embed.FS

func (t *AskUserQuestionTool) ShortDoc() string {
	return common.ReadDoc(ask_userDocs, "ask_user.short.md")
}
func (t *AskUserQuestionTool) LongDoc() string {
	return common.ReadDoc(ask_userDocs, "ask_user.long.md")
}
func (t *AskUserQuestionTool) Examples() []string {
	return []string{
		`{"question": "Should I proceed?", "options": ["yes", "no"]}`,
	}
}
