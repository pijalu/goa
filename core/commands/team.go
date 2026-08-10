// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/team"
	"github.com/pijalu/goa/tui"
)

// TeamCommand selects and manages agent teams (TEAMS.md §8.1). It behaves
// like /model: bare /team opens an interactive team selector; /team:<name>
// switches directly with persistence and a footer refresh.
type TeamCommand struct{}

func (c *TeamCommand) Name() string      { return "team" }
func (c *TeamCommand) Aliases() []string { return []string{} }
func (c *TeamCommand) ShortHelp() string {
	return "Select or manage the active agent team (main/companion pairing)"
}

func (c *TeamCommand) LongHelp() string {
	return `/team behaves like /model for agent teams.

Usage:
  /team                 Open the interactive team selector (— none — deactivates)
  /team:<name>          Activate a team directly
  /team:off             Deactivate the active team (restores prior model/companion)
  /team:status          Show the active team, drift state, and bindings
  /team:list            List defined teams
  /team:sync            Re-apply the active team definition (clears drift)
  /team:show:<name>     Show a team definition

Teams are defined under teams.definitions in the config (see /config → Teams).`
}

func (c *TeamCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	add := func(val, desc string) {
		if strings.HasPrefix(val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: val, Description: desc})
		}
	}
	if ctx.Config != nil {
		for _, name := range ctx.Config.TeamNames() {
			add(name, "activate team")
			add("show:"+name, "show team definition")
			add("use:"+name, "activate team (alias)")
		}
	}
	add("off", "deactivate the active team")
	add("status", "show active team status")
	add("list", "list defined teams")
	add("sync", "re-apply the active team (clear drift)")
	return comps
}

// teamManager extracts the *team.Manager from the context (declared as any
// to avoid the core→team import cycle).
func teamManager(ctx core.Context) *team.Manager {
	if m, ok := ctx.TeamManager.(*team.Manager); ok {
		return m
	}
	return nil
}

func (c *TeamCommand) Run(ctx core.Context, args []string) error {
	m := teamManager(ctx)
	if m == nil {
		writeStr(ctx, "Teams: unavailable (no team manager)\n")
		return nil
	}
	if len(args) == 0 {
		return showTeamSelector(ctx, m)
	}
	arg := args[0]
	switch {
	case arg == "off":
		return teamOff(ctx, m)
	case arg == "status":
		return teamStatus(ctx, m)
	case arg == "list":
		return teamList(ctx)
	case arg == "sync":
		return teamSync(ctx, m)
	case strings.HasPrefix(arg, "show:"):
		return teamShow(ctx, strings.TrimPrefix(arg, "show:"))
	case strings.HasPrefix(arg, "use:"):
		return teamActivate(ctx, m, strings.TrimPrefix(arg, "use:"))
	default:
		return teamActivate(ctx, m, arg)
	}
}

// showTeamSelector opens the /model-style interactive picker.
func showTeamSelector(ctx core.Context, m *team.Manager) error {
	names := ctx.Config.TeamNames()
	if len(names) == 0 {
		writeStr(ctx, "No teams defined. Add one under teams.definitions in the config or via /config → Teams.\n")
		return nil
	}
	items := make([]tui.SelectorItem, 0, len(names)+1)
	for _, name := range names {
		def, _ := m.Resolve(name)
		items = append(items, tui.SelectorItem{
			Value:       name,
			Label:       name,
			Description: teamOneLiner(def),
		})
	}
	items = append(items, tui.SelectorItem{Value: "__none__", Label: "— none —", Description: "deactivate the active team"})
	active := m.Active()
	ctx.SelectOption("Select team:", items, active, func(selected string, ok bool) {
		if !ok {
			return
		}
		if selected == "__none__" {
			_ = teamOff(ctx, m)
			return
		}
		_ = teamActivate(ctx, m, selected)
	})
	return nil
}

// teamActivate applies a team to the session (like /model:<id>).
func teamActivate(ctx core.Context, m *team.Manager, name string) error {
	if err := m.Activate(name); err != nil {
		writeFmt(ctx, "Cannot switch to team %s: %v\n", name, err)
		return nil
	}
	persistActiveTeam(ctx, name)
	ctx.FooterRefresh()
	def, _ := m.Resolve(name)
	writeFmt(ctx, "Team active: %s (%s)\n", name, teamOneLiner(def))
	return nil
}

// teamOff deactivates the active team, restoring the pre-team session state.
func teamOff(ctx core.Context, m *team.Manager) error {
	if m.Active() == "" && m.OverlayTeam() == "" {
		writeStr(ctx, "No team active.\n")
		return nil
	}
	if err := m.Deactivate(); err != nil {
		writeFmt(ctx, "Cannot deactivate team: %v\n", err)
		return nil
	}
	persistActiveTeam(ctx, "")
	ctx.FooterRefresh()
	writeStr(ctx, "Team deactivated (prior model/companion restored).\n")
	return nil
}

