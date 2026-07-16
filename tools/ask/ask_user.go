// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package ask implements the ask_user_question tool, which lets the LLM ask
// the user one or more clarifying questions when requirements are ambiguous.
//
// Each question is rendered as a ClarifyCard in the conversation viewport
// (title / summary / question / numbered options) and answered through the
// MAIN input line — the card itself never captures input (see the "Input
// discipline" guideline in docs/TUI.md). The tool is registered by default
// and can be disabled with tools.enabled.clarify_disabled in config.
package ask

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/common"
)

// maxOptions caps the number of options per question to keep the card readable.
const maxOptions = 6

// ClarifyFunc is the host callback that displays a question and blocks until
// the user answers on the main input line. ok==false means the user cancelled.
// step/total give the question's 1-based position within a multi-question batch
// (total<=1 for a standalone question) so the host can show compact progress.
// It is intentionally decoupled from the TUI package to avoid an import cycle.
type ClarifyFunc func(title, summary, question string, options []string, step, total int) (answer string, ok bool)

// AskUserQuestionTool asks the user one or more clarifying questions and
// returns the aggregated answers as a tool result.
type AskUserQuestionTool struct {
	agentic.BaseTool
	// Clarify handles question delivery and blocks until each answer arrives.
	// Injected by the host at registration time.
	Clarify ClarifyFunc

	mu sync.Mutex
}

type clarifyQuestion struct {
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	Question      string   `json:"question"`
	Options       []string `json:"options,omitempty"`
	Required      bool     `json:"required,omitempty"`
	AllowFreeText bool     `json:"allow_free_text,omitempty"`
}

type clarifyInput struct {
	Questions []clarifyQuestion `json:"questions"`
}

type clarifyAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// Schema returns the tool schema. The description tells the model this is the
// tool to reach for when it needs clarification before proceeding.
func (t *AskUserQuestionTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name: "ask_user_question",
		Description: "Ask the user for clarification.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"description": "clarifying questions, each asked separately",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"summary":  map[string]any{"type": "string", "description": "Optional context explaining why the question is asked."},
							"question": map[string]any{"type": "string", "description": "The question itself (required)."},
							"options": map[string]any{
								"type":        "array",
								"description": "answer choices (max 6)",
								"items":       map[string]any{"type": "string"},
							},
							"required":         map[string]any{"type": "boolean", "description": "If true, cancellation is an error. Default false."},
							"allow_free_text":  map[string]any{"type": "boolean", "description": "If false with options, restrict to listed options. Default true."},
						},
						"required": []string{"question"},
					},
				},
			},
			"required": []string{"questions"},
		},
	}
}

// Execute asks each question separately via the host Clarify callback and
// returns the aggregated answers as JSON. It blocks per question (the agent
// tool-execution loop expects blocking tools).
func (t *AskUserQuestionTool) Execute(input string) (string, error) {
	return t.run(context.Background(), input)
}

// ExecuteContext implements agentic.ContextTool so the agent's turn context
// (user Escape / interrupt) propagates into the blocking wait for an answer.
// Without it, the tool ignores cancellation: it stays parked on the clarify
// channel while the agent turn is torn down, and the user sees a bare
// "Error: context canceled" with no card and no recoverable result. On
// cancellation it returns a graceful skipped-answers result instead.
func (t *AskUserQuestionTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := t.run(ctx, input)
		done <- outcome{out, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		return r.out, r.err
	}
}

// run parses the input and asks each question in order, honoring ctx between
// questions so a cancelled batch stops promptly.
func (t *AskUserQuestionTool) run(ctx context.Context, input string) (string, error) {
	var p clarifyInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "ask_user_question", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide valid JSON with a questions array.",
		}
	}
	if len(p.Questions) == 0 {
		return "", &internal.ToolError{
			Tool: "ask_user_question", Type: "missing_questions",
			Detail:   "at least one question is required",
			HintText: "Provide a non-empty questions array.",
		}
	}

	t.mu.Lock()
	clarify := t.Clarify
	t.mu.Unlock()

	// Force a fixed title so the model cannot supply arbitrary labels.
	const fixedTitle = "Clarifications needed"

	total := len(p.Questions)
	answers := make([]clarifyAnswer, 0, total)
	for i, q := range p.Questions {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		q.Title = fixedTitle
		ans, err := t.askOne(clarify, q, i+1, total)
		if err != nil {
			return "", err
		}
		answers = append(answers, ans)
	}

	payload, _ := json.Marshal(answers)
	return fmt.Sprintf("[ask_user_question] %d question(s) answered:\n%s", len(answers), string(payload)), nil
}

