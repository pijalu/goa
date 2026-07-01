//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
	"github.com/pijalu/goa/internal/agentic/skillrunner/tools"
)

//go:embed skills/*/SKILL.md
var skillsFS embed.FS

// compressTool implements agentic.Tool for direct compression
type compressTool struct {
	model           providerModel
	reasoningEffort agentic.ReasoningEffort
}

// providerModel is the minimal interface we need from the model.
// In practice, the tool creates a sub-agent using cfg.ToAgentConfig().
type providerModel interface {
	GetModel() agentic.Config
}

func (t *compressTool) IsRetryable(err error) bool { return false }

func (t *compressTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "compress",
		Description: "Compress text into minimal token representation.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{
					"type":        "string",
					"description": "Text to compress",
				},
			},
			"required": []string{"text"},
		},
	}
}

func (t *compressTool) Execute(input string) (string, error) {
	var params struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}

	if params.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	// Read skill content
	skillContent, err := readSkillFromEmbed(skillsFS)
	if err != nil {
		return "", fmt.Errorf("read skill: %w", err)
	}

	prompt := fmt.Sprintf(`You are a text compressor. Apply the following skill to compress the input.

SKILL:
%s

INPUT TEXT:
%s

OUTPUT ONLY THE COMPRESSED TEXT. NO EXPLANATION.`, skillContent, params.Text)

	// Create a sub-agent for compression using the same model config
	subCfg := agentic.Config{
		SystemPrompt:    prompt,
		Tools:           tools.Tools(".", nil),
		Logger:          agentic.NewLogger(agentic.Error),
		ReasoningEffort: t.reasoningEffort,
	}

	capture := &resultCapture{}
	subAgent := agentic.NewAgent(subCfg)
	subAgent.AddObserver(capture)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := subAgent.Run(ctx, "Compress the input text above."); err != nil {
		return "", fmt.Errorf("compression failed: %w", err)
	}

	return capture.content, nil
}

func readSkillFromEmbed(fsys embed.FS) (string, error) {
	entries, err := fsys.ReadDir("skills")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			skillPath := fmt.Sprintf("skills/%s/SKILL.md", entry.Name())
			content, err := fsys.ReadFile(skillPath)
			if err == nil {
				return string(content), nil
			}
		}
	}
	return "", fmt.Errorf("no skill file found")
}

type resultCapture struct {
	content string
}

func (rc *resultCapture) OnEvent(event agentic.OutputEvent) {
	if event.Type == agentic.EventToolCall {
		rc.content = ""
	}
	if event.Type == agentic.EventContent && event.Role == agentic.Assistant && event.State == agentic.StateContent {
		rc.content += event.Text
	}
}

var _ agentic.OutputObserver = &resultCapture{}

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"",
	)

	// Read input
	input := readInput()
	if input == "" {
		fmt.Fprintf(os.Stderr, "Error: No input provided. Use stdin pipe or pass text as argument.\n")
		os.Exit(1)
	}

	// If no model specified, fetch the first available model
	modelName := cfg.Model
	if modelName == "" {
		m, err := fetchFirstModel(cfg.Endpoint, cfg.APIKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching model: %v\n", err)
			os.Exit(1)
		}
		modelName = m
	}

	// Execute compression via a sub-agent
	agentCfg := cfg.ToAgentConfig()
	agentCfg.SystemPrompt = "You are a text compressor."
	agentCfg.Logger = agentic.NewLogger(agentic.Error)
	agentCfg.Tools = tools.Tools(".", nil)

	capture := &resultCapture{}
	agent := agentic.NewAgent(agentCfg)
	agent.AddObserver(capture)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	skillContent, err := readSkillFromEmbed(skillsFS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading skill: %v\n", err)
		os.Exit(1)
	}

	compressionTask := fmt.Sprintf(`Compress the following text using this skill:

SKILL:
%s

TEXT:
%s

OUTPUT ONLY THE COMPRESSED TEXT.`, skillContent, input)

	if err := agent.Run(ctx, compressionTask); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(capture.content)
}

// readInput reads input from stdin or positional argument
func readInput() string {
	info, _ := os.Stdin.Stat()
	if info.Mode()&os.ModeNamedPipe != 0 {
		data, err := io.ReadAll(os.Stdin)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
	}
	if flag.NArg() > 0 {
		return strings.Join(flag.Args(), " ")
	}
	return ""
}

// fetchFirstModel fetches the first available model from the endpoint
func fetchFirstModel(endpoint, apiKey string) (string, error) {
	modelsURL := strings.Replace(endpoint, "/v1/chat/completions", "/v1/models", 1)

	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		return "", err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch models: status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Data) == 0 {
		return "", fmt.Errorf("no models available")
	}

	return result.Data[0].ID, nil
}
