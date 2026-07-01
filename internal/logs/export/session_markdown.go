// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/ansi"
)

// RenderSessionMarkdown returns the full dump report as a Markdown string.
// It is used by /export to include a human-readable summary
// inside the diagnostic bundle.
func RenderSessionMarkdown(ctx core.Context) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Goa Session Dump — %s\n\n", time.Now().Format(time.RFC3339)))

	b.WriteString(dumpConfig(ctx))
	b.WriteString(dumpSystemPrompt(ctx))
	b.WriteString(dumpToolSchemas(ctx))
	b.WriteString(dumpSkills(ctx))
	b.WriteString(dumpModeState(ctx))
	b.WriteString(dumpChat(ctx))
	b.WriteString(dumpTurnHistory(ctx))
	b.WriteString(dumpContextStats(ctx))
	b.WriteString(dumpSavedSessions(ctx))
	return b.String()
}

func dumpConfig(ctx core.Context) string {
	cfg := ctx.Config
	var b strings.Builder
	b.WriteString("## Configuration\n\n")
	if cfg != nil {
		b.WriteString(fmt.Sprintf("- Profile: %s\n", cfg.ActiveMajor()))
		b.WriteString(fmt.Sprintf("- Model: %s\n", cfg.ActiveModel))
		b.WriteString(fmt.Sprintf("- Provider: %s\n", cfg.ActiveProvider))
		b.WriteString(fmt.Sprintf("- Autonomy: %s\n", cfg.Execution.Mode))
		b.WriteString(fmt.Sprintf("- Config dir: %s\n", cfg.ConfigDir))
		if len(cfg.Providers) > 0 {
			b.WriteString("\n### Providers\n\n")
			for _, p := range cfg.Providers {
				modelName := ""
				if m := firstModelForProvider(cfg, p.ID); m != nil {
					modelName = m.Model
				}
				b.WriteString(fmt.Sprintf("- %s (%s): model=%s\n", p.ID, p.Name, modelName))
			}
		}
	}
	b.WriteString(fmt.Sprintf("- Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	b.WriteString("\n---\n\n")
	return b.String()
}

func dumpSystemPrompt(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## System Prompt\n\n")
	if sp, ok := any(ctx).(core.SystemPromptProvider); ok {
		if prompt := sp.SystemPrompt(); prompt != "" {
			b.WriteString("```\n")
			b.WriteString(ansi.Strip(prompt))
			b.WriteString("\n```\n\n")
		} else {
			b.WriteString("*(empty)*\n\n")
		}
	} else {
		b.WriteString("*(not available)*\n\n")
	}
	b.WriteString("---\n\n")
	return b.String()
}

func dumpToolSchemas(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## Tool Schemas\n\n")
	if ctx.ToolRegistry != nil {
		tools := ctx.ToolRegistry.All()
		b.WriteString(fmt.Sprintf("Total: %d tool(s)\n\n", len(tools)))
		for _, t := range tools {
			schema := t.Schema()
			b.WriteString(fmt.Sprintf("### %s\n", schema.Name))
			b.WriteString(fmt.Sprintf("- Description: %s\n", schema.Description))
			if schema.Schema != nil {
				if schemaBytes, err := json.MarshalIndent(schema.Schema, "", "  "); err == nil {
					b.WriteString("```json\n")
					b.WriteString(string(schemaBytes))
					b.WriteString("\n```\n")
				}
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("*(not available)*\n\n")
	}
	b.WriteString("---\n\n")
	return b.String()
}

func dumpSkills(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## Skills\n\n")
	if ctx.SkillRegistry != nil {
		skills := ctx.SkillRegistry.List()
		b.WriteString(fmt.Sprintf("Total: %d skill(s)\n\n", len(skills)))
		for _, s := range skills {
			typ := "sub-agent"
			if s.Inline {
				typ = "inline"
			}
			b.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", s.Name, typ, s.Description))
		}
	} else {
		b.WriteString("*(not available)*\n\n")
	}
	b.WriteString("\n---\n\n")
	return b.String()
}

func dumpModeState(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## Mode State\n\n")
	if ctx.AgentManager != nil {
		mode := ctx.CurrentMode()
		b.WriteString(fmt.Sprintf("- Major: %s\n", mode.Major))
		b.WriteString(fmt.Sprintf("- Autonomy: %s\n", mode.Autonomy))
		b.WriteString(fmt.Sprintf("- Skills: %v\n", mode.Skills))
		b.WriteString(fmt.Sprintf("- Thinking level: %s\n", ctx.GetThinkingLevel()))
	} else {
		b.WriteString("*(not available)*\n\n")
	}
	b.WriteString("\n---\n\n")
	return b.String()
}

func dumpChat(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## Rendered Chat\n\n")
	if ctx.RenderChat != nil {
		rendered := ctx.RenderChat(80)
		b.WriteString("```\n")
		b.WriteString(ansi.Strip(rendered))
		b.WriteString("\n```\n\n")
	} else {
		b.WriteString("*(not available)*\n\n")
	}
	b.WriteString("---\n\n")
	return b.String()
}

func dumpTurnHistory(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## Turn History\n\n")
	if ctx.AgentManager == nil {
		b.WriteString("*(not available)*\n\n")
		b.WriteString("\n---\n\n")
		return b.String()
	}

	history := ctx.AgentManager.TurnHistory()
	b.WriteString(fmt.Sprintf("Total: %d turn(s)\n\n", len(history)))
	var totalPrompt, totalPredicted, totalTokens int
	for _, turn := range history {
		totalTokens += turn.TokensUsed
		totalPrompt += turn.TokenUsage.PromptN
		totalPredicted += turn.TokenUsage.PredictedN
		writeDumpTurn(&b, turn)
	}
	writeDumpTurnSummary(&b, totalTokens, totalPrompt, totalPredicted)
	b.WriteString("\n---\n\n")
	return b.String()
}

func writeDumpTurn(b *strings.Builder, turn core.TurnRecord) {
	writeDumpTurnHeader(b, turn)
	writeDumpTurnStats(b, turn)
	writeDumpTurnToolCalls(b, turn.ToolCalls)
	writeDumpTurnToolResults(b, turn.ToolResults)
	writeDumpTurnJSON(b, "Request", turn.RequestJSON)
	if turn.ResponseJSON != "" {
		writeDumpTurnJSON(b, "Response", turn.ResponseJSON)
	}
}

func writeDumpTurnHeader(b *strings.Builder, turn core.TurnRecord) {
	b.WriteString(fmt.Sprintf("### Turn %d\n\n", turn.Number))
}

func writeDumpTurnStats(b *strings.Builder, turn core.TurnRecord) {
	b.WriteString(fmt.Sprintf("- Tokens: %d (in=%d out=%d)\n", turn.TokensUsed, turn.TokenUsage.PromptN, turn.TokenUsage.PredictedN))
	if turn.TokenUsage.CacheRead > 0 || turn.TokenUsage.CacheWrite > 0 {
		b.WriteString(fmt.Sprintf("- Cache: R=%d W=%d\n", turn.TokenUsage.CacheRead, turn.TokenUsage.CacheWrite))
	}
	if turn.TokenUsage.CostUSD > 0 {
		b.WriteString(fmt.Sprintf("- Cost: $%.4f\n", turn.TokenUsage.CostUSD))
	}
	if turn.TokenUsage.SpeedTokPerSec > 0 {
		b.WriteString(fmt.Sprintf("- Speed: %.1f tok/s\n", turn.TokenUsage.SpeedTokPerSec))
	}
	b.WriteString(fmt.Sprintf("- Timing: Total=%.2fs\n", turn.Timing.Total))
}

func writeDumpTurnToolCalls(b *strings.Builder, calls []core.TurnToolCall) {
	if len(calls) == 0 {
		return
	}
	b.WriteString("\n**Tool Calls:**\n\n")
	for _, tc := range calls {
		b.WriteString(fmt.Sprintf("- `%s`: %s\n", tc.Name, truncateString(tc.Input, 80)))
	}
}

func writeDumpTurnToolResults(b *strings.Builder, results []core.TurnToolResult) {
	if len(results) == 0 {
		return
	}
	b.WriteString("\n**Tool Results:**\n\n")
	for _, tr := range results {
		b.WriteString(fmt.Sprintf("- `%s`: %s\n", tr.Name, truncateString(tr.Result, 80)))
	}
}

func writeDumpTurnJSON(b *strings.Builder, label, json string) {
	b.WriteString(fmt.Sprintf("\n**%s:**\n```json\n", label))
	b.WriteString(prettyJSON(json))
	b.WriteString("\n```\n\n")
}

func writeDumpTurnSummary(b *strings.Builder, totalTokens, totalPrompt, totalPredicted int) {
	b.WriteString("### Summary\n\n")
	b.WriteString(fmt.Sprintf("- Total tokens: %d\n", totalTokens))
	b.WriteString(fmt.Sprintf("- Total in: %d\n", totalPrompt))
	b.WriteString(fmt.Sprintf("- Total out: %d\n", totalPredicted))
}

func dumpContextStats(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## Context Stats\n\n")
	if ctx.AgentManager != nil {
		if agent := ctx.AgentManager.CurrentAgent(); agent != nil {
			stats := agent.ContextStats()
			b.WriteString(fmt.Sprintf("- Messages: %d\n", stats.Messages))
			b.WriteString(fmt.Sprintf("- Characters: %d\n", stats.Characters))
			b.WriteString(fmt.Sprintf("- Estimated tokens: %d\n", stats.EstimatedTokens))
			b.WriteString(fmt.Sprintf("- Max tokens: %d", stats.MaxTokens))
			if stats.AutoMax {
				b.WriteString(" (auto-detected)")
			}
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("- Usage: %d%%\n", stats.UsagePercent))
		} else {
			b.WriteString("*(no active agent)*\n")
		}
	} else {
		b.WriteString("*(not available)*\n")
	}
	b.WriteString("\n---\n\n")
	return b.String()
}

func dumpSavedSessions(ctx core.Context) string {
	var b strings.Builder
	b.WriteString("## Saved Sessions\n\n")
	if ctx.SessionStore != nil {
		if sessions, err := ctx.SessionStore.ListSessions(); err == nil && len(sessions) > 0 {
			for _, s := range sessions {
				b.WriteString(fmt.Sprintf("- %s: %d events, %d tokens\n", s.Name, s.EventCount, s.TokenTotal))
			}
		} else {
			b.WriteString("*(no saved sessions)*\n")
		}
	} else {
		b.WriteString("*(not available)*\n")
	}
	b.WriteString("\n")
	return b.String()
}

func prettyJSON(s string) string {
	if s == "" {
		return ""
	}
	return ansi.Strip(s)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return strings.ReplaceAll(s, "\n", "\\n")
	}
	return strings.ReplaceAll(s[:maxLen], "\n", "\\n") + "..."
}

func firstModelForProvider(cfg *config.Config, providerID string) *config.ModelConfig {
	for i := range cfg.Models {
		if cfg.Models[i].ProviderID == providerID {
			return &cfg.Models[i]
		}
	}
	return nil
}
