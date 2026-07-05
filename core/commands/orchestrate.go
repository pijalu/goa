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
)

// OrchestrateBuilder builds a Runtime for one orchestration run. The production
// implementation (internal/app.OrchestratorAdapter) bridges to a real agent
// pool; the interface keeps this package free of internal/app (no import cycle).
type OrchestrateBuilder = orchestrator.Builder

// OrchestrateCommand runs multi-agent orchestrations: per-run topology
// selection (hub/fanout/pipeline), goal binding, listing, resuming, and
// per-target steering. It is the user-facing surface over core/orchestrator.
type OrchestrateCommand struct {
	Builder  OrchestrateBuilder        // builds a wired Runtime per run
	Active   *orchestrator.ActiveRuntime
	RootDir  string                    // event-store root, typically ".goa/orchestrator"
	GoalMode *goal.GoalMode            // optional; enables `goal <objective>` binding
}

func (c *OrchestrateCommand) Name() string      { return "orchestrate" }
func (c *OrchestrateCommand) Aliases() []string { return []string{"orch"} }
func (c *OrchestrateCommand) ShortHelp() string {
	return "Run a multi-agent orchestration (hub/fanout/pipeline)"
}
func (c *OrchestrateCommand) LongHelp() string { return help.LongHelp(c.Name()) }

// Run dispatches to the subcommand parsed from args[0].
func (c *OrchestrateCommand) Run(ctx core.Context, args []string) error {
	if c.Builder == nil || c.Active == nil {
		writeStr(ctx, "Orchestration subsystem not available.\n")
		return nil
	}
	if len(args) == 0 {
		return c.usage(ctx)
	}
	switch strings.ToLower(args[0]) {
	case "new", "start", "run":
		return c.runNew(ctx, args[1:])
	case "list", "ls":
		return c.runList(ctx)
	case "resume":
		return c.runResume(ctx, args[1:])
	case "steer":
		return c.runSteer(ctx, args[1:])
	case "help", "?":
		return c.usage(ctx)
	default:
		// Bare objective: /orchestrate <objective> → new run with default topology.
		return c.runNew(ctx, args)
	}
}

func (c *OrchestrateCommand) usage(ctx core.Context) error {
	writeStr(ctx, "Usage:\n")
	writeStr(ctx, "  /orchestrate new [hub|fanout|pipeline] [goal <objective>] <objective>\n")
	writeStr(ctx, "  /orchestrate list\n")
	writeStr(ctx, "  /orchestrate resume <run-id>\n")
	writeStr(ctx, "  /orchestrate steer <agent-id|all|orchestrator> <text>\n")
	return nil
}

// parsedNew captures the parsed /orchestrate new arguments.
type parsedNew struct {
	topology      string
	objective     string
	goalObjective string
}

func parseNewArgs(rest []string) (parsedNew, error) {
	var p parsedNew
	i := 0
	// Optional topology keyword.
	if i < len(rest) {
		switch strings.ToLower(rest[i]) {
		case config.OrchestratorTopologyHub, config.OrchestratorTopologyFanout, config.OrchestratorTopologyPipeline:
			p.topology = strings.ToLower(rest[i])
			i++
		}
	}
	// Optional "goal <objective>" then run objective, or just run objective.
	remaining := rest[i:]
	for j, tok := range remaining {
		if strings.ToLower(tok) == "goal" && j+1 < len(remaining) {
			p.goalObjective = strings.Join(remaining[j+1:], " ")
			p.objective = strings.TrimSpace(strings.Join(remaining[:j], " "))
			if p.objective == "" {
				p.objective = p.goalObjective // run on the goal itself
			}
			return p, nil
		}
	}
	p.objective = strings.TrimSpace(strings.Join(remaining, " "))
	if p.objective == "" {
		return p, fmt.Errorf("usage: /orchestrate new [topology] [goal <objective>] <objective>")
	}
	return p, nil
}

func (c *OrchestrateCommand) runNew(ctx core.Context, rest []string) error {
	p, err := parseNewArgs(rest)
	if err != nil {
		return err
	}
	oCfg := ctx.Config.Orchestrator
	if p.topology != "" {
		oCfg.Defaults.Topology = p.topology
	}
	if len(oCfg.Roles) == 0 {
		return fmt.Errorf("no orchestrator.roles configured — define roles in config first")
	}
	if _, exists := oCfg.Roles["orchestrator"]; !exists && strings.EqualFold(oCfg.Defaults.Topology, config.OrchestratorTopologyHub) {
		writeStr(ctx, "Note: hub topology works best with an 'orchestrator' role; running fanout-style fallback.\n")
	}

	rt, err := c.Builder.NewRuntime(oCfg, c.RootDir)
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}
	if p.goalObjective != "" {
		if c.GoalMode == nil {
			writeStr(ctx, "Warning: goal binding requested but goal mode unavailable; running goal-less.\n")
		} else if err := c.bindGoal(rt, p.goalObjective); err != nil {
			writeFmt(ctx, "Warning: goal bind failed (%v); running goal-less.\n", err)
		}
	}
	if prev := c.Active.Set(rt); prev != nil {
		// An older run is still active; leave it running but stop surfacing it.
	}

	writeFmt(ctx, "Started orchestration [%s]: %s\n", oCfg.Defaults.Topology, p.objective)
	if p.goalObjective != "" {
		writeFmt(ctx, "  goal binding: %s\n", p.goalObjective)
	}
	c.launch(ctx, rt, p.objective)
	return nil
}

