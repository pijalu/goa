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
	RootDir  string         // event-store root, typically ".goa/orchestrator"
	GoalMode *goal.GoalMode // optional; enables goal binding
	// ShowBrowser is set by the TUI host to open the dedicated orchestrator
	// browser overlay. When nil, /orchestrate:browser returns an error.
	ShowBrowser func()
	// SelectAgentTab selects a tab of the persistent multi-agent run view by
	// key (or 1-based index) and returns the selected tab's label. Set by the
	// TUI host; when nil, /orchestrate:tab returns an error. Named generically
	// so pipeline/swarm reuse it later.
	SelectAgentTab func(key string) (label string, ok bool)
}

func (c *OrchestrateCommand) Name() string      { return "orchestrate" }
func (c *OrchestrateCommand) Aliases() []string { return []string{"orch"} }
func (c *OrchestrateCommand) ShortHelp() string {
	return "Run a multi-agent orchestration (hub/fanout/pipeline)"
}
func (c *OrchestrateCommand) LongHelp() string { return help.LongHelp(c.Name()) }

// flashFmt flashes a formatted status message to the user. Use this for
// messages that must be visible after an asynchronous callback returns.
func flashFmt(ctx core.Context, format string, args ...interface{}) {
	ctx.Flash(fmt.Sprintf(format, args...))
}

// flashStr flashes a literal status message to the user. Use this for
// messages that must be visible after an asynchronous callback returns.
func flashStr(ctx core.Context, s string) {
	ctx.Flash(s)
}

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
	case "tab":
		return c.runTab(ctx, in)
	case "browser":
		return c.runBrowser(ctx)
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
	writeStr(ctx, "  /orchestrate:tab:<key|index>      Switch the orchestration view tab (stats/all/<agent>).\n")
	writeStr(ctx, "  /orchestrate:browser            Open the dedicated run browser overlay.\n")
	return nil
}

// --- interactive entry points ------------------------------------------------

func isInteractive(ctx core.Context) bool {
	return ctx.SelectOptionFunc != nil && ctx.ShowInputFunc != nil
}

func (c *OrchestrateCommand) showMenu(ctx core.Context) error {
	if !isInteractive(ctx) {
		return c.usage(ctx)
	}
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
		if !isInteractive(ctx) {
			return fmt.Errorf("missing required argument 'objective'")
		}
		ctx.ShowInput("Objective:", "", func(value string, ok bool) {
			if !ok || value == "" {
				return
			}
			in.Objective = value
			if err := c.runNewInteractive(ctx, in); err != nil {
				ctx.Flash(err.Error())
			}
		})
		return nil
	}
	if err := c.doNew(ctx, in); err != nil {
		return err
	}
	return nil
}

func (c *OrchestrateCommand) runResumeInteractive(ctx core.Context, in OrchestrateInput) error {
	if in.ID == "" {
		if !isInteractive(ctx) {
			return fmt.Errorf("missing required argument 'id'")
		}
		runs, err := c.listableRuns()
		if err != nil {
			return err
		}
		items := runSelectorItems(runs, true)
		if len(items) == 0 {
			flashStr(ctx, "No resumable runs found.\n")
			return nil
		}
		ctx.SelectOption("Resume run:", items, "", func(selected string, ok bool) {
			if !ok || selected == "" {
				return
			}
			in.ID = selected
			if err := c.runResumeInteractive(ctx, in); err != nil {
				ctx.Flash(err.Error())
			}
		})
		return nil
	}
	if err := c.doResume(ctx, in); err != nil {
		return err
	}
	return nil
}

func (c *OrchestrateCommand) runDeleteInteractive(ctx core.Context, in OrchestrateInput) error {
	if in.ID == "" {
		return c.promptDeleteID(ctx, in)
	}
	if !in.Confirm {
		c.promptDeleteConfirm(ctx, in)
		return nil
	}
	if err := c.doDelete(ctx, in); err != nil {
		return err
	}
	return nil
}

func (c *OrchestrateCommand) promptDeleteID(ctx core.Context, in OrchestrateInput) error {
	if !isInteractive(ctx) {
		return fmt.Errorf("missing required argument 'id'")
	}
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
		flashStr(ctx, "No runs to delete.\n")
		return nil
	}
	ctx.SelectOption("Delete run:", items, "", func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		in.ID = selected
		if err := c.runDeleteInteractive(ctx, in); err != nil {
			ctx.Flash(err.Error())
		}
	})
	return nil
}

func (c *OrchestrateCommand) promptDeleteConfirm(ctx core.Context, in OrchestrateInput) {
	ctx.SelectOption("Delete "+in.ID+"?", confirmItems(), "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			return
		}
		in.Confirm = true
		if err := c.runDeleteInteractive(ctx, in); err != nil {
			ctx.Flash(err.Error())
		}
	})
}

