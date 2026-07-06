// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// OrchestrateBuilder builds a Runtime for one orchestration run. The production
// implementation (internal/app.OrchestratorAdapter) bridges to a real agent
// pool; the interface keeps this package free of internal/app (no import cycle).
type OrchestrateBuilder = orchestrator.Builder

// OrchestrateCommand runs multi-agent orchestrations: per-run topology
// selection (hub/fanout/pipeline), goal binding, listing, resuming, and
// per-target steering. It is the user-facing surface over core/orchestrator.
type OrchestrateCommand struct {
	Builder  OrchestrateBuilder // builds a wired Runtime per run
	Active   *orchestrator.ActiveRuntime
	RootDir  string             // event-store root, typically ".goa/orchestrator"
	GoalMode *goal.GoalMode     // optional; enables goal binding
}

func (c *OrchestrateCommand) Name() string      { return "orchestrate" }
func (c *OrchestrateCommand) Aliases() []string { return []string{"orch"} }
func (c *OrchestrateCommand) ShortHelp() string {
	return "Run a multi-agent orchestration (hub/fanout/pipeline)"
}
func (c *OrchestrateCommand) LongHelp() string { return help.LongHelp(c.Name()) }

// CompleteArgs implements core.ArgCompleter.
func (c *OrchestrateCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var out []core.ArgCompletion
	for _, sc := range orchestrateSubcommands {
		if prefix == "" || strings.HasPrefix(sc.value, prefix) {
			out = append(out, core.ArgCompletion{Value: sc.value, Description: sc.desc})
		}
	}
	return out
}

// Run dispatches to the parsed subcommand. Missing required fields trigger
// interactive prompts instead of hard errors.
func (c *OrchestrateCommand) Run(ctx core.Context, args []string) error {
	if c.Builder == nil || c.Active == nil {
		writeStr(ctx, "Orchestration subsystem not available.\n")
		return nil
	}
	in, err := parseOrchestrateInput(args)
	if err != nil {
		return err
	}
	switch in.Subcommand {
	case "":
		return c.showMenu(ctx)
	case "new":
		return c.runNewInteractive(ctx, in)
	case "list":
		return c.runListInteractive(ctx)
	case "resume":
		return c.runResumeInteractive(ctx, in)
	case "delete":
		return c.runDeleteInteractive(ctx, in)
	case "steer":
		return c.runSteerInteractive(ctx, in)
	default:
		return fmt.Errorf("unknown /orchestrate subcommand: %s", in.Subcommand)
	}
}

func (c *OrchestrateCommand) usage(ctx core.Context) error {
	writeStr(ctx, "Usage:\n")
	writeStr(ctx, "  /orchestrate:new:[topology=hub][,name=alias][,objective=<text>]\n")
	writeStr(ctx, "  /orchestrate:list\n")
	writeStr(ctx, "  /orchestrate:resume:id=<run-id>\n")
	writeStr(ctx, "  /orchestrate:delete:id=<run-id|*>[,confirm=true]\n")
	writeStr(ctx, "  /orchestrate:steer:id=<agent-id|all|orchestrator>,message=<text>\n")
	return nil
}

// --- interactive entry points ------------------------------------------------

func (c *OrchestrateCommand) showMenu(ctx core.Context) error {
	items := []tui.SelectorItem{
		{Value: "new", Label: "new", Description: "start a new orchestration"},
		{Value: "resume", Label: "resume", Description: "resume an existing run"},
		{Value: "delete", Label: "delete", Description: "delete a run or all runs"},
		{Value: "list", Label: "list", Description: "list all runs"},
	}
	ctx.SelectOption("Orchestrate:", items, "", func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		in := OrchestrateInput{Subcommand: selected}
		switch selected {
		case "new":
			c.runNewInteractive(ctx, in)
		case "resume":
			c.runResumeInteractive(ctx, in)
		case "delete":
			c.runDeleteInteractive(ctx, in)
		case "list":
			c.runListInteractive(ctx)
		}
	})
	return nil
}

func (c *OrchestrateCommand) runNewInteractive(ctx core.Context, in OrchestrateInput) error {
	if in.Objective == "" {
		ctx.ShowInput("Objective:", "", func(value string, ok bool) {
			if !ok || value == "" {
				return
			}
			in.Objective = value
			c.runNewInteractive(ctx, in)
		})
		return nil
	}
	return c.doNew(ctx, in)
}

