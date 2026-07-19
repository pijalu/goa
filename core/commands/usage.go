// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/usage"
)

// UsageCommand implements /usage: a cross-session usage view (like
// opencode-stats) read from the global SQLite store. Supports global,
// per-project, per-provider, and per-model aggregations.
type UsageCommand struct {
	// OpenStore opens the usage store; nil uses usage.Open(usage.DefaultPath()).
	// Injectable for tests.
	OpenStore func() (usageStore, error)
	// ProjectDir is the current project; used to label the per-project view.
	ProjectDir string
}

// usageStore is the subset of *usage.Store the command needs (for tests).
type usageStore interface {
	Query(dim usage.Dimension, project string) ([]usage.Stat, error)
	Sum(project string) (usage.Stat, error)
	Close() error
}

func (c *UsageCommand) Name() string      { return "usage" }
func (c *UsageCommand) IsInternal() bool  { return false }
func (c *UsageCommand) Aliases() []string { return nil }
func (c *UsageCommand) ShortHelp() string {
	return "Show cumulative usage stats (global, per-project, per-provider, per-model)"
}
func (c *UsageCommand) LongHelp() string { return help.LongHelp(c.Name()) }
func (c *UsageCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	return nil
}

func (c *UsageCommand) Run(ctx core.Context, args []string) error {
	open := c.OpenStore
	if open == nil {
		open = defaultStoreOpener
	}
	st, err := open()
	if err != nil {
		ctx.Writef("usage: cannot open store: %v\n", err)
		return nil
	}
	defer st.Close()

	project := c.ProjectDir
	if project == "" {
		project = ctx.ProjectDir
	}

	scope := ""
	if len(args) > 0 {
		scope = args[0]
	}

	var b strings.Builder
	switch scope {
	case "", "all":
		writeUsageGlobal(&b, st)
		writeUsageSection(&b, st, usage.ByProject, "", "By project")
		writeUsageSection(&b, st, usage.ByProvider, "", "By provider")
		writeUsageSection(&b, st, usage.ByModel, "", "By model")
	case "project":
		writeUsageSection(&b, st, usage.ByProject, "", "By project")
	case "provider":
		writeUsageSection(&b, st, usage.ByProvider, "", "By provider")
	case "model":
		writeUsageSection(&b, st, usage.ByModel, "", "By model")
	case "here", "this":
		writeUsageGlobalFor(&b, st, project)
		writeUsageSection(&b, st, usage.ByProvider, project, "By provider (this project)")
		writeUsageSection(&b, st, usage.ByModel, project, "By model (this project)")
	default:
		b.WriteString("Unknown /usage scope: " + scope + "\n")
		b.WriteString("Usage: /usage[:all|:project|:provider|:model|:here]\n")
	}
	ctx.Writef("%s", b.String())
	return nil
}

func defaultStoreOpener() (usageStore, error) {
	p, err := usage.DefaultPath()
	if err != nil {
		return nil, err
	}
	return usage.Open(p)
}

func writeUsageGlobal(b *strings.Builder, st usageStore) {
	writeUsageGlobalFor(b, st, "")
}

func writeUsageGlobalFor(b *strings.Builder, st usageStore, project string) {
	sum, err := st.Sum(project)
	if err != nil {
		fmt.Fprintf(b, "usage: %v\n", err)
		return
	}
	label := "Global usage (all projects)"
	if project != "" {
		label = "Usage for " + project
	}
	fmt.Fprintf(b, "## %s\n\n", label)
	if sum.Turns == 0 {
		b.WriteString("No usage recorded yet.\n\n")
		return
	}
	fmt.Fprintf(b, "Turns: %d   Input: %s   Output: %s   Total: %s\n\n",
		sum.Turns, humanTokens(sum.PromptN), humanTokens(sum.PredictedN), humanTokens(sum.Total()))
}

func writeUsageSection(b *strings.Builder, st usageStore, dim usage.Dimension, project, title string) {
	rows, err := st.Query(dim, project)
	if err != nil {
		fmt.Fprintf(b, "usage: %v\n", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n", title)
	b.WriteString("| Name | Turns | Input | Output | Total |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: |\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %d | %s | %s | %s |\n",
			r.Key, r.Turns, humanTokens(r.PromptN), humanTokens(r.PredictedN), humanTokens(r.Total()))
	}
	b.WriteString("\n")
}

// humanTokens formats a token count compactly (1.2K, 3.4M).
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