// launch starts the run in a goroutine and forwards lifecycle events into the
// chat viewport (Flash + InterAgent). The forwarder exits when the run's
// Events() channel closes, then clears the active holder.
func (c *OrchestrateCommand) launch(ctx core.Context, rt *orchestrator.Runtime, objective string) {
	go c.forwardEvents(ctx, rt)
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := rt.Run(runCtx, objective)
		<-rt.Done()
		c.Active.Clear(rt)
		if err != nil {
			ctx.Flash(fmt.Sprintf("Orchestration failed: %v", err))
		}
	}()
}

func (c *OrchestrateCommand) forwardEvents(ctx core.Context, rt *orchestrator.Runtime) {
	seen := map[string]bool{} // per-agent finished dedupe for chat lines
	for ev := range rt.Events() {
		c.handleOrchEvent(ctx, rt, ev, seen)
	}
}

// handleOrchEvent routes one orchestrator event to the chat viewport. Kept
// cyclotronically simple: each case does a single formatted emission.
func (c *OrchestrateCommand) handleOrchEvent(ctx core.Context, rt *orchestrator.Runtime, ev orchestrator.Event, seen map[string]bool) {
	switch ev.Type {
	case orchestrator.EventRunStarted:
		ctx.Flash(fmt.Sprintf("▷ orchestration %s started", rt.Topology()))
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
	ctx.Flash("■ orchestration " + status)
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

func (c *OrchestrateCommand) runList(ctx core.Context) error {
	runs, err := orchestrator.ListRuns(c.RootDir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		writeStr(ctx, "No orchestration runs found.\n")
		return nil
	}
	writeStr(ctx, "Orchestration runs (most recent first):\n")
	for _, r := range runs {
		state := "running"
		if r.Finished {
			state = "finished"
		}
		writeFmt(ctx, "  %s  [%s/%s]  agents=%d  %s  %s\n",
			r.RunID, r.Topology, state, r.AgentCount, truncStr(r.Objective, 50), r.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return nil
}

func (c *OrchestrateCommand) runResume(ctx core.Context, rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("usage: /orchestrate resume <run-id>")
	}
	runID := rest[0]
	store := orchestrator.NewFileEventStore(c.RootDir, runID)
	snap, err := orchestrator.ReplaySnapshot(store)
	if err != nil {
		return fmt.Errorf("resume %s: %w", runID, err)
	}
	if snap.Finished {
		writeFmt(ctx, "Run %s is already finished — nothing to resume.\n", runID)
		return nil
	}
	writeFmt(ctx, "Resuming run %s (topology=%s, agents=%d). Re-issuing objective.\n",
		runID, snap.Topology, len(snap.Agents))
	// For M2 we re-launch with the same objective; full mid-flight agent
	// re-acquisition (Phase 4 step 21 runtime half) lands with M3/M4.
	oCfg := ctx.Config.Orchestrator
	if snap.Topology != "" {
		oCfg.Defaults.Topology = string(snap.Topology)
	}
	rt, err := c.Builder.NewRuntime(oCfg, c.RootDir)
	if err != nil {
		return err
	}
	c.Active.Set(rt)
	c.launch(ctx, rt, snap.Objective)
	return nil
}

func (c *OrchestrateCommand) runSteer(ctx core.Context, rest []string) error {
	if len(rest) < 2 {
		return fmt.Errorf("usage: /orchestrate steer <agent-id|all|orchestrator> <text>")
	}
	target := rest[0]
	text := strings.Join(rest[1:], " ")
	rt := c.Active.Get()
	if rt == nil {
		return fmt.Errorf("no active orchestration to steer")
	}
	switch strings.ToLower(target) {
	case "all":
		rt.SteerAll(text)
		writeFmt(ctx, "Broadcast steering to all agents: %s\n", text)
	case "orchestrator", "orch":
		if rt.SteerOrchestrator(text) {
			writeStr(ctx, "Steered orchestrator.\n")
		} else {
			writeStr(ctx, "No orchestrator agent is live right now.\n")
		}
	default:
		if rt.SteerAgent(target, text) {
			writeFmt(ctx, "Steered %s.\n", target)
		} else {
			return fmt.Errorf("no live agent with id %q", target)
		}
	}
	return nil
}

// orchestrateSubcommands feeds /orchestrate:<tab> completion.
var orchestrateSubcommands = []struct {
	value, desc string
}{
	{"new", "start a new run: new [topology] [goal <obj>] <objective>"},
	{"list", "list past runs"},
	{"resume", "resume a run: resume <run-id>"},
	{"steer", "steer: steer <agent-id|all|orchestrator> <text>"},
	{"hub", "shortcut: new hub run"},
	{"fanout", "shortcut: new fanout run"},
	{"pipeline", "shortcut: new pipeline run"},
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

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// bindGoal attaches a GoalBinder to the runtime and creates the goal.
func (c *OrchestrateCommand) bindGoal(rt *orchestrator.Runtime, objective string) error {
	gb := NewGoalBinder(c.GoalMode)
	if _, err := gb.Create(objective, 0); err != nil {
		return err
	}
	rt.SetGoalBinder(gb)
	return nil
}
