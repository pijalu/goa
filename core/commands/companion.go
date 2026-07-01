// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/multiagent"
)

// CompanionToggleCommand toggles companion / agent-driven mode.
type CompanionToggleCommand struct{}

func (c *CompanionToggleCommand) Name() string      { return "companion" }
func (c *CompanionToggleCommand) Aliases() []string { return []string{} }
func (c *CompanionToggleCommand) ShortHelp() string {
	return "Toggle companion mode (agent-driven by default)"
}

func (c *CompanionToggleCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *CompanionToggleCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"on", "enable companion mode (agent-driven)"},
		{"off", "disable companion mode"},
		{"agent", "enable agent-driven mode"},
		{"framework", "enable framework-driven (review every turn)"},
	} {
		if strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

func (c *CompanionToggleCommand) Run(ctx core.Context, args []string) error {
	am := ctx.AgentManager
	if am == nil {
		writeStr(ctx, "Companion mode: unavailable (no active session)\n")
		return nil
	}

	if len(args) == 0 {
		return showCompanionStatus(ctx)
	}

	switch args[0] {
	case "on", "agent":
		return enableCompanionAgentDriven(ctx, am)
	case "framework":
		return enableCompanionFramework(ctx, am)
	case "off":
		return disableCompanion(ctx, am)
	default:
		writeFmt(ctx, "Unknown option: %q. Use /companion:on, /companion:agent, /companion:framework, or /companion:off\n", args[0])
	}
	return nil
}

func enableCompanionAgentDriven(ctx core.Context, am *core.AgentManager) error {
	if err := am.SetMinorMode("companion", true); err != nil {
		return fmt.Errorf("enable companion: %w", err)
	}
	if err := am.InjectCompanionReview(true); err != nil {
		return fmt.Errorf("enable companion review: %w", err)
	}
	// Mirror the mode into the orchestrator so /companion (status) reports
	// consistently with what was just enabled.
	if orch := ctx.ForegroundOrchestrator; orch != nil {
		orch.SetMode(multiagent.WorkflowAgentDriven)
	}
	writeStr(ctx, "Companion mode enabled (agent-driven).\n")
	return nil
}

func enableCompanionFramework(ctx core.Context, am *core.AgentManager) error {
	if ctx.ForegroundOrchestrator == nil {
		writeStr(ctx, "Companion mode: unavailable (no orchestrator)\n")
		return nil
	}
	ctx.ForegroundOrchestrator.SetMode(multiagent.WorkflowCompanionMinor)
	_ = am.SetAgentDrivenEnabled(true)
	if err := am.InjectCompanionReview(false); err != nil {
		return fmt.Errorf("disable companion review: %w", err)
	}
	writeStr(ctx, "Companion mode enabled (framework-driven).\n")
	return nil
}

func disableCompanion(ctx core.Context, am *core.AgentManager) error {
	if err := am.SetMinorMode("companion", false); err != nil {
		return fmt.Errorf("disable companion: %w", err)
	}
	if err := am.InjectCompanionReview(false); err != nil {
		return fmt.Errorf("disable companion review: %w", err)
	}
	// Reset the orchestrator mode too, otherwise status would still report
	// framework/agent-driven after /companion:off.
	if orch := ctx.ForegroundOrchestrator; orch != nil {
		orch.SetMode(multiagent.WorkflowInactive)
	}
	writeStr(ctx, "Companion mode disabled.\n")
	return nil
}

func showCompanionStatus(ctx core.Context) error {
	if ctx.ForegroundOrchestrator == nil {
		writeStr(ctx, "Companion mode: unavailable (no orchestrator)\n")
		return nil
	}
	mode := ctx.ForegroundOrchestrator.Mode()
	switch mode {
	case multiagent.WorkflowCompanionMinor:
		writeStr(ctx, "Companion mode: enabled (framework-driven)\n")
	case multiagent.WorkflowAgentDriven:
		writeStr(ctx, "Companion mode: enabled (agent-driven)\n")
	default:
		writeStr(ctx, "Companion mode: disabled\n")
	}
	return nil
}
