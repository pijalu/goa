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
	"github.com/pijalu/goa/multiagent"
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
  /team                 Open the interactive team selector (— none — deactivates, — add team — creates)
  /team:<name>          Activate a team directly
  /team:add             Create a team via the add-team wizard (like /config → Teams)
  /team:remove:<name>   Remove a team (refused while it is the active team)
  /team:off             Deactivate the active team (restores prior model/companion)
  /team:status          Show the active team, drift state, and bindings
  /team:list            List defined teams
  /team:sync            Re-apply the active team definition (clears drift)
  /team:show:<name>     Show a team definition
  /team:run:<name>      Run the team's ordered member workflow (architect → coder → reviewer, with loop_back_to feedback) on a task

Teams may define a workflow: list of member stages run in order; a stage with
loop_back_to: <earlier member> forms a feedback loop (reviewer <-> coder) bounded
by max_iterations. See /team:show:<name> for a team's workflow.

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
			add("remove:"+name, "remove team")
			if def, ok := ctx.Config.Teams.Definitions[name]; ok && def.HasWorkflow() {
				add("run:"+name, "run team workflow (ordered members + loops)")
			}
		}
	}
	add("add", "create a team (wizard)")
	add("off", "deactivate the active team")
	add("status", "show active team status")
	add("list", "list defined teams")
	add("sync", "re-apply the active team (clear drift)")
	add("run", "run the team's ordered workflow on a task")
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
	// The command router splits on every ':' (e.g. /team:remove:beta →
	// ["remove","beta"]), so a team name containing spaces arrives as a single
	// trailing arg while a sub-command may precede it. Reassemble the full
	// argument, then split off a leading sub-command keyword.
	arg := strings.Join(args, ":")
	sub, rest := splitTeamArg(arg)

	// Manager-free subcommands (like /model add): listing, adding, and
	// removing definitions only need the config, not an active team manager.
	switch sub {
	case "add":
		return teamAdd(ctx)
	case "list":
		return teamList(ctx)
	case "remove":
		return teamRemove(ctx, teamManager(ctx), rest, false)
	}

	m := teamManager(ctx)
	if m == nil {
		writeStr(ctx, "Teams: unavailable (no team manager)\n")
		return nil
	}
	if arg == "" {
		return showTeamSelector(ctx, m)
	}
	return teamManagedDispatch(ctx, m, sub, rest)
}

// splitTeamArg splits a reassembled /team argument into a leading sub-command
// keyword and the remaining team name. It recognizes the explicit
// "sub:name" form (e.g. "remove:My Team") and, for backward compatibility,
// the bare sub-command alone. When the whole argument is not a known
// sub-command it is treated as a team name (default activation).
func splitTeamArg(arg string) (sub, rest string) {
	if i := strings.Index(arg, ":"); i >= 0 {
		if kw := arg[:i]; isTeamSubCommand(kw) {
			return kw, arg[i+1:]
		}
		return "", arg
	}
	if isTeamSubCommand(arg) {
		return arg, ""
	}
	return "", arg
}

// isTeamSubCommand reports whether s is a /team sub-command keyword.
func isTeamSubCommand(s string) bool {
	switch s {
	case "add", "list", "remove", "off", "status", "sync", "show", "use", "run":
		return true
	}
	return false
}

// teamManagedDispatch runs the subcommands that operate on the active session
// team (they require a team manager).
func teamManagedDispatch(ctx core.Context, m *team.Manager, sub, rest string) error {
	switch sub {
	case "off":
		return teamOff(ctx, m)
	case "status":
		return teamStatus(ctx, m)
	case "sync":
		return teamSync(ctx, m)
	case "show":
		return teamShow(ctx, rest)
	case "use":
		return teamActivate(ctx, m, rest)
	case "run":
		return teamRun(ctx, m, rest)
	default:
		// No recognized sub-command: rest holds the whole argument (the team
		// name), per splitTeamArg's "", arg return.
		return teamActivate(ctx, m, rest)
	}
}

// teamAdd opens the add-team wizard (the same flow as /config → Teams →
// definitions → — add team —), so a team can be created without leaving the
// /team command (mirrors /model add).
func teamAdd(ctx core.Context) error {
	menu := newConfigMenu(ctx)
	menu.open(menu.addTeamWizard)
	return nil
}

