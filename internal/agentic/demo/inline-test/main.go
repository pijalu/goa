//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
)

type inlineSkillTool struct{}

func (inlineSkillTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "run_skill",
		Description: "Execute a skill. Available skills: math-expert",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"skill_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the skill to execute",
					"enum":        []string{"math-expert"},
				},
				"task": map[string]interface{}{
					"type":        "string",
					"description": "Task to execute",
				},
			},
			"required": []string{"skill_name", "task"},
		},
	}
}

func (inlineSkillTool) Execute(input string) (string, error) {
	var params struct {
		SkillName string `json:"skill_name"`
		Task      string `json:"task"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", err
	}

	instructions := `# Math Expert Skill

You are an expert mathematician. When given a math problem:
1. Break it down into steps
2. Explain your reasoning clearly
3. Use the calculator tool for any arithmetic
4. Verify your answer

You must ALWAYS use the calculator tool for arithmetic — do not calculate in your head.
`

	return fmt.Sprintf("%s\n## Task\n%s\n\nFollow the skill instructions above and complete the task using available tools.", instructions, params.Task), nil
}

func (inlineSkillTool) IsRetryable(err error) bool { return false }

type calculatorTool struct{}

func (calculatorTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "calculator",
		Description: "Evaluate a mathematical expression. Supports +, -, *, /, parentheses, and decimal numbers.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"expression": map[string]interface{}{
					"type":        "string",
					"description": "Math expression to evaluate, e.g. '(23 + 47) * 2'",
				},
			},
			"required": []string{"expression"},
		},
	}
}

func (calculatorTool) Execute(input string) (string, error) {
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", err
	}
	result, err := evaluate(params.Expression)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Result: %v", result), nil
}

func (calculatorTool) IsRetryable(err error) bool { return false }

type parser struct {
	s string
	i int
}

func (p *parser) skipSpaces() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) parseExpr() (float64, error) {
	return p.parseAddSub()
}

func (p *parser) parseAddSub() (float64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.i >= len(p.s) {
			break
		}
		op := p.s[p.i]
		if op != '+' && op != '-' {
			break
		}
		p.i++
		right, err := p.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *parser) parseMulDiv() (float64, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.i >= len(p.s) {
			break
		}
		op := p.s[p.i]
		if op != '*' && op != '/' {
			break
		}
		p.i++
		right, err := p.parsePrimary()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
	}
	return left, nil
}

func (p *parser) parsePrimary() (float64, error) {
	p.skipSpaces()
	if p.i >= len(p.s) {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	if p.s[p.i] == '(' {
		p.i++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.i++
		return val, nil
	}
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.') {
		p.i++
	}
	if start == p.i {
		return 0, fmt.Errorf("expected number at position %d", p.i)
	}
	var val float64
	_, err := fmt.Sscanf(p.s[start:p.i], "%f", &val)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", p.s[start:p.i])
	}
	return val, nil
}

func evaluate(expr string) (float64, error) {
	p := &parser{s: expr, i: 0}
	return p.parseExpr()
}

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"local-model",
	)

	agentCfg := cfg.ToAgentConfig()
	agentCfg.Logger = agentic.NewLogger(agentic.Debug)
	agentCfg.SystemPrompt = "You are a helpful assistant with access to tools."
	agentCfg.Tools = []agentic.Tool{inlineSkillTool{}, calculatorTool{}}

	agent := agentic.NewAgent(agentCfg)

	agent.AddObserver(agentic.OutputObserverFunc(func(event agentic.OutputEvent) {
		switch event.Type {
		case agentic.EventContent:
			fmt.Printf("[ASSISTANT]: %s\n", event.Text)
		case agentic.EventToolCall:
			fmt.Printf("[TOOL CALL]: %s(%s)\n", event.ToolName, event.ToolInput)
		case agentic.EventToolResult:
			fmt.Printf("[TOOL RESULT]: %s\n", event.Text)
		case agentic.EventEnd:
			fmt.Println("[END]")
		}
	}))

	ctx := context.Background()

	fmt.Println("=== TEST: Inline Skill Execution ===")
	fmt.Println("Asking the agent to use run_skill with math-expert...")
	fmt.Println()

	err := agent.Run(ctx, "Use the math-expert skill to solve: what is (12345 * 6789 + 54321) / 100? You must show your work and use the calculator for each step.")
	if err != nil {
		log.Fatalf("Agent error: %v", err)
	}
}
