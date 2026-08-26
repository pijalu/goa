// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/agentic"
)

// CompressCommand triggers context compression on the active agent session.
type CompressCommand struct{}

func (c *CompressCommand) Name() string      { return "compress" }
func (c *CompressCommand) Aliases() []string { return []string{} }
func (c *CompressCommand) ShortHelp() string { return "Compress agent context window" }
func (c *CompressCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs offers strategy name completion after the command.
func (c *CompressCommand) CompleteArgs(_ core.Context, prefix string) []core.ArgCompletion {
	all := []string{"tool_elision", "selective", "summarize", "hybrid", "micro"}
	var out []core.ArgCompletion
	for _, s := range all {
		if prefix == "" || strings.HasPrefix(s, prefix) {
			out = append(out, core.ArgCompletion{Value: s, Description: "compress using " + s})
		}
	}
	return out
}

// AsyncHint implements core.AsyncCommand. Strategies that invoke the LLM
// (summarize, hybrid, and the default which may resolve to either) return a
// spinner label so the host runs them in a background goroutine instead of
// freezing the UI. In-memory strategies (tool_elision, selective, micro) are
// instant and return "" to run synchronously.
func (c *CompressCommand) AsyncHint(args []string) string {
	strategy, _, err := parseCompressArgs(args)
	if err != nil {
		return ""
	}
	switch strategy {
	case "", "summarize", "hybrid":
		label := "Compressing"
		if strategy != "" {
			label += " (" + strategy + ")"
		}
		return label + "…"
	default:
		return ""
	}
}

func (c *CompressCommand) Run(ctx core.Context, args []string) error {
	if ctx.AgentManager == nil {
		return fmt.Errorf("no active agent session")
	}

	strategy, force, err := parseCompressArgs(args)
	if err != nil {
		return err
	}

	// Get context stats before compression.
	agent := ctx.AgentManager.CurrentAgent()
	var beforeStats *agentic.ContextStats
	if agent != nil {
		s := agent.ContextStats()
		beforeStats = &s
	}

	start := time.Now()
	if err := ctx.AgentManager.TriggerCompressionWith(context.Background(), strategy, force); err != nil {
		return fmt.Errorf("compression failed: %w", err)
	}
	elapsed := time.Since(start)

	// Get context stats after compression.
	var afterStats *agentic.ContextStats
	if agent != nil {
		s := agent.ContextStats()
		afterStats = &s
	}

	reportCompression(ctx, strategy, beforeStats, afterStats, elapsed)
	return nil
}

// parseCompressArgs interprets /compress arguments.
//
// Accepted forms (router splits on ":"):
//
//	/compress                       → "", force=true
//	/compress:micro                 → "micro", force=true
//	/compress:micro:force           → "micro", force=true (explicit)
//	/compress force                 → "", force=true (space form, single arg)
//	/compress summarize --force     → "summarize", force=true
//
// Strategy is validated against the known strategy set; unknown values
// return an error so the user gets actionable feedback.
func parseCompressArgs(args []string) (strategy string, force bool, err error) {
	// Manual /compress always forces by default — otherwise it would be a
	// no-op whenever usage is below the configured threshold.
	force = true

	var rest []string
	for _, a := range args {
		switch {
		case a == "":
			continue
		case a == "force" || a == "--force":
			force = true
		case a == "noforce" || a == "--no-force":
			force = false
		default:
			rest = append(rest, a)
		}
	}

	if len(rest) > 0 {
		strategy = rest[0]
		if !isKnownStrategy(strategy) {
			return "", false, fmt.Errorf("unknown compression strategy %q (valid: tool_elision, selective, summarize, hybrid, micro)", strategy)
		}
	}
	return strategy, force, nil
}

// isKnownStrategy reports whether s names a supported CompressionStrategy.
func isKnownStrategy(s string) bool {
	switch agentic.CompressionStrategy(s) {
	case agentic.CompressionToolElision,
		agentic.CompressionSelective,
		agentic.CompressionSummarize,
		agentic.CompressionHybrid,
		agentic.CompressionMicro:
		return true
	}
	return false
}

// reportCompression writes a human-readable summary of the compression result.
// It distinguishes "freed N tokens" from "no reduction" and always names the
// strategy that was applied so the user can correlate the action with output.
func reportCompression(ctx core.Context, strategy string, before, after *agentic.ContextStats, elapsed time.Duration) {
	label := strategy
	if label == "" {
		label = "default"
	}

	switch {
	case before == nil || after == nil:
		ctx.Writef("Context compression (%s) triggered in %.2fs\n", label, elapsed.Seconds())
		return
	case before.Messages == 0:
		// History empty — nothing to do.
		ctx.Writef("Context compression (%s): history empty, nothing to do (%.2fs)\n", label, elapsed.Seconds())
		return
	}

	saved := before.EstimatedTokens - after.EstimatedTokens
	if saved > 0 {
		ctx.Writef("Context compressed (%s): %d → %d tokens (freed %d) in %.2fs\n",
			label, before.EstimatedTokens, after.EstimatedTokens, saved, elapsed.Seconds())
		ctx.Writef("  Usage: %s%% → %s%% of %d context window  Messages: %d → %d\n",
			pctOf(before.EstimatedTokens, before.MaxTokens), pctOf(after.EstimatedTokens, after.MaxTokens),
			after.MaxTokens, before.Messages, after.Messages)
		return
	}

	// Even when no tokens were freed, name the strategy so the user knows
	// the command actually ran (e.g. micro compaction had nothing to trim).
	ctx.Writef("Context compression (%s) applied: %d tokens, %s%% usage (nothing to trim) in %.2fs  Messages: %d\n",
		label, after.EstimatedTokens, pctOf(after.EstimatedTokens, after.MaxTokens), elapsed.Seconds(), after.Messages)
}

// pctOf renders estimated tokens as a percentage of the context window.
//
// The report deliberately derives percentages from EstimatedTokens — the same
// series as the token counts on the line above — instead of ContextStats.UsagePercent.
// UsagePercent reads the PROJECTED next-request occupancy, which is anchored at
// the last provider-reported prompt size; any history-shrinking strategy calls
// invalidateContextUsageLocked, dropping that anchor mid-report so the after-%
// falls back to the raw heuristic while the before-% was still provider-anchored.
// Mixing the two bases produced self-contradictory reports ("freed 153362 tokens"
// yet "Usage: 35% → 51%") even though both token counts came from one estimator.
func pctOf(tokens, max int) string {
	if max <= 0 {
		return "?"
	}
	return strconv.Itoa(tokens * 100 / max)
}