// teamRemove deletes a team definition after confirmation (mirrors /model
// remove). Removing the active team is refused — deactivate it first.
func teamRemove(ctx core.Context, m *team.Manager, name string, fromSelector bool) error {
	if name == "" {
		writeStr(ctx, "Usage: /team:remove:<name>\n")
		return nil
	}
	cfg := ctx.Config
	if _, ok := cfg.Teams.Definitions[name]; !ok {
		writeFmt(ctx, "Team %q not defined (defined: %s)\n", name, strings.Join(cfg.TeamNames(), ", "))
		return nil
	}
	active := cfg.Teams.Active
	if active == name && !fromSelector {
		writeFmt(ctx, "Cannot remove the active team %q — deactivate it first (/team:off).\n", name)
		return nil
	}
	// Interactive deletion still requires confirmation for the active team;
	// clear the active selection after confirmation rather than silently
	// refusing the selector action.
	items := []tui.SelectorItem{
		{Value: "yes", Label: "Remove " + name},
		{Value: "no", Label: "Cancel"},
	}
	ctx.SelectOption("Remove team "+name+"?", items, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			return
		}
		delete(cfg.Teams.Definitions, name)
		if active == name {
			if m != nil && m.Active() == name {
				_ = m.Deactivate()
			}
			// Persist the cleared selection to the project local layer —
			// without this the stale teams.active resurfaces on next start
			// and fails validation (teams.active must name a defined team).
			persistActiveTeam(ctx, "")
		}
		persistTeamsDefinitions(ctx)
		writeFmt(ctx, "Team removed: %s\n", name)
	})
	return nil
}

// persistTeamsDefinitions writes teams.definitions back to the home config.
func persistTeamsDefinitions(ctx core.Context) {
	if ctx.ConfigSaver == nil {
		return
	}
	if err := ctx.ConfigSaver.SaveHomeFieldValue([]string{"teams", "definitions"}, ctx.Config.Teams.Definitions); err != nil {
		ctx.Flash("Failed to persist teams.definitions: " + err.Error())
	}
}

