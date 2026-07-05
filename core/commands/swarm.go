// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/swarm"
)

// SwarmCommand handles /swarm on|off|<task>.
type SwarmCommand struct {
	State *swarm.State
}

// Name returns the command name.
func (c *SwarmCommand) Name() string { return "swarm" }

// Aliases returns command aliases.
func (c *SwarmCommand) Aliases() []string { return nil }

// ShortHelp returns a one-line description.
func (c *SwarmCommand) ShortHelp() string {
	return "Enable or disable swarm mode, or run a one-off swarm task"
}

// LongHelp returns detailed usage.
func (c *SwarmCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for swarm subcommands. Only the
// value that changes the current state is proposed; tasks are always offered.
func (c *SwarmCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if c.State == nil {
		return nil
	}
	active := c.State.IsActive()
	next := "off"
	desc := "disable swarm mode"
	if !active {
		next = "on"
		desc = "enable swarm mode"
	}
	if prefix == "" || strings.HasPrefix(next, prefix) {
		return []core.ArgCompletion{{Value: next, Description: desc}}
	}
	return nil
}

// Run executes the command.
func (c *SwarmCommand) Run(ctx core.Context, args []string) error {
	if c.State == nil {
		return fmt.Errorf("swarm state not configured")
	}
	if len(args) == 0 {
		if c.State.IsActive() {
			ctx.Writef("Swarm mode is ON (trigger: %s, task: %q)\n", triggerLabel(c.State.Trigger()), c.State.Task())
		} else {
			ctx.Writef("Swarm mode is OFF\n")
		}
		return nil
	}
	switch args[0] {
	case "on":
		c.enableManual(ctx)
	case "off":
		c.disable(ctx)
	default:
		c.startTask(ctx, strings.Join(args, " "))
	}
	return nil
}

func (c *SwarmCommand) enableManual(ctx core.Context) {
	if c.State.IsActive() {
		ctx.Writef("Swarm mode is already on.\n")
		return
	}
	c.State.Enter(swarm.ManualTrigger, "manual")
	ctx.Writef("Swarm mode enabled (manual). Use the agent_swarm tool, or /swarm off to exit.\n")
}

func (c *SwarmCommand) disable(ctx core.Context) {
	if !c.State.IsActive() {
		ctx.Writef("Swarm mode is already off.\n")
		return
	}
	c.State.Exit()
	ctx.Writef("Swarm mode disabled.\n")
}

// startTask activates swarm mode under the one-shot task trigger and feeds
// the prompt to the agent as a normal user input (kimi-code startSwarmTask
// parity). The turn-end hook auto-exits after the turn completes.
func (c *SwarmCommand) startTask(ctx core.Context, task string) {
	c.State.Enter(swarm.TaskTrigger, task)
	ctx.Writef("Swarm task started: %s\n", task)
	am := ctx.AgentManager
	if am == nil {
		return
	}
	go func() {
		if err := am.SendUserInput(task); err != nil {
			ctx.InterAgent("swarm", "user", fmt.Sprintf("Swarm task error: %v", err))
		}
	}()
}

func triggerLabel(t swarm.Trigger) string {
	switch t {
	case swarm.ManualTrigger:
		return "manual"
	case swarm.TaskTrigger:
		return "task"
	case swarm.ToolTrigger:
		return "tool"
	default:
		return "none"
	}
}
