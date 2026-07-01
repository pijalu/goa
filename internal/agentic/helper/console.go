// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"fmt"
	"io"
	"os"

	"github.com/pijalu/goa/internal/agentic"
)

// ConsoleOption configures a ConsoleObserver.
type ConsoleOption func(*ConsoleObserver)

// WithConsoleWriter sets the output destination. Defaults to os.Stdout.
func WithConsoleWriter(w io.Writer) ConsoleOption {
	return func(c *ConsoleObserver) {
		c.writer = w
	}
}

// WithConsoleFormat sets a custom prefix formatter.
func WithConsoleFormat(f FormatFunc) ConsoleOption {
	return func(c *ConsoleObserver) {
		c.format = f
	}
}

// ConsoleObserver receives output events and prints formatted output to an io.Writer.
type ConsoleObserver struct {
	writer   io.Writer
	format   FormatFunc
	state    agentic.OutputState
	lastRole agentic.Role // Track last role to detect role changes
}

// NewConsoleObserver creates a ConsoleObserver with the given options.
func NewConsoleObserver(opts ...ConsoleOption) *ConsoleObserver {
	c := &ConsoleObserver{
		format: DefaultFormat(),
		writer: os.Stdout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// OnEvent implements agentic.OutputObserver.
func (c *ConsoleObserver) OnEvent(event agentic.OutputEvent) {
	switch event.Type {
	case agentic.EventStateChange:
		c.handleStateChange(event)
	case agentic.EventContent:
		c.handleContent(event)
	case agentic.EventToolCall:
		c.handleToolCall(event)
	case agentic.EventToolResult:
		fmt.Fprint(c.writer, event.Text)
	case agentic.EventTokenStats:
		c.handleTokenStats(event)
	case agentic.EventProgress:
		c.handleProgress(event)
	case agentic.EventEnd:
		c.handleEnd()
	case agentic.EventClear:
		c.state = agentic.StateIdle
	}
}

func (c *ConsoleObserver) handleStateChange(event agentic.OutputEvent) {
	if event.State == c.state {
		return
	}
	if c.state != agentic.StateIdle {
		fmt.Fprintln(c.writer)
	}
	c.state = event.State
	c.lastRole = ""
	if prefix := c.format(c.state); prefix != "" {
		fmt.Fprint(c.writer, prefix)
	}
}

func (c *ConsoleObserver) handleContent(event agentic.OutputEvent) {
	if event.Role != c.lastRole && event.Role != "" {
		if c.lastRole != "" {
			fmt.Fprintln(c.writer)
		}
		c.lastRole = event.Role
		if prefix := rolePrefix(event.Role); prefix != "" {
			fmt.Fprint(c.writer, prefix)
		}
	}
	fmt.Fprint(c.writer, event.Text)
}

func (c *ConsoleObserver) handleToolCall(event agentic.OutputEvent) {
	if c.state != agentic.StateIdle {
		fmt.Fprintln(c.writer)
	}
	fmt.Fprintf(c.writer, "[tool_call] %s\n", event.ToolName)
	if event.ToolInput != "" {
		fmt.Fprintf(c.writer, "  input: %s\n", event.ToolInput)
	}
	if event.ToolCallID != "" {
		fmt.Fprintf(c.writer, "  call_id: %s\n", event.ToolCallID)
	}
	c.state = agentic.StateIdle
}

func (c *ConsoleObserver) handleTokenStats(event agentic.OutputEvent) {
	if event.Timings == nil {
		return
	}
	totalTokens := event.Timings.PromptN + event.Timings.PredictedN
	totalTimeSec := (event.Timings.PromptMs + event.Timings.PredictedMs) / 1000.0
	fmt.Fprintf(c.writer, "[stats] %d tokens, %.1fs, %.2f t/s\n",
		totalTokens, totalTimeSec, event.Timings.PredictedPerSecond)
}

func (c *ConsoleObserver) handleProgress(event agentic.OutputEvent) {
	if event.PromptProgress == nil {
		return
	}
	p := event.PromptProgress
	fmt.Fprintf(c.writer, "[progress] %d/%d processed (cache: %d, %dms)\n",
		p.Processed, p.Total, p.Cache, p.TimeMs)
}

func (c *ConsoleObserver) handleEnd() {
	if c.state != agentic.StateIdle {
		fmt.Fprintln(c.writer)
	}
	fmt.Fprintln(c.writer, "[end]")
	c.state = agentic.StateIdle
}
