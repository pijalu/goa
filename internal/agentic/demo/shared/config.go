// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package shared provides common configuration helpers for agentic demos.
package shared

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// Config holds the LLM connection settings shared across demos.
type Config struct {
	Endpoint         string
	APIKey           string
	Model            string
	SkillMode        string
	Compression      string
	MaxTokens        int
	Threshold        int
	DetectContext    bool
	ToolResultAsUser bool
	MaxToolRepeatTotal       int
	MaxToolRepeatConsecutive int
	ReasoningEffort          string
}

var (
	endpointFlag         = flag.String("endpoint", "", "OpenAI-compatible API endpoint URL")
	apiKeyFlag           = flag.String("api-key", "", "API key for the endpoint")
	modelFlag            = flag.String("model", "", "Model name to use")
	skillModeFlag        = flag.String("skill-mode", "subagent", "Skill execution mode: subagent or inline")
	compressionFlag      = flag.String("compression", "hybrid", "Compression strategy: tool_elision, selective, summarize, hybrid")
	maxTokensFlag        = flag.Int("max-tokens", 0, "Context window size in tokens (0 = auto-detect if -detect-context is set)")
	thresholdFlag        = flag.Int("threshold", 75, "Compression threshold percentage (0-100)")
	detectCtxFlag        = flag.Bool("detect-context", true, "Auto-detect context window from provider")
	toolResultAsUserFlag = flag.String("tool-result-as-user", "auto", "Format tool results as user messages: auto, true, false. Auto enables for Gemma models.")
	maxToolRepeatFlag    = flag.Int("max-tool-repeat", 1, "Max identical tool calls per turn before loop guardrail triggers (0 = disable)")
	reasoningEffortFlag  = flag.String("reasoning-effort", "", "Model reasoning effort, e.g. low|medium|high|xhigh, on|off (empty = provider default)")
)

// Parse reads CLI flags and environment variables into a Config.
// Flags take precedence over environment variables, which take precedence
// over the supplied defaults.
func Parse(defaultEndpoint, defaultModel string) Config {
	if !flag.Parsed() {
		flag.Parse()
	}

	cfg := Config{
		Endpoint:        firstNonEmpty(*endpointFlag, os.Getenv("AGENTIC_ENDPOINT"), defaultEndpoint),
		APIKey:          firstNonEmpty(*apiKeyFlag, os.Getenv("AGENTIC_API_KEY")),
		Model:           firstNonEmpty(*modelFlag, os.Getenv("AGENTIC_MODEL"), defaultModel),
		SkillMode:       firstNonEmpty(*skillModeFlag, os.Getenv("AGENTIC_SKILL_MODE"), "subagent"),
		Compression:     firstNonEmpty(*compressionFlag, os.Getenv("AGENTIC_COMPRESSION"), "hybrid"),
		Threshold:       *thresholdFlag,
		DetectContext:   *detectCtxFlag,
		MaxToolRepeatTotal:       *maxToolRepeatFlag,
		ReasoningEffort: firstNonEmpty(*reasoningEffortFlag, os.Getenv("AGENTIC_REASONING_EFFORT")),
	}

	trau := strings.ToLower(firstNonEmpty(*toolResultAsUserFlag, os.Getenv("AGENTIC_TOOL_RESULT_AS_USER"), "auto"))
	switch trau {
	case "true", "yes", "1":
		cfg.ToolResultAsUser = true
	case "false", "no", "0":
		cfg.ToolResultAsUser = false
	default:
		cfg.ToolResultAsUser = strings.Contains(strings.ToLower(cfg.Model), "gemma")
	}

	if envMax := os.Getenv("AGENTIC_MAX_TOKENS"); envMax != "" {
		if v, err := strconv.Atoi(envMax); err == nil {
			cfg.MaxTokens = v
		}
	}
	if *maxTokensFlag != 0 {
		cfg.MaxTokens = *maxTokensFlag
	}

	if envRepeat := os.Getenv("AGENTIC_MAX_TOOL_REPEAT"); envRepeat != "" {
		if v, err := strconv.Atoi(envRepeat); err == nil {
			cfg.MaxToolRepeatTotal = v
		}
	}

	return cfg
}

// ToModel creates a provider.Model from the config settings.
func (c Config) ToModel() provider.Model {
	return provider.Model{
		ID:       c.Model,
		Name:     c.Model,
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderCustom,
		BaseURL:  c.Endpoint,
	}
}

// ToAgentConfig builds an agentic.Config with the model and common settings.
func (c Config) ToAgentConfig() agentic.Config {
	tr := c.ToolResultAsUser
	return agentic.Config{
		Model:            c.ToModel(),
		APIKey:           c.APIKey,
		ToolResultAsUser: &tr,
		MaxToolRepeatTotal:       c.MaxToolRepeatTotal,
		MaxToolRepeatConsecutive: c.MaxToolRepeatConsecutive,
		ReasoningEffort:  agentic.ReasoningEffort(c.ReasoningEffort),
	}
}

// ToSkillMode converts the skill mode string to the typed enum.
func (c Config) ToSkillMode() agentic.SkillExecutionMode {
	switch strings.ToLower(c.SkillMode) {
	case "inline":
		return agentic.SkillExecutionModeInline
	default:
		return agentic.SkillExecutionModeSubAgent
	}
}

// ToCompression builds a ContextCompressionConfig.
func (c Config) ToCompression() agentic.ContextCompressionConfig {
	fmt.Fprintf(os.Stderr, "[config] Model: %s, ToolResultAsUser: %v, MaxToolRepeatTotal: %d, MaxToolRepeatConsecutive: %d\n",
		c.Model, c.ToolResultAsUser, c.MaxToolRepeatTotal, c.MaxToolRepeatConsecutive)
	maxTokens := c.MaxTokens
	if maxTokens == 0 && c.DetectContext {
		maxTokens = agentic.DetectContextWindow(c.Endpoint, c.Model, c.APIKey)
		if maxTokens > 0 {
			fmt.Fprintf(os.Stderr, "[config] Auto-detected context window: %d tokens\n", maxTokens)
		}
	}
	if maxTokens == 0 {
		fmt.Fprintln(os.Stderr, "[config] Context compression disabled (no max-tokens set and auto-detection failed)")
	}

	var strategy agentic.CompressionStrategy
	switch strings.ToLower(c.Compression) {
	case "tool_elision", "tool-elision":
		strategy = agentic.CompressionToolElision
	case "selective":
		strategy = agentic.CompressionSelective
	case "summarize":
		strategy = agentic.CompressionSummarize
	case "hybrid":
		strategy = agentic.CompressionHybrid
	default:
		strategy = agentic.CompressionHybrid
	}

	return agentic.ContextCompressionConfig{
		MaxTokens:           maxTokens,
		ThresholdPercent:    c.Threshold,
		OnContextError:      true,
		Strategy:            strategy,
		PreserveRecentTurns: 2,
	}
}

// MustParse is like Parse but prints usage and exits if the endpoint is empty.
func MustParse(defaultEndpoint, defaultModel string) Config {
	cfg := Parse(defaultEndpoint, defaultModel)
	if cfg.Endpoint == "" {
		fmt.Fprintln(os.Stderr, "Error: endpoint is required. Set via -endpoint flag or AGENTIC_ENDPOINT env var.")
		flag.Usage()
		os.Exit(1)
	}
	return cfg
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
