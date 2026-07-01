// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strconv"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// ExchangeCommand shows the raw LLM exchange for a turn.
type ExchangeCommand struct{}

func (c *ExchangeCommand) Name() string      { return "exchange" }
func (c *ExchangeCommand) Aliases() []string { return []string{} }
func (c *ExchangeCommand) ShortHelp() string { return "Show raw LLM request/response for a turn" }
func (c *ExchangeCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ExchangeCommand) Run(ctx core.Context, args []string) error {
	return showExchange(ctx, ctx, args)
}

// showExchange displays the full LLM exchange for a turn as a readable tree.
// Depends on OutputWriter + SessionRecorder.
func showExchange(w core.OutputWriter, rec core.SessionRecorder, args []string) error {
	turn, ok := selectTurn(w, rec, args)
	if !ok {
		return nil
	}

	writeTurnHeader(w, turn)
	writeStatsSection(w, turn.TokensUsed, turn.TokenUsage, turn.Timing.Total)
	writeUserInputSection(w, turn.UserInput)
	writeThinkingSection(w, turn.Thinking)
	writeToolCallsSection(w, turn.ToolCalls)
	writeToolResultsSection(w, turn.ToolResults)
	writeAssistantResponsesSection(w, turn.AssistantResponses, turn.ResponseJSON)
	writeRequestJSONSection(w, turn.RequestJSON)
	return nil
}

// selectTurn resolves the requested turn from history.
func selectTurn(w core.OutputWriter, rec core.SessionRecorder, args []string) (*core.TurnRecord, bool) {
	history := rec.TurnHistory()
	if len(history) == 0 {
		writeStr(w, "No turn history available. Send a message first.\n")
		return nil, false
	}
	if len(args) == 0 {
		return rec.LastTurn(), true
	}
	turnNum, err := strconv.Atoi(args[0])
	if err != nil || turnNum < 1 || turnNum > len(history) {
		writeFmt(w, "Invalid turn number. Available: 1-%d\n", len(history))
		return nil, false
	}
	return &history[turnNum-1], true
}

func writeTurnHeader(w core.OutputWriter, turn *core.TurnRecord) {
	writeFmt(w, "Turn #%d\n", turn.Number)
	writeStr(w, strings.Repeat("=", 40)+"\n")
}

func writeStatsSection(w core.OutputWriter, tokensUsed int, usage core.TurnTokenUsage, total float64) {
	writeFmt(w, "Duration: %.2fs\n", total)
	writeFmt(w, "Tokens:   %d (in=%d out=%d)\n", tokensUsed, usage.PromptN, usage.PredictedN)
	if usage.CacheRead > 0 || usage.CacheWrite > 0 {
		writeFmt(w, "Cache:    read=%d write=%d\n", usage.CacheRead, usage.CacheWrite)
	}
	if usage.SpeedTokPerSec > 0 {
		writeFmt(w, "Speed:    %.1f tok/s\n", usage.SpeedTokPerSec)
	}
	if usage.ContextMax > 0 {
		pct := float64(usage.ContextEstimate) / float64(usage.ContextMax) * 100
		writeFmt(w, "Context:  %d/%d (%.1f%%)\n", usage.ContextEstimate, usage.ContextMax, pct)
	}
	writeStr(w, "\n")
}

func writeUserInputSection(w core.OutputWriter, input string) {
	writeStr(w, "User input\n")
	writeStr(w, strings.Repeat("-", 40)+"\n")
	if input != "" {
		writeStr(w, input+"\n")
	} else {
		writeStr(w, "(no user input captured)\n")
	}
	writeStr(w, "\n")
}

func writeThinkingSection(w core.OutputWriter, blocks []string) {
	if len(blocks) == 0 {
		return
	}
	writeStr(w, "Thinking\n")
	writeStr(w, strings.Repeat("-", 40)+"\n")
	for i, block := range blocks {
		writeFmt(w, "Block #%d:\n%s\n\n", i+1, block)
	}
}

