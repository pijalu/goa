//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"time"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
	"github.com/pijalu/goa/internal/agentic/helper"
	"github.com/pijalu/goa/internal/agentic/skillrunner"
)

//go:embed skills/*
//go:embed skills/*/*
var skillsFS embed.FS

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"google/gemma-4-e4b",
	)

	agentCfg := cfg.ToAgentConfig()
	agentCfg.Logger = agentic.NewLogger(agentic.Warn)

	compressionCfg := cfg.ToCompression()
	fmt.Fprintf(os.Stderr, "[config] Skill mode: %s, Compression: %s, MaxTokens: %d\n",
		cfg.SkillMode, cfg.Compression, compressionCfg.MaxTokens)

	// Create skill runner - loads embedded skills
	loader := skillrunner.NewEmbeddedSkillsLoader(skillsFS, "skills")
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:              loader,
		Model:               cfg.ToModel(),
		WorkDir:             ".",
		Logger:              agentic.NewLogger(agentic.Warn),
		ContextCompression:  compressionCfg,
		ReasoningEffort:     agentic.ReasoningEffort(cfg.ReasoningEffort),
		ToolResultAsUser:    agentCfg.ToolResultAsUser,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Build system prompt with skills section
	systemPrompt := "You are a helpful assistant with skills.\n" + runner.GenerateSkillsSection()
	agentCfg.SystemPrompt = systemPrompt
	agentCfg.Tools = []agentic.Tool{runner}

	// Create agent
	agent := agentic.NewAgent(agentCfg)

	// Add observers
	agent.AddObserver(helper.NewConsoleObserver())
	logObserver := helper.NewMessageLogObserver()
	agent.AddObserver(logObserver)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Run task: search Wikipedia for Chuck Norris
	task := "Use the wiki-search skill to search Wikipedia for 'chuck norris' and return the list of article URLs"
	fmt.Printf("Running task: %s\n", task)
	if err := agent.Run(ctx, task); err != nil {
		log.Fatal(err)
	}

	// Get structured log
	jsonData, _ := logObserver.JSON()
	fmt.Println(string(jsonData))

	agent.Stop()
}