// askOne poses a single question through the host callback, canonicalizing
// option answers and honoring the required flag. step/total are forwarded so
// the host can render compact progress for a multi-question batch.
func (t *AskUserQuestionTool) askOne(clarify ClarifyFunc, q clarifyQuestion, step, total int) (clarifyAnswer, error) {
	question := strings.TrimSpace(q.Question)
	if question == "" {
		return clarifyAnswer{}, &internal.ToolError{
			Tool: "ask_user_question", Type: "missing_question",
			Detail:   "each question requires a non-empty 'question' field",
			HintText: "Provide a question string.",
		}
	}
	// Cap options and trim.
	opts := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		o = strings.TrimSpace(o)
		if o != "" {
			opts = append(opts, o)
		}
		if len(opts) >= maxOptions {
			break
		}
	}

	if clarify == nil {
		return clarifyAnswer{}, &internal.ToolError{
			Tool: "ask_user_question", Type: "no_host",
			Detail:   "no clarify handler configured (running headless?)",
			HintText: "This tool requires an interactive TUI host.",
		}
	}

	raw, ok := clarify(q.Title, q.Summary, question, opts, step, total)
	if !ok || strings.TrimSpace(raw) == "" {
		if q.Required {
			return clarifyAnswer{}, &internal.ToolError{
				Tool: "ask_user_question", Type: "cancelled",
				Detail:   fmt.Sprintf("required question cancelled by user: %s", question),
				HintText: "The user cancelled; ask again or proceed without the answer.",
			}
		}
		return clarifyAnswer{Question: question, Answer: "", Skipped: true}, nil
	}

	answer := canonicalizeAnswer(raw, opts, q.AllowFreeText)
	return clarifyAnswer{Question: question, Answer: answer}, nil
}

// canonicalizeAnswer maps a numeric option choice to its label, and (when
// allowFreeText is false and options exist) restricts the answer to a listed
// option — otherwise returning the closest option or the raw text.
func canonicalizeAnswer(raw string, opts []string, allowFreeText bool) string {
	raw = strings.TrimSpace(raw)
	if len(opts) == 0 {
		return raw
	}
	// Numeric choice: "1" -> opts[0].
	if n, err := parseInt(raw); err == nil && n >= 1 && n <= len(opts) {
		return opts[n-1]
	}
	// Exact (case-insensitive) match against an option.
	lower := strings.ToLower(raw)
	for _, o := range opts {
		if strings.ToLower(o) == lower {
			return o
		}
	}
	if !allowFreeText {
		// Restrict: pick the closest option, else the first.
		return closestOption(raw, opts)
	}
	return raw
}

func parseInt(s string) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not an int")
		}
		n = n*10 + int(r-'0')
		if n > 1e6 {
			return 0, fmt.Errorf("too large")
		}
	}
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	return n, nil
}

// closestOption returns the option sharing the longest common prefix with raw,
// falling back to the first option.
func closestOption(raw string, opts []string) string {
	best := opts[0]
	bestScore := 0
	lr := strings.ToLower(raw)
	for _, o := range opts {
		lo := strings.ToLower(o)
		score := commonPrefixLen(lr, lo)
		if score > bestScore {
			bestScore = score
			best = o
		}
	}
	return best
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// IsRetryable returns false.
func (t *AskUserQuestionTool) IsRetryable(err error) bool { return false }

// SetClarify injects the host callback. Thread-safe so it can be attached
// after registration from the App layer.
func (t *AskUserQuestionTool) SetClarify(f ClarifyFunc) {
	t.mu.Lock()
	t.Clarify = f
	t.mu.Unlock()
}

//go:embed ask_user.short.md ask_user.long.md
var askUserDocs embed.FS

// ShortDoc returns a short doc string.
func (t *AskUserQuestionTool) ShortDoc() string {
	return common.ReadDoc(askUserDocs, "ask_user.short.md")
}

// LongDoc returns a detailed doc string.
func (t *AskUserQuestionTool) LongDoc() string {
	return common.ReadDoc(askUserDocs, "ask_user.long.md")
}

// Examples returns example invocations.
func (t *AskUserQuestionTool) Examples() []string {
	return []string{
		`{"questions":[{"title":"Target branch","summary":"Two release branches are active","question":"Which branch should I target?","options":["main","release-2.x"]}]}`,
	}
}
