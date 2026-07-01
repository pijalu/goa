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
	"time"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
	"github.com/pijalu/goa/internal/agentic/helper"
	"github.com/pijalu/goa/internal/agentic/skillrunner"
)

//go:embed skills/*
//go:embed skills/*/*
//go:embed skills/*/*/*
var skillsFS embed.FS

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"google/gemma-4-e4b",
	)

	agentCfg := cfg.ToAgentConfig()
	agentCfg.Logger = agentic.NewLogger(agentic.Warn)

	agent, err := skillrunner.NewAgentWithSkillsLoader(
		agentic.Config{
			Model:              cfg.ToModel(),
			APIKey:             cfg.APIKey,
			SystemPrompt:       "You are a helpful sentence generator.",
			Logger:             agentic.NewLogger(agentic.Warn),
			ReasoningEffort:    agentic.ReasoningEffort(cfg.ReasoningEffort),
			SkillExecutionMode: cfg.ToSkillMode(),
			ContextCompression: cfg.ToCompression(),
			ToolResultAsUser:   agentCfg.ToolResultAsUser,
		},
		skillrunner.NewEmbeddedSkillsLoader(skillsFS, "skills"),
		".",
	)
	if err != nil {
		log.Fatal(err)
	}

	agent.AddObserver(helper.NewConsoleObserver())
	logObserver := helper.NewMessageLogObserver()
	agent.AddObserver(logObserver)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	task := "Use the genSentence skill to generate a sentence and show the results"
	fmt.Printf("Running task: %s\n", task)
	if err := agent.Run(ctx, task); err != nil {
		log.Fatal(err)
	}

	agent.Stop()
}