func (c *OrchestrateCommand) runResumeInteractive(ctx core.Context, in OrchestrateInput) error {
	if in.ID == "" {
		runs, err := c.listableRuns()
		if err != nil {
			return err
		}
		items := runSelectorItems(runs, true)
		if len(items) == 0 {
			writeStr(ctx, "No resumable runs found.\n")
			return nil
		}
		ctx.SelectOption("Resume run:", items, "", func(selected string, ok bool) {
			if !ok || selected == "" {
				return
			}
			in.ID = selected
			c.runResumeInteractive(ctx, in)
		})
		return nil
	}
	return c.doResume(ctx, in)
}

func (c *OrchestrateCommand) runDeleteInteractive(ctx core.Context, in OrchestrateInput) error {
	if in.ID == "" {
		runs, err := c.listableRuns()
		if err != nil {
			return err
		}
		items := runSelectorItems(runs, false)
		items = append(items, tui.SelectorItem{
			Value:       "*",
			Label:       "— delete all —",
			Description: "remove every orchestration run",
		})
		if len(items) == 1 {
			writeStr(ctx, "No runs to delete.\n")
			return nil
		}
		ctx.SelectOption("Delete run:", items, "", func(selected string, ok bool) {
			if !ok || selected == "" {
				return
			}
			in.ID = selected
			c.runDeleteInteractive(ctx, in)
		})
		return nil
	}
	if !in.Confirm {
		ctx.SelectOption("Delete "+in.ID+"?", confirmItems(), "no", func(v string, ok bool) {
			if !ok || v != "yes" {
				return
			}
			in.Confirm = true
			c.runDeleteInteractive(ctx, in)
		})
		return nil
	}
	return c.doDelete(ctx, in)
}

func (c *OrchestrateCommand) runListInteractive(ctx core.Context) error {
	runs, err := c.listableRuns()
	if err != nil {
		return err
	}
	items := runSelectorItems(runs, false)
	if len(items) == 0 {
		writeStr(ctx, "No orchestration runs found.\n")
		return nil
	}
	ctx.SelectOption("Orchestration runs:", items, "", func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		c.runResumeInteractive(ctx, OrchestrateInput{Subcommand: "resume", ID: selected})
	})
	return nil
}

func (c *OrchestrateCommand) runSteerInteractive(ctx core.Context, in OrchestrateInput) error {
	if in.ID == "" {
		ctx.ShowInput("Agent ID (or all/orchestrator):", "", func(value string, ok bool) {
			if !ok || value == "" {
				return
			}
			in.ID = value
			c.runSteerInteractive(ctx, in)
		})
		return nil
	}
	if in.Message == "" {
		ctx.ShowInput("Steer "+in.ID+":", "", func(value string, ok bool) {
			if !ok || value == "" {
				return
			}
			in.Message = value
			c.runSteerInteractive(ctx, in)
		})
		return nil
	}
	return c.doSteer(ctx, in)
}

// --- synchronous execution ---------------------------------------------------

func (c *OrchestrateCommand) doNew(ctx core.Context, in OrchestrateInput) error {
	topology, err := normalizeTopology(in.Topology, ctx.Config)
	if err != nil {
		return err
	}
	oCfg := ctx.Config.Orchestrator
	oCfg.Defaults.Topology = topology
	if len(oCfg.Roles) == 0 {
		return fmt.Errorf("no orchestrator.roles configured — define roles in config first")
	}

	rt, err := c.Builder.NewRuntime(oCfg, c.RootDir)
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}

	name := c.resolveRunName(in.Name)
	rt.SetName(name)

	if _, exists := oCfg.Roles["orchestrator"]; !exists && topology == config.OrchestratorTopologyHub {
		writeStr(ctx, "Note: hub topology works best with an 'orchestrator' role; running fanout-style fallback.\n")
	}

	if c.GoalMode != nil {
		goalID, err := c.bindGoal(rt, name, in.Objective)
		if err != nil {
			writeFmt(ctx, "Warning: goal bind failed (%v); running goal-less.\n", err)
		} else {
			rt.SetGoalID(goalID)
		}
	}

	if prev := c.Active.Set(rt); prev != nil {
		// An older run is still active; leave it running but stop surfacing it.
	}

	writeFmt(ctx, "Started orchestration [%s] %s: %s\n", topology, name, in.Objective)
	c.launch(ctx, rt, in.Objective)
	return nil
}

