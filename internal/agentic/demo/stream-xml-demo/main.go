//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"context"
	"embed"
	"fmt"
	"os"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
	"github.com/pijalu/goa/internal/agentic/observer/xmlstream"
	"github.com/pijalu/goa/internal/agentic/skillrunner"
)

//go:embed skills/*
//go:embed skills/*/*
var skillsFS embed.FS

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"local-model",
	)

	log := agentic.NewLogger(agentic.Info)

	// Create XML streaming observer
	obs, err := xmlstream.NewXMLStreamingObserver(xmlstream.Config{
		Writer:         xmlstream.NewConsoleWriter(os.Stdout),
		Model:          cfg.Model,
		ConversationID: "stream-xml-demo",
		IncludeTimings: true,
	})
	if err != nil {
		log.Log(agentic.Error, "create XML observer: %v", err)
		return
	}

	compressionCfg := cfg.ToCompression()
	fmt.Fprintf(os.Stderr, "[config] Skill mode: %s, Compression: %s, MaxTokens: %d\n",
		cfg.SkillMode, cfg.Compression, compressionCfg.MaxTokens)

	// Create skills loader with embedded skills
	loader := skillrunner.NewEmbeddedSkillsLoader(skillsFS, "skills")

	// Create runner with shared observer for sub-agent event forwarding
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:              loader,
		Model:               cfg.ToModel(),
		WorkDir:             ".",
		Logger:              log,
		Observer:            obs,
		ContextCompression:  compressionCfg,
		ReasoningEffort:     agentic.ReasoningEffort(cfg.ReasoningEffort),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create runner: %v\n", err)
		return
	}

	// Create agent with the skill runner
	agentCfg := cfg.ToAgentConfig()
	agentCfg.SystemPrompt = "You are a helpful assistant with skills.\n" + runner.GenerateSkillsSection()
	agentCfg.Tools = []agentic.Tool{runner}
	agentCfg.Logger = log

	agent := agentic.NewAgent(agentCfg)

	// Add the XML streaming observer to the main agent
	agent.AddObserver(obs)

	// Run a simple conversation
	agent.Run(context.Background(), "Hello, what can you do?")

	fmt.Println()

	// Run with metadata — tags are visible in the XML stream
	// but are NOT sent to the LLM.
	agent.RunWithMetadata(context.Background(), "What time is it?", map[string]string{
		"category": "demo",
		"internal": "true",
	})

	// Flush and close the XML stream
	obs.Flush()

	agent.Stop()
}
