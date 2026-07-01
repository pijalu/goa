//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"context"
	"fmt"
	"os"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
	"github.com/pijalu/goa/internal/agentic/skillrunner"
)

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"local-model",
	)

	compressionCfg := cfg.ToCompression()
	fmt.Fprintf(os.Stderr, "[config] Skill mode: %s, Compression: %s, MaxTokens: %d\n",
		cfg.SkillMode, cfg.Compression, compressionCfg.MaxTokens)

	agentCfg := cfg.ToAgentConfig()
	agentCfg.Logger = agentic.NewLogger(agentic.Debug)

	sampleFile, cleanup := createSampleFile()
	defer cleanup()

	agent, err := createDemoAgent(cfg, compressionCfg, agentCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}
	agent.AddObserver(agentic.OutputObserverFunc(printEvent))

	ctx := context.Background()
	printDemoHeader(sampleFile)

	runDemoTurn(ctx, agent, "Use the code-reviewer skill to review sample.go for bugs.", "Turn 1: Review sample.go for bugs")
	printContextStats(agent, "After turn 1")
	runDemoTurn(ctx, agent, "What was the most critical issue found?", "Turn 2: Ask a follow-up about the review")
	runDemoTurn(ctx, agent, "Use the code-reviewer skill to review sample.go for style issues.", "Turn 3: Review again with different focus")
	printContextStats(agent, "Final")
	fmt.Println("=== Demo complete ===")
}

func createSampleFile() (string, func()) {
	sampleFile := "sample.go"
	if err := os.WriteFile(sampleFile, []byte(sampleGoCode), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write sample file: %v\n", err)
		os.Exit(1)
	}
	return sampleFile, func() { os.Remove(sampleFile) }
}

func createDemoAgent(cfg shared.Config, compressionCfg agentic.ContextCompressionConfig, agentCfg agentic.Config) (*agentic.Agent, error) {
	return skillrunner.NewAgentWithSkills(
		agentic.Config{
			Model:              cfg.ToModel(),
			APIKey:             cfg.APIKey,
			SystemPrompt:       "You are a helpful assistant with access to tools and skills.",
			SkillExecutionMode: cfg.ToSkillMode(),
			Logger:             agentic.NewLogger(agentic.Info),
			ContextCompression: compressionCfg,
			ReasoningEffort:    agentic.ReasoningEffort(cfg.ReasoningEffort),
			ToolResultAsUser:   agentCfg.ToolResultAsUser,
		},
		[]string{"./skills"},
		".",
	)
}

func printEvent(event agentic.OutputEvent) {
	switch event.Type {
	case agentic.EventContextStats:
		if event.ContextStats != nil {
			fmt.Printf("\n[CONTEXT] Usage: %d%% (%d / %d tokens, %d messages)\n",
				event.ContextStats.UsagePercent,
				event.ContextStats.EstimatedTokens,
				event.ContextStats.MaxTokens,
				event.ContextStats.Messages,
			)
		}
	case agentic.EventContent:
		if event.Role == agentic.Assistant && event.State == agentic.StateContent {
			fmt.Print(event.Text)
		}
	case agentic.EventToolCall:
		fmt.Printf("\n[TOOL CALL] %s(%s)\n", event.ToolName, event.ToolInput)
	case agentic.EventToolResult:
		text := event.Text
		if len(text) > 300 {
			text = text[:300] + "... [truncated]"
		}
		fmt.Printf("\n[TOOL RESULT] %s\n", text)
	case agentic.EventCompact:
		fmt.Println("\n[COMPRESSION] Conversation compacted")
	}
}

func printDemoHeader(sampleFile string) {
	fmt.Println("=== Inline Skill Execution Demo ===")
	fmt.Println()
	fmt.Println("This demo shows:")
	fmt.Println("1. Inline mode: skill instructions returned as tool result")
	fmt.Println("2. LLM follows instructions and uses tools in the same session")
	fmt.Println("3. Context compression triggers when usage exceeds threshold")
	fmt.Println()
	fmt.Println("Sample file created:", sampleFile)
	fmt.Println()
}

func runDemoTurn(ctx context.Context, agent *agentic.Agent, prompt, label string) {
	fmt.Printf("--- %s ---\n", label)
	fmt.Println()
	if err := agent.Run(ctx, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "%s error: %v\n", label, err)
		os.Exit(1)
	}
	fmt.Println()
}

func printContextStats(agent *agentic.Agent, label string) {
	stats := agent.ContextStats()
	fmt.Printf("[CONTEXT] %s: %d%% (%d / %d tokens, %d messages)\n",
		label, stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens, stats.Messages)
	fmt.Println()
}

const sampleGoCode = `package main

import (
	"fmt"
)

// calculate computes a value but has issues
func calculate(a int, b int) int {
	result := a + b
	if a == 0 {
		return 0 // unnecessary check
	}
	fmt.Println("debug:", result) // debug print left in production
	return result
}

func main() {
	x := 10
	y := 20
	z := calculate(x, y)
	fmt.Println(z)
}
`