func (c *OrchestrateCommand) doResume(ctx core.Context, in OrchestrateInput) error {
	internalID, err := orchestrator.ResolveRunID(c.RootDir, in.ID)
	if err != nil {
		return err
	}
	store := orchestrator.NewFileEventStore(c.RootDir, internalID)
	snap, err := orchestrator.ReplaySnapshot(store)
	if err != nil {
		return fmt.Errorf("resume %s: %w", internalID, err)
	}
	if snap.Finished {
		writeFmt(ctx, "Run %s is already finished — nothing to resume.\n", internalID)
		return nil
	}
	oCfg := ctx.Config.Orchestrator
	if snap.Topology != "" {
		oCfg.Defaults.Topology = string(snap.Topology)
	}
	rt, err := c.Builder.NewRuntime(oCfg, c.RootDir)
	if err != nil {
		return err
	}
	if snap.Name != "" {
		rt.SetName(snap.Name)
	}
	c.Active.Set(rt)
	writeFmt(ctx, "Resuming %s: %s\n", snap.NameOrID(), snap.Objective)
	c.launch(ctx, rt, snap.Objective)
	return nil
}

func (c *OrchestrateCommand) doDelete(ctx core.Context, in OrchestrateInput) error {
	if in.ID == "*" {
		return c.deleteAll(ctx)
	}
	internalID, err := orchestrator.ResolveRunID(c.RootDir, in.ID)
	if err != nil {
		return err
	}
	store := orchestrator.NewFileEventStore(c.RootDir, internalID)
	snap, _ := orchestrator.ReplaySnapshot(store)
	orchestrator.StopActiveRun(c.Active, internalID, 5*time.Second)
	if err := orchestrator.DeleteRun(c.RootDir, internalID); err != nil {
		return err
	}
	if snap.GoalID != "" && c.GoalMode != nil {
		_, _ = c.GoalMode.CancelGoalByID(snap.GoalID, goal.GoalActorRuntime)
	}
	writeFmt(ctx, "Deleted run %s.\n", internalID)
	return nil
}

func (c *OrchestrateCommand) deleteAll(ctx core.Context) error {
	if rt := c.Active.Get(); rt != nil {
		c.Active.Clear(rt)
		rt.Cancel()
		select {
		case <-rt.Done():
		case <-time.After(5 * time.Second):
		}
	}
	runs, _ := orchestrator.ListRuns(c.RootDir)
	deleted, err := orchestrator.DeleteAllRuns(c.RootDir)
	if err != nil {
		return err
	}
	if c.GoalMode != nil {
		for _, r := range runs {
			if r.GoalID == "" {
				continue
			}
			_, _ = c.GoalMode.CancelGoalByID(r.GoalID, goal.GoalActorRuntime)
		}
	}
	writeFmt(ctx, "Deleted %d run(s).\n", deleted)
	return nil
}