func writeToolCallsSection(w core.OutputWriter, calls []core.TurnToolCall) {
	if len(calls) == 0 {
		return
	}
	writeStr(w, "Tool calls\n")
	writeStr(w, strings.Repeat("-", 40)+"\n")
	for _, tc := range calls {
		writeFmt(w, "• %s (id=%s)\n", tc.Name, tc.CallID)
		writeFmt(w, "  Input: %s\n", tc.Input)
	}
	writeStr(w, "\n")
}

func writeToolResultsSection(w core.OutputWriter, results []core.TurnToolResult) {
	if len(results) == 0 {
		return
	}
	writeStr(w, "Tool results\n")
	writeStr(w, strings.Repeat("-", 40)+"\n")
	for _, tr := range results {
		writeFmt(w, "• %s (id=%s)\n", tr.Name, tr.CallID)
		writeFmt(w, "  Result: %s\n", tr.Result)
	}
	writeStr(w, "\n")
}

func writeAssistantResponsesSection(w core.OutputWriter, responses []string, responseJSON string) {
	writeStr(w, "Assistant responses\n")
	writeStr(w, strings.Repeat("-", 40)+"\n")
	if len(responses) > 0 {
		for _, resp := range responses {
			writeStr(w, resp+"\n\n")
		}
		return
	}
	if responseJSON != "" {
		writeStr(w, responseJSON+"\n")
		return
	}
	writeStr(w, "(no response captured)\n")
}

func writeRequestJSONSection(w core.OutputWriter, requestJSON string) {
	writeStr(w, "Request JSON\n")
	writeStr(w, strings.Repeat("=", 40)+"\n")
	if requestJSON != "" {
		writeStr(w, requestJSON+"\n")
	} else {
		writeStr(w, "(no request captured)\n")
	}
}

// PromptCommand shows the current system prompt.
type PromptCommand struct{}

func (c *PromptCommand) Name() string      { return "prompt" }
func (c *PromptCommand) Aliases() []string { return []string{} }
func (c *PromptCommand) ShortHelp() string { return "Show the current system prompt" }
func (c *PromptCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *PromptCommand) Run(ctx core.Context, args []string) error {
	return showSystemPrompt(ctx, ctx, args)
}

// showSystemPrompt displays the assembled system prompt.
// Depends on OutputWriter + SystemPromptProvider.
func showSystemPrompt(w core.OutputWriter, sp core.SystemPromptProvider, args []string) error {
	prompt := sp.SystemPrompt()
	if prompt == "" {
		writeStr(w, "No system prompt loaded.\n")
		return nil
	}
	writeStr(w, "System Prompt:\n")
	writeStr(w, strings.Repeat("=", 40)+"\n\n")
	writeStr(w, prompt+"\n")
	if len(args) > 0 && args[0] == "diff" {
		writeStr(w, "\n(Diff mode requires storing previous prompt version)\n")
	}
	return nil
}

// StatsCommand shows token usage and performance statistics.
type StatsCommand struct{}

func (c *StatsCommand) Name() string      { return "stats" }
func (c *StatsCommand) Aliases() []string { return []string{} }
func (c *StatsCommand) ShortHelp() string { return "Show token usage and performance statistics" }
func (c *StatsCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *StatsCommand) Run(ctx core.Context, args []string) error {
	return showStats(ctx, ctx, args)
}

// showStats displays token usage statistics across turns.
// Depends on OutputWriter + SessionRecorder.
func showStats(w core.OutputWriter, rec core.SessionRecorder, args []string) error {
	history := rec.TurnHistory()
	if len(history) == 0 {
		writeStr(w, "No turn history available. Send a message first.\n")
		return nil
	}

	if len(args) > 0 {
		return showTurnDetail(w, rec, args[0])
	}

	var totals core.TurnTokenUsage
	var totalTokens int
	writeStr(w, "Token statistics per turn:\n")
	writeStr(w, strings.Repeat("-", 60)+"\n")
	for _, t := range history {
		totals = addTokenUsage(totals, t.TokenUsage)
		totalTokens += t.TokensUsed
		writeTurnStats(w, t)
	}
	writeStr(w, strings.Repeat("-", 60)+"\n")
	writeSummaryStats(w, rec, totalTokens, totals, history)
	return nil
}