// teamSync re-applies the active team definition, clearing drift.
func teamSync(ctx core.Context, m *team.Manager) error {
	if err := m.Sync(); err != nil {
		writeFmt(ctx, "Team sync: %v\n", err)
		return nil
	}
	ctx.FooterRefresh()
	writeFmt(ctx, "Team re-applied: %s (drift cleared)\n", m.EffectiveTeam())
	return nil
}

// teamStatus renders the active team, drift marker, and bindings.
func teamStatus(ctx core.Context, m *team.Manager) error {
	active := m.Active()
	overlay := m.OverlayTeam()
	if active == "" && overlay == "" {
		writeStr(ctx, "No team active. Defined teams: "+strings.Join(ctx.Config.TeamNames(), ", ")+"\n")
		return nil
	}
	if active != "" {
		def, _ := m.Resolve(active)
		line := fmt.Sprintf("Team: %s (%s)", active, teamOneLiner(def))
		if m.Drifted() {
			line += " *drifted — /team:sync to re-apply"
		}
		writeStr(ctx, line+"\n")
	}
	if overlay != "" {
		writeFmt(ctx, "Goal overlay: %s (governs until the bound goal ends)\n", overlay)
	}
	return nil
}

// teamList renders all defined teams (non-interactive).
func teamList(ctx core.Context) error {
	names := ctx.Config.TeamNames()
	if len(names) == 0 {
		writeStr(ctx, "No teams defined.\n")
		return nil
	}
	var b strings.Builder
	b.WriteString("Defined teams:\n")
	for _, name := range names {
		def := ctx.Config.Teams.Definitions[name]
		b.WriteString(fmt.Sprintf("  %s — %s\n", name, teamOneLiner(def)))
	}
	writeStr(ctx, b.String())
	return nil
}

// teamShow renders one team definition with resolved members.
func teamShow(ctx core.Context, name string) error {
	def, ok := ctx.Config.Teams.Definitions[name]
	if !ok {
		writeFmt(ctx, "Team %q not defined (defined: %s)\n", name, strings.Join(ctx.Config.TeamNames(), ", "))
		return nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Team %s", name))
	if def.Description != "" {
		b.WriteString(fmt.Sprintf(" — %s", def.Description))
	}
	b.WriteString(fmt.Sprintf("\n  review: %s", def.EffectiveReview()))
	if def.EffectiveReview() == config.TeamReviewGated {
		b.WriteString(fmt.Sprintf(" (triggers: %s, quorum: %s)", strings.Join(def.ReviewGates.Triggers, ","), def.EffectiveQuorum()))
	}
	b.WriteString("\n")
	members, err := def.ResolvedMembers()
	if err != nil {
		b.WriteString("  <invalid definition: " + err.Error() + ">\n")
	} else {
		for _, rm := range members {
			b.WriteString(fmt.Sprintf("  %s (%s): model=%s", rm.Name, rm.Member.Role, rm.Member.Model))
			if rm.Member.Provider != "" {
				b.WriteString(" provider=" + rm.Member.Provider)
			}
			if rm.Member.Mode != "" {
				b.WriteString(" mode=" + rm.Member.Mode)
			}
			if rm.Member.ThinkingLevel != "" {
				b.WriteString(" thinking=" + rm.Member.ThinkingLevel)
			}
			b.WriteString("\n")
		}
	}
	writeStr(ctx, b.String())
	return nil
}

// teamOneLiner renders the compact description used in selectors and the
// footer: main/companion models + review policy.
func teamOneLiner(def config.TeamDefinition) string {
	main, hasMain := def.MainMember()
	reviewers := def.Reviewers()
	var b strings.Builder
	if hasMain {
		b.WriteString(main.Member.Model)
		if main.Member.ThinkingLevel != "" {
			b.WriteString("/" + main.Member.ThinkingLevel)
		}
	}
	if len(reviewers) > 0 {
		b.WriteString(" + ")
		names := make([]string, 0, len(reviewers))
		for _, r := range reviewers {
			s := r.Member.Model
			if r.Member.ThinkingLevel != "" {
				s += "/" + r.Member.ThinkingLevel
			}
			names = append(names, s)
		}
		sort.Strings(names)
		b.WriteString(strings.Join(names, ","))
	}
	review := def.EffectiveReview()
	if review == "" {
		review = "none"
	}
	b.WriteString(" · review:" + review)
	return b.String()
}

// persistActiveTeam records teams.active in the home config so the selection
// survives restarts (like /model persisting active_model).
func persistActiveTeam(ctx core.Context, name string) {
	if ctx.Config == nil || ctx.ConfigSaver == nil {
		return
	}
	ctx.Config.Teams.Active = name
	if err := ctx.ConfigSaver.SaveHomeFieldValue([]string{"teams", "active"}, name); err != nil {
		ctx.Flash("Failed to persist teams.active: " + err.Error())
	}
}
