// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// AgentCommand drives the multi-agent tab surface (the per-delegation
// agentctx tabs): explicit tab selection (/agent:tab:<id>) and a deliberate
// full-history scrollback replay of the ACTIVE tab (/agent:replay). It
// mirrors the /orchestrate:tab subcommand pattern but is source-agnostic —
// it talks to whatever the host binds, never to internal/app types.
//
// The host (internal/app) supplies the callbacks at registration time; a nil
// callback makes the corresponding subcommand report unavailability instead
// of failing.
type AgentCommand struct {
	// SelectTab makes the tab matching idOrLabel (a registry id such as
	// "dlg-coder-03" or its display label "coder·dlg-03") active and returns
	// the display label. ok=false ⇒ unknown tab.
	SelectTab func(idOrLabel string) (label string, ok bool)
	// ActiveTabs lists the selectable tabs as display labels in tab order,
	// used to make unknown-tab errors actionable.
	ActiveTabs func() []string
	// ReplayActiveTab starts a deliberate full-history replay of the active
	// tab's transcript into terminal scrollback (ReplayRunner path) and
	// returns that tab's label. started=false ⇒ replay unavailable or
	// nothing to replay.
	ReplayActiveTab func() (label string, started bool)
}

func (c *AgentCommand) Name() string      { return "agent" }
func (c *AgentCommand) Aliases() []string { return []string{} }
func (c *AgentCommand) ShortHelp() string {
	return "Multi-agent tabs: select a delegation tab (/agent:tab:<id>) or replay its history (/agent:replay)"
}
func (c *AgentCommand) LongHelp() string { return help.LongHelp(c.Name()) }

// agentSubcommand is one /agent verb with its usage line.
type agentSubcommand struct {
	name  string
	usage string
	desc  string
}

// agentSubcommands is the ordered list accepted by Run (and completed).
var agentSubcommands = []agentSubcommand{
	{"tab", "/agent:tab:<id|label>", "switch to a multi-agent tab by delegation id or label"},
	{"replay", "/agent:replay", "re-emit the active tab's full history into scrollback"},
}

// CompleteArgs implements core.ArgCompleter: it offers the subcommands and,
// after "tab", the host's known tabs.
func (c *AgentCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	var out []core.ArgCompletion
	for _, sc := range agentSubcommands {
		if strings.HasPrefix(sc.name, prefix) {
			out = append(out, core.ArgCompletion{Value: sc.name + ":", Description: sc.desc})
		}
	}
	if c.ActiveTabs != nil && (prefix == "" || strings.HasPrefix("tab", prefix)) {
		for _, label := range c.ActiveTabs() {
			out = append(out, core.ArgCompletion{Value: "tab:" + label, Description: "switch to this tab"})
		}
	}
	return out
}

// Run dispatches a /agent colon command. The first argument is the
// subcommand; "tab" takes the tab reference as the next argument
// (/agent:tab:<id>), mirroring the orchestration :tab: pattern.
func (c *AgentCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		flashStr(ctx, c.usage())
		return nil
	}
	switch args[0] {
	case "tab":
		c.runTab(ctx, joinTabRef(args[1:]))
	case "replay":
		c.runReplay(ctx)
	default:
		flashFmt(ctx, "Unknown /agent subcommand %q.\n%s", args[0], c.usage())
	}
	return nil
}

// runTab selects one tab. An unknown id produces an actionable flash error
// listing every known tab so the user can pick a valid one directly.
func (c *AgentCommand) runTab(ctx core.Context, ref string) {
	if c.SelectTab == nil {
		flashStr(ctx, "Tab navigation not available (no multi-agent session).\n")
		return
	}
	if ref == "" {
		flashFmt(ctx, "Usage: /agent:tab:<id|label>\n%s\n", c.knownTabsLine())
		return
	}
	label, ok := c.SelectTab(ref)
	if !ok {
		flashFmt(ctx, "Unknown tab %q. %s\n", ref, c.knownTabsLine())
		return
	}
	flashFmt(ctx, "tab: %s\n", label)
}

// runReplay triggers the deliberate full-history replay of the active tab.
func (c *AgentCommand) runReplay(ctx core.Context) {
	if c.ReplayActiveTab == nil {
		flashStr(ctx, "Replay not available (no TUI session).\n")
		return
	}
	label, started := c.ReplayActiveTab()
	if !started {
		flashStr(ctx, "Replay unavailable: enable features.multi_agent_scrollback_replay and open a tab with history.\n")
		return
	}
	flashFmt(ctx, "replaying %s history into scrollback…\n", label)
}

// usage renders the subcommand help block.
func (c *AgentCommand) usage() string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	for _, sc := range agentSubcommands {
		fmt.Fprintf(&b, "  %-22s %s\n", sc.usage, sc.desc)
	}
	return b.String()
}

// knownTabsLine renders the actionable part of tab errors: the host's known
// tabs, or a hint when none are selectable.
func (c *AgentCommand) knownTabsLine() string {
	if c.ActiveTabs == nil {
		return "No tabs are currently available."
	}
	tabs := c.ActiveTabs()
	if len(tabs) == 0 {
		return "No tabs are currently available."
	}
	return "Known tabs: " + strings.Join(tabs, ", ")
}

// joinTabRef rejoins the remaining colon-split fragments: ids contain dashes,
// never colons, but a stray extra colon should still degrade gracefully.
func joinTabRef(parts []string) string {
	return strings.TrimSpace(strings.Join(parts, ":"))
}