func writeTurnStats(w core.OutputWriter, t core.TurnRecord) {
	usage := t.TokenUsage
	writeFmt(w, "  Turn #%d:\n", t.Number)
	writeFmt(w, "    Tokens: %d (in=%d out=%d)\n", t.TokensUsed, usage.PromptN, usage.PredictedN)
	if usage.CacheRead > 0 || usage.CacheWrite > 0 {
		writeFmt(w, "    Cache:  R=%d W=%d\n", usage.CacheRead, usage.CacheWrite)
	}
	if usage.SpeedTokPerSec > 0 {
		writeFmt(w, "    Speed:  %.1f tok/s\n", usage.SpeedTokPerSec)
	}
	if usage.CostUSD > 0 {
		writeFmt(w, "    Cost:   $%.4f\n", usage.CostUSD)
	}
	if usage.ContextMax > 0 {
		pct := float64(usage.ContextEstimate) / float64(usage.ContextMax) * 100
		writeFmt(w, "    Ctx:    %d/%d (%.1f%%)\n", usage.ContextEstimate, usage.ContextMax, pct)
	}
	writeFmt(w, "    Time:   %.2fs\n", t.Timing.Total)
	writeFmt(w, "    Tools:  %d calls\n", len(t.ToolCalls))
	writeStr(w, "\n")
}

func writeSummaryStats(w core.OutputWriter, rec core.SessionRecorder, totalTokens int, totals core.TurnTokenUsage, history []core.TurnRecord) {
	writeFmt(w, "  Total: %d tokens across %d turns\n", totalTokens, len(history))
	writeFmt(w, "  Total in:  %d  out: %d\n", totals.PromptN, totals.PredictedN)
	if totals.CacheRead > 0 || totals.CacheWrite > 0 {
		writeFmt(w, "  Cache R: %d  W: %d\n", totals.CacheRead, totals.CacheWrite)
	}
	if totals.CostUSD > 0 {
		writeFmt(w, "  Cost: $%.4f\n", totals.CostUSD)
	}
	if last := rec.LastTurn(); last != nil && last.TokenUsage.ContextMax > 0 {
		pct := float64(last.TokenUsage.ContextEstimate) / float64(last.TokenUsage.ContextMax) * 100
		writeFmt(w, "  Context: %d/%d (%.1f%%)\n", last.TokenUsage.ContextEstimate, last.TokenUsage.ContextMax, pct)
	}
	writeFmt(w, "  Tool calls: %d total\n", totalToolCalls(history))
}

func addTokenUsage(a, b core.TurnTokenUsage) core.TurnTokenUsage {
	return core.TurnTokenUsage{
		PromptN:         a.PromptN + b.PromptN,
		PredictedN:      a.PredictedN + b.PredictedN,
		CacheRead:       a.CacheRead + b.CacheRead,
		CacheWrite:      a.CacheWrite + b.CacheWrite,
		SpeedTokPerSec:  b.SpeedTokPerSec,
		CostUSD:         a.CostUSD + b.CostUSD,
		ContextEstimate: b.ContextEstimate,
		ContextMax:      b.ContextMax,
	}
}

// showTurnDetail dumps a detailed tree for a single turn.
func showTurnDetail(w core.OutputWriter, rec core.SessionRecorder, arg string) error {
	history := rec.TurnHistory()
	turnNum, err := strconv.Atoi(arg)
	if err != nil || turnNum < 1 || turnNum > len(history) {
		writeFmt(w, "Invalid turn number. Available: 1-%d\n", len(history))
		return nil
	}
	return showExchange(w, rec, []string{arg})
}

func totalToolCalls(history []core.TurnRecord) int {
	var n int
	for _, t := range history {
		n += len(t.ToolCalls)
	}
	return n
}