func (c *OrchestrateCommand) runListInteractive(ctx core.Context) error {
	runs, err := c.listableRuns()
	if err != nil {
		return err
	}
	items := runSelectorItems(runs, false)
	if len(items) == 0 {
		flashStr(ctx, "No orchestration runs found.\n")
		return nil
	}
	ctx.SelectOption("Orchestration runs:", items, "", func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		if err := c.runResumeInteractive(ctx, OrchestrateInput{Subcommand: "resume", ID: selected}); err != nil {
			ctx.Flash(err.Error())
		}
	})
	return nil
}

func (c *OrchestrateCommand) runSteerInteractive(ctx core.Context, in OrchestrateInput) error {
	if in.ID == "" {
		return c.promptSteerID(ctx, in)
	}
	if in.Message == "" {
		return c.promptSteerMessage(ctx, in)
	}
	if err := c.doSteer(ctx, in); err != nil {
		return err
	}
	return nil
}

func (c *OrchestrateCommand) promptSteerID(ctx core.Context, in OrchestrateInput) error {
	if !isInteractive(ctx) {
		return fmt.Errorf("missing required argument 'id'")
	}
	ctx.ShowInput("Agent ID (or all/orchestrator):", "", func(value string, ok bool) {
		if !ok || value == "" {
			return
		}
		in.ID = value
		if err := c.runSteerInteractive(ctx, in); err != nil {
			ctx.Flash(err.Error())
		}
	})
	return nil
}

func (c *OrchestrateCommand) promptSteerMessage(ctx core.Context, in OrchestrateInput) error {
	if !isInteractive(ctx) {
		return fmt.Errorf("missing required argument 'message'")
	}
	ctx.ShowInput("Steer "+in.ID+":", "", func(value string, ok bool) {
		if !ok || value == "" {
			return
		}
		in.Message = value
		if err := c.runSteerInteractive(ctx, in); err != nil {
			ctx.Flash(err.Error())
		}
	})
	return nil
}

func (c *OrchestrateCommand) runBrowser(ctx core.Context) error {
	if !isInteractive(ctx) {
		return fmt.Errorf("browser requires an interactive TUI")
	}
	if c.ShowBrowser == nil {
		return fmt.Errorf("browser overlay not available")
	}
	c.ShowBrowser()
	return nil
}

// runTab selects a tab of the persistent multi-agent run view. It requires an
// active orchestration run and a TUI host that supplies SelectAgentTab.
func (c *OrchestrateCommand) runTab(ctx core.Context, in OrchestrateInput) error {
	if c.SelectAgentTab == nil {
		return fmt.Errorf("tab navigation not available")
	}
	if c.Active.Get() == nil {
		flashStr(ctx, "No active orchestration run.\n")
		return nil
	}
	if in.Tab == "" {
		flashStr(ctx, "Usage: /orchestrate:tab:<key|index>\n")
		return nil
	}
	label, ok := c.SelectAgentTab(in.Tab)
	if !ok {
		flashFmt(ctx, "Unknown tab %q.\n", in.Tab)
		return nil
	}
	flashFmt(ctx, "tab: %s\n", label)
	return nil
}

// --- synchronous execution ---------------------------------------------------

