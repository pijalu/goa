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

	compressionCfg := cfg.ToCompression()
	fmt.Fprintf(os.Stderr, "[config] Skill mode: %s, Compression: %s, MaxTokens: %d\n",
		cfg.SkillMode, cfg.Compression, compressionCfg.MaxTokens)

	agent, err := skillrunner.NewAgentWithSkillsLoader(
		agentic.Config{
			Model:              cfg.ToModel(),
			APIKey:             cfg.APIKey,
			SystemPrompt:       "You are a helpful assistant with skills.",
			Logger:             agentic.NewLogger(agentic.Warn),
			SkillExecutionMode: cfg.ToSkillMode(),
			ContextCompression: compressionCfg,
			ReasoningEffort:    agentic.ReasoningEffort(cfg.ReasoningEffort),
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	task := "Use the file-resumer skill to summarize the content of README.md"
	fmt.Printf("Running task: %s\n", task)
	if err := agent.Run(ctx, task); err != nil {
		log.Fatal(err)
	}

	jsonData, _ := logObserver.JSON()
	fmt.Println(string(jsonData))

	agent.Stop()
}