func (c *OrchestrateCommand) doSteer(ctx core.Context, in OrchestrateInput) error {
	rt := c.Active.Get()
	if rt == nil {
		return fmt.Errorf("no active orchestration to steer")
	}
	switch strings.ToLower(in.ID) {
	case "all":
		rt.SteerAll(in.Message)
		writeFmt(ctx, "Broadcast steering to all agents: %s\n", in.Message)
	case "orchestrator", "orch":
		if rt.SteerOrchestrator(in.Message) {
			writeStr(ctx, "Steered orchestrator.\n")
		} else {
			writeStr(ctx, "No orchestrator agent is live right now.\n")
		}
	default:
		if rt.SteerAgent(in.ID, in.Message) {
			writeFmt(ctx, "Steered %s.\n", in.ID)
		} else {
			return fmt.Errorf("no live agent with id %q", in.ID)
		}
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

func (c *OrchestrateCommand) resolveRunName(requested string) string {
	if requested != "" && internal.IsValidRunName(requested) {
		if _, err := orchestrator.ResolveRunID(c.RootDir, requested); err == nil {
			// Collision: fall back to generated name below.
		} else {
			return requested
		}
	}
	return orchestrator.GenerateRunName(c.RootDir)
}

func (c *OrchestrateCommand) listableRuns() ([]orchestrator.RunSummary, error) {
	return orchestrator.ListRuns(c.RootDir)
}

func (c *OrchestrateCommand) bindGoal(rt *orchestrator.Runtime, name, objective string) (string, error) {
	gb := NewGoalBinder(c.GoalMode)
	goalID, err := gb.CreateWithName(objective, name, 0)
	if err != nil {
		return "", err
	}
	rt.SetGoalBinder(gb)
	return goalID, nil
}

func (c *OrchestrateCommand) launch(ctx core.Context, rt *orchestrator.Runtime, objective string) {
	go c.forwardEvents(ctx, rt)
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = rt.Run(runCtx, objective)
		<-rt.Done()
		c.Active.Clear(rt)
	}()
}

func (c *OrchestrateCommand) forwardEvents(ctx core.Context, rt *orchestrator.Runtime) {
	seen := map[string]bool{}
	for ev := range rt.Events() {
		c.handleOrchEvent(ctx, rt, ev, seen)
	}
}

func (c *OrchestrateCommand) handleOrchEvent(ctx core.Context, rt *orchestrator.Runtime, ev orchestrator.Event, seen map[string]bool) {
	switch ev.Type {
	case orchestrator.EventRunStarted:
		ctx.Flash(fmt.Sprintf("▷ orchestration %s started", rt.NameOrID()))
	case orchestrator.EventAgentStarted:
		ctx.InterAgent(ev.Role, "user", fmt.Sprintf("▶ %s (%s) started", ev.Role, ev.Model))
	case orchestrator.EventAgentFinished:
		emitAgentFinished(ctx, ev, seen)
	case orchestrator.EventRunFinished:
		emitRunFinished(ctx, rt, ev)
	}
}

func emitAgentFinished(ctx core.Context, ev orchestrator.Event, seen map[string]bool) {
	if seen[ev.AgentID] {
		return
	}
	seen[ev.AgentID] = true
	outcome := stringValOr(ev.Payload, "outcome", "done")
	ctx.InterAgent(ev.Role, "user", fmt.Sprintf("■ %s %s", ev.Role, outcome))
}

func emitRunFinished(ctx core.Context, rt *orchestrator.Runtime, ev orchestrator.Event) {
	ok := boolValOr(ev.Payload, "ok", true)
	status := "finished"
	if !ok {
		status = "finished with errors"
	}
	ctx.Flash("■ orchestration " + rt.NameOrID() + " " + status)
	for _, r := range rt.Snapshot() {
		writeFmt(ctx, "  %-12s %-8s turns=%d in=%d out=%d tools=%d\n",
			r.Role, r.Status, r.Turns, r.TokensIn, r.TokensOut, r.ToolCalls)
	}
}

func stringValOr(p map[string]any, k, fallback string) string {
	if v, ok := p[k].(string); ok {
		return v
	}
	return fallback
}

func boolValOr(p map[string]any, k string, fallback bool) bool {
	if v, ok := p[k].(bool); ok {
		return v
	}
	return fallback
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// --- selector builders -------------------------------------------------------

var orchestrateSubcommands = []struct {
	value, desc string
}{
	{"new", "start a new run: /orchestrate:new:[topology=...][,name=...][,objective=...]"},
	{"list", "list all runs"},
	{"resume", "resume a run: /orchestrate:resume:id=<run-id>"},
	{"delete", "delete: /orchestrate:delete:id=<run-id|*>[,confirm=true]"},
	{"steer", "steer: /orchestrate:steer:id=<agent-id|all|orchestrator>,message=<text>"},
}

func runSelectorItems(runs []orchestrator.RunSummary, onlyUnfinished bool) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, len(runs))
	for _, r := range runs {
		if onlyUnfinished && r.Finished {
			continue
		}
		label := r.NameOrID()
		desc := fmt.Sprintf("%s %s  agents=%d  %s", r.Topology, statusLabel(r.Finished), r.AgentCount, truncStr(r.Objective, 40))
		items = append(items, tui.SelectorItem{Value: r.RunID, Label: label, Description: desc})
	}
	return items
}

func confirmItems() []tui.SelectorItem {
	return []tui.SelectorItem{
		{Value: "yes", Label: "Yes", Description: "confirm deletion"},
		{Value: "no", Label: "No", Description: "cancel"},
	}
}

func statusLabel(finished bool) string {
	if finished {
		return "finished"
	}
	return "running"
}