func (c *OrchestrateCommand) doNew(ctx core.Context, in OrchestrateInput) error {
	topology, err := normalizeTopology(in.Topology, ctx.Config)
	if err != nil {
		return err
	}
	oCfg, defaulted := effectiveOrchestratorConfig(ctx.Config)
	if defaulted {
		ctx.Flash("No orchestrator roles configured; using default coder/reviewer/orchestrator roles mapped to " + ctx.Config.ActiveModel + ". Run /config → Orchestrator → Roles to customize.")
	}
	if len(oCfg.Roles) == 0 {
		return fmt.Errorf("no orchestrator.roles configured — define roles in config first")
	}
	oCfg.Defaults.Topology = topology

	rt, err := c.Builder.NewRuntime(oCfg, c.RootDir)
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}

	name := c.resolveRunName(in.Name)
	rt.SetName(name)

	if _, exists := oCfg.Roles["orchestrator"]; !exists && topology == config.OrchestratorTopologyHub {
		flashStr(ctx, "Note: hub topology works best with an 'orchestrator' role; running fanout-style fallback.\n")
	}

	if c.GoalMode != nil {
		goalID, err := c.bindGoal(rt, name, in.Objective)
		if err != nil {
			flashFmt(ctx, "Warning: goal bind failed (%v); running goal-less.\n", err)
		} else {
			rt.SetGoalID(goalID)
		}
	}

	if prev := c.Active.Set(rt); prev != nil {
		// An older run is still active; leave it running but stop surfacing it.
	}

	flashFmt(ctx, "Started orchestration [%s] %s: %s\n", topology, name, in.Objective)
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
		flashFmt(ctx, "Run %s is already finished — nothing to resume.\n", internalID)
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
	// Continue the same run (same run-id + event log) and skip roles that
	// already finished, instead of re-running everything under a new id.
	rt.Resume(store, snap)
	c.Active.Set(rt)
	flashFmt(ctx, "Resuming %s: %s\n", snap.NameOrID(), snap.Objective)
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
	flashFmt(ctx, "Deleted run %s.\n", internalID)
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
	flashFmt(ctx, "Deleted %d run(s).\n", deleted)
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
		flashFmt(ctx, "Broadcast steering to all agents: %s\n", in.Message)
	case "orchestrator", "orch":
		if rt.SteerOrchestrator(in.Message) {
			flashStr(ctx, "Steered orchestrator.\n")
		} else {
			flashStr(ctx, "No orchestrator agent is live right now.\n")
		}
	default:
		if rt.SteerAgent(in.ID, in.Message) {
			flashFmt(ctx, "Steered %s.\n", in.ID)
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

// effectiveOrchestratorConfig returns a copy of the orchestrator config with
// default roles synthesized from the active model when no roles are configured.
// It also reports whether defaults were synthesized so the caller can warn the user.
func effectiveOrchestratorConfig(cfg *config.Config) (config.OrchestratorConfig, bool) {
	oCfg := cfg.Orchestrator
	if len(oCfg.Roles) > 0 || cfg.ActiveModel == "" {
		return oCfg, false
	}
	model := cfg.ActiveModel
	oCfg.Roles = map[string]config.OrchestratorRole{
		"orchestrator": {Model: model},
		"coder":        {Model: model},
		"reviewer":     {Model: model},
	}
	return oCfg, true
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
		runCtx, cancel := runContextWithActivityTimeout(
			rt,
			orchestrateActivityTimeout(ctx),
			orchestrateRunTimeout(ctx),
		)
		defer cancel()
		_ = rt.Run(runCtx, objective)
		<-rt.Done()
		c.Active.Clear(rt)
	}()
}

// runContextWithActivityTimeout returns a context that is cancelled only when
// no orchestrator events flow for activityTimeout, or when the absolute
// maxTimeout is reached. Any event from the runtime (agent messages, stats,
// tool calls, thinking, ...) resets the activity timer, so long-running runs
// that keep producing output are not killed by a fixed wall-clock limit.
func runContextWithActivityTimeout(rt *orchestrator.Runtime, activityTimeout, maxTimeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	events := rt.Subscribe()
	deadline := time.Now().Add(maxTimeout)

	// Absolute maximum guard: no run may exceed maxTimeout, even with activity.
	time.AfterFunc(maxTimeout, cancel)
	go watchActivity(ctx, cancel, events, activityTimeout, deadline)

	return ctx, cancel
}

// watchActivity resets the activity timer whenever an event arrives, and
// cancels the context on either inactivity or context cancellation.
func watchActivity(ctx context.Context, cancel context.CancelFunc, events <-chan orchestrator.Event, activityTimeout time.Duration, deadline time.Time) {
	defer cancel()
	activityTimer := time.NewTimer(activityTimeout)
	defer activityTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-activityTimer.C:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if activityDeadlineReached(ev, deadline, activityTimer, activityTimeout) {
				return
			}
		}
	}
}

// activityDeadlineReached reports whether the absolute deadline has passed and,
// if not, resets the activity timer. It discards a stale timer fire so the
// reset is safe.
func activityDeadlineReached(ev orchestrator.Event, deadline time.Time, timer *time.Timer, timeout time.Duration) bool {
	_ = ev
	if time.Now().After(deadline) {
		return true
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
	return false
}

// orchestrateRunTimeout derives the per-run absolute wall-clock budget from
// config (orchestrator.defaults.run_timeout, e.g. "1h"). Empty or unparseable
// values fall back to 1h so long-running multi-agent runs are not killed
// mid-turn, while still providing a hard safety ceiling.
const defaultOrchestrateRunTimeout = 1 * time.Hour

func orchestrateRunTimeout(ctx core.Context) time.Duration {
	if ctx.Config != nil {
		if s := ctx.Config.Orchestrator.Defaults.RunTimeout; s != "" {
			if d, err := time.ParseDuration(s); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultOrchestrateRunTimeout
}

// orchestrateActivityTimeout derives the inactivity budget from config
// (orchestrator.defaults.activity_timeout, e.g. "2m"). The timer resets on
// every event from the runtime; only a true stall (no events for this long)
// cancels the run. Empty or unparseable values fall back to 2m.
const defaultOrchestrateActivityTimeout = 2 * time.Minute

func orchestrateActivityTimeout(ctx core.Context) time.Duration {
	if ctx.Config != nil {
		if s := ctx.Config.Orchestrator.Defaults.ActivityTimeout; s != "" {
			if d, err := time.ParseDuration(s); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultOrchestrateActivityTimeout
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
	{"tab", "switch view tab: /orchestrate:tab:<key|index>"},
	{"browser", "open the run browser overlay"},
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
