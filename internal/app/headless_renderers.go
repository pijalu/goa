// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// output with explicit boundary markers.
type plainRenderer struct {
	out io.Writer

	mu                    sync.Mutex
	assistantOpen         bool
	thinkingOpen          bool
	companionOpen         bool
	companionThinkingOpen bool
}

func newPlainRenderer(out io.Writer) *plainRenderer {
	return &plainRenderer{out: out}
}

func (r *plainRenderer) UserPrompt(prompt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintln(r.out, "-- user")
	fmt.Fprintln(r.out, prompt)
	fmt.Fprintln(r.out)
}

func (r *plainRenderer) AssistantChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.assistantOpen {
		r.closeOpenBlocksLocked()
		fmt.Fprintln(r.out, "-- assistant")
		r.assistantOpen = true
	}
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) ThinkingStart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintln(r.out, "-- thinking start")
	r.thinkingOpen = true
}

func (r *plainRenderer) ThinkingChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.thinkingOpen {
		r.closeOpenBlocksLocked()
		fmt.Fprintln(r.out, "-- thinking start")
		r.thinkingOpen = true
	}
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) ThinkingEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.thinkingOpen {
		return
	}
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "-- thinking end")
	r.thinkingOpen = false
}

func (r *plainRenderer) ToolCall(name, id, input string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- tool call %s id=%s\n", name, id)
	fmt.Fprintln(r.out, input)
	fmt.Fprintln(r.out)
}

func (r *plainRenderer) ToolResult(name, id, output string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- tool result %s id=%s\n", name, id)
	fmt.Fprintln(r.out, output)
	fmt.Fprintln(r.out)
}

func (r *plainRenderer) Stats(stats sessionStats, turn int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- stats turn=%d %s\n", turn, formatFooterStatsPlain(stats))
}

func (r *plainRenderer) Summary(stats sessionStats, turns int, totalTime time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	parts := []string{
		fmt.Sprintf("turns=%d", turns),
		fmt.Sprintf("total_in=%d", stats.PromptN),
		fmt.Sprintf("total_out=%d", stats.PredictedN),
		fmt.Sprintf("total_tool_calls=%d", stats.ToolCalls),
	}
	if stats.ShowCost {
		parts = append(parts, fmt.Sprintf("total_cost=$%.4f", stats.CostUSD))
	}
	parts = append(parts, fmt.Sprintf("total_time=%s", totalTime.Round(time.Millisecond)))
	fmt.Fprintf(r.out, "-- summary %s\n", strings.Join(parts, " "))
}

func (r *plainRenderer) Error(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- error\n%s\n", msg)
}

func (r *plainRenderer) AssistantStreamEnd() {
	// Boundary is handled by closeOpenBlocksLocked before the next block.
}

func (r *plainRenderer) CompanionStart(cycle int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- companion start cycle=%d\n", cycle)
	r.companionOpen = true
}

func (r *plainRenderer) CompanionEnd(cycle int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	fmt.Fprintf(r.out, "-- companion end cycle=%d\n", cycle)
	r.companionOpen = false
}

func (r *plainRenderer) CompanionThinkingStart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	if !r.companionThinkingOpen {
		fmt.Fprintln(r.out, "-- companion thinking start")
		r.companionThinkingOpen = true
	}
}

func (r *plainRenderer) CompanionThinkingChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	if !r.companionThinkingOpen {
		fmt.Fprintln(r.out, "-- companion thinking start")
		r.companionThinkingOpen = true
	}
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) CompanionThinkingEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionThinkingOpen {
		return
	}
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "-- companion thinking end")
	r.companionThinkingOpen = false
}

func (r *plainRenderer) CompanionChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	fmt.Fprintln(r.out, "-- companion")
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) Flush() {
	if f, ok := r.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}

func (r *plainRenderer) closeOpenBlocksLocked() {
	if r.assistantOpen {
		fmt.Fprintln(r.out)
		r.assistantOpen = false
	}
	if r.thinkingOpen {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, "-- thinking end")
		r.thinkingOpen = false
	}
	if r.companionThinkingOpen {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, "-- companion thinking end")
		r.companionThinkingOpen = false
	}
}

// ansiRenderer implements HeadlessRenderer with minimal ANSI color markers.
type ansiRenderer struct {
	out io.Writer
}

func newANSIRenderer(out io.Writer) *ansiRenderer {
	return &ansiRenderer{out: out}
}

func (r *ansiRenderer) UserPrompt(prompt string) {
	r.colorLine("User:", "user")
	fmt.Fprintln(r.out, prompt)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) AssistantChunk(text string) {
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) ThinkingStart() {
	r.colorLine("Thinking:", "thinking")
}

func (r *ansiRenderer) ThinkingChunk(text string) {
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) ThinkingEnd() {
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) ToolCall(name, id, input string) {
	r.colorLine(fmt.Sprintf("Tool call %s (id=%s):", name, id), "tool")
	fmt.Fprintln(r.out, input)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) ToolResult(name, id, output string) {
	r.colorLine(fmt.Sprintf("Tool result %s (id=%s):", name, id), "tool")
	fmt.Fprintln(r.out, output)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) Stats(stats sessionStats, turn int) {
	r.colorLine(fmt.Sprintf("Turn %d stats:", turn), "stats")
	fmt.Fprintln(r.out, formatFooterStats(stats))
}

func (r *ansiRenderer) Summary(stats sessionStats, turns int, totalTime time.Duration) {
	r.colorLine("Summary:", "stats")
	parts := []string{
		fmt.Sprintf("turns=%d", turns),
		fmt.Sprintf("total_in=%d", stats.PromptN),
		fmt.Sprintf("total_out=%d", stats.PredictedN),
		fmt.Sprintf("total_tool_calls=%d", stats.ToolCalls),
	}
	if stats.ShowCost {
		parts = append(parts, fmt.Sprintf("total_cost=$%.4f", stats.CostUSD))
	}
	parts = append(parts, fmt.Sprintf("total_time=%s", totalTime.Round(time.Millisecond)))
	fmt.Fprintln(r.out, strings.Join(parts, " "))
}

func (r *ansiRenderer) Error(msg string) {
	r.colorLine("Error:", "error")
	fmt.Fprintln(r.out, msg)
}

func (r *ansiRenderer) AssistantStreamEnd() {
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) CompanionStart(cycle int) {
	r.colorLine(fmt.Sprintf("Companion cycle %d:", cycle), "companion")
}

func (r *ansiRenderer) CompanionEnd(cycle int) {
	fmt.Fprintf(r.out, "End companion cycle %d\n\n", cycle)
}

func (r *ansiRenderer) CompanionThinkingStart() {
	r.colorLine("Companion thinking:", "thinking")
}

func (r *ansiRenderer) CompanionThinkingChunk(text string) {
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) CompanionThinkingEnd() {
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) CompanionChunk(text string) {
	r.colorLine("Companion:", "companion")
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) Flush() {
	if f, ok := r.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}

func (r *ansiRenderer) colorLine(label, role string) {
	color := ansi.Fg(roleColor(role))
	fmt.Fprintf(r.out, "%s%s%s\n", color, label, ansi.Reset)
}

func roleColor(role string) string {
	switch role {
	case "user":
		return "#58a6ff"
	case "assistant":
		return "#7ee787"
	case "thinking":
		return "#d2a8ff"
	case "tool":
		return "#ffa657"
	case "stats":
		return "#8b949e"
	case "error":
		return "#f85149"
	case "companion":
		return "#79c0ff"
	default:
		return "#c9d1d9"
	}
}

// HeadlessApp runs a single headless agent session.