// showTeamSelector opens the /model-style interactive picker.
func showTeamSelector(ctx core.Context, m *team.Manager) error {
	names := ctx.Config.TeamNames()
	if len(names) == 0 {
		// No teams yet: offer to create one straight away (like /model's empty
		// picker routes to — add —), rather than a dead-end message.
		items := []tui.SelectorItem{{Value: "__add__", Label: "— add team —", Description: "create your first team (wizard)"}}
		ctx.SelectOption("No teams defined:", items, "", func(selected string, ok bool) {
			if ok && selected == "__add__" {
				_ = teamAdd(ctx)
			}
		})
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
	items = append(items, tui.SelectorItem{Value: "__add__", Label: "— add team —", Description: "create a new team (wizard)"})
	items = append(items, tui.SelectorItem{Value: "__none__", Label: "— none —", Description: "deactivate the active team"})
	active := m.Active()
	ctx.SelectOption("Select team:", items, active, func(selected string, ok bool) {
		if !ok {
			return
		}
		switch selected {
		case "__none__":
			_ = teamOff(ctx, m)
		case "__add__":
			_ = teamAdd(ctx)
		default:
			if name, deleted := strings.CutPrefix(selected, "__delete__"); deleted {
				_ = teamRemove(ctx, m, name, true)
				return
			}
			_ = teamActivate(ctx, m, selected)
		}
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

// teamRun executes a team's ordered member workflow (bugs.md "team: member
// order / workflow"). It builds a pipeline from the team's workflow stages —
// each stage runs its member in order, and a stage with loop_back_to forms a
// feedback loop (e.g. reviewer ⇄ coder) — and runs it on the foreground
// orchestrator. The team must be activated first so its members are in the
// agent pool; the user is prompted for the task when none is supplied.
//
// Usage: /team:run:<name>  (name optional when a team is already active)
func teamRun(ctx core.Context, m *team.Manager, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = m.EffectiveTeam()
	}
	if name == "" {
		writeStr(ctx, "No team active. Usage: /team:run:<name> (or activate one with /team:<name>)\n")
		return nil
	}
	def, ok := ctx.Config.Teams.Definitions[name]
	if !ok {
		writeFmt(ctx, "Team %q not defined (defined: %s)\n", name, strings.Join(ctx.Config.TeamNames(), ", "))
		return nil
	}
	if !def.HasWorkflow() {
		writeFmt(ctx, "Team %q has no workflow. Add a `workflow:` list to its definition (ordered members, optional loop_back_to) to define the execution order.\n", name)
		return nil
	}
	if ctx.ForegroundOrchestrator == nil {
		writeStr(ctx, "Orchestrator not available.\n")
		return nil
	}
	// Activate the team so its members are registered in the agent pool before
	// the workflow references them (idempotent when already active).
	if m.EffectiveTeam() != name {
		if err := m.Activate(name); err != nil {
			writeFmt(ctx, "Cannot activate team %s: %v\n", name, err)
			return nil
		}
		persistActiveTeam(ctx, name)
		ctx.FooterRefresh()
	}
	pipeline, err := teamWorkflowPipeline(name, def)
	if err != nil {
		writeFmt(ctx, "Team %q workflow invalid: %v\n", name, err)
		return nil
	}
	// Prompt for the task, then run.
	ctx.ShowInput("Task for team "+name+" workflow:", "", func(task string, okInput bool) {
		if !okInput || strings.TrimSpace(task) == "" {
			return
		}
		go runTeamWorkflow(ctx, name, pipeline, task)
	})
	return nil
}

// runTeamWorkflow runs the built pipeline on the foreground orchestrator and
// reports completion/failure as an inter-agent message.
func runTeamWorkflow(ctx core.Context, name string, p *multiagent.Pipeline, task string) {
	ctx.InterAgent("system", "user", "Team workflow "+name+": running "+teamWorkflowSummary(p)+"…")
	if err := ctx.ForegroundOrchestrator.RunPipeline(ctx.ForegroundOrchestrator.Context(), p, task); err != nil {
		ctx.InterAgent("system", "user", fmt.Sprintf("Team workflow %s error: %v", name, err))
		return
	}
	ctx.InterAgent("system", "user", "Team workflow "+name+" complete.")
}

// teamWorkflowPipeline builds a *multiagent.Pipeline from a team definition's
// ordered workflow. Each stage's Agent is the member name (a pool role set at
// activation); a stage with loop_back_to becomes a Loop back-edge. Stage order
// is the workflow list order — the requested member order.
func teamWorkflowPipeline(teamName string, def config.TeamDefinition) (*multiagent.Pipeline, error) {
	if err := def.ValidateWorkflow(); err != nil {
		return nil, err
	}
	p := &multiagent.Pipeline{
		ID:   "team:" + teamName,
		Name: "Team " + teamName + " workflow",
	}
	for _, s := range def.Workflow {
		prompt := s.Prompt
		if prompt == "" {
			prompt = "{{.UserInput}}" // pass the task through when no stage prompt
		}
		p.Stages = append(p.Stages, multiagent.PipelineStage{
			ID:     s.Member,
			Name:   s.Member,
			Agent:  s.Member,
			Prompt: prompt,
			Loop: multiagent.LoopConfig{
				LoopBackTo:    s.LoopBackTo,
				MaxIterations: s.MaxIterations,
			},
		})
	}
	return p, nil
}

// teamWorkflowSummary renders the ordered member chain for the chat header,
// marking loop-backs (e.g. "architect → coder → reviewer⇄coder").
func teamWorkflowSummary(p *multiagent.Pipeline) string {
	var b strings.Builder
	for i, s := range p.Stages {
		if i > 0 {
			b.WriteString(" → ")
		}
		b.WriteString(s.Agent)
		if s.Loop.LoopBackTo != "" {
			b.WriteString("⇄" + s.Loop.LoopBackTo)
		}
	}
	return b.String()
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
	writeTeamMembers(&b, def)
	writeTeamWorkflow(&b, name, def)
	writeStr(ctx, b.String())
	return nil
}

// writeTeamMembers renders the resolved member list for /team:show.
func writeTeamMembers(b *strings.Builder, def config.TeamDefinition) {
	members, err := def.ResolvedMembers()
	if err != nil {
		b.WriteString("  <invalid definition: " + err.Error() + ">\n")
		return
	}
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

// writeTeamWorkflow renders the ordered member workflow (with loop-backs) for
// /team:show, plus the /team:run hint.
func writeTeamWorkflow(b *strings.Builder, name string, def config.TeamDefinition) {
	if !def.HasWorkflow() {
		return
	}
	b.WriteString("  workflow (run with /team:run:" + name + "):\n")
	for i, s := range def.Workflow {
		b.WriteString(fmt.Sprintf("    %d. %s", i+1, s.Member))
		if s.LoopBackTo != "" {
			b.WriteString(fmt.Sprintf(" ⇄ %s (max %d)", s.LoopBackTo, s.MaxIterations))
		}
		if s.Prompt != "" {
			b.WriteString(" — " + s.Prompt)
		}
		b.WriteString("\n")
	}
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

// persistActiveTeam records teams.active in the project local layer
// (.goa/config.local.yaml — gitignored, per-developer) so the selection
// survives restarts without leaking across projects (home layer) or dirtying
// the committed project config. Unlike /model's active_model (a global
// default persisted to home by design), a team is a project-scoped working
// set. The startup cascade (home → project → local) resolves the value with
// no resolution-order change: the most specific layer wins.
func persistActiveTeam(ctx core.Context, name string) {
	if ctx.Config == nil || ctx.ConfigSaver == nil {
		return
	}
	ctx.Config.Teams.Active = name
	if err := ctx.ConfigSaver.SaveLocalFieldValue([]string{"teams", "active"}, name); err != nil {
		ctx.Flash("Failed to persist teams.active: " + err.Error())
	}
}
