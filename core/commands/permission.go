// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/perms"
)

// PermissionCommand handles /permission for managing permission rules.
type PermissionCommand struct {
	mu    sync.RWMutex
	rules []perms.Rule
}

// Name returns the command name.
func (c *PermissionCommand) Name() string { return "permission" }

// Aliases returns command aliases.
func (c *PermissionCommand) Aliases() []string { return []string{} }

// ShortHelp returns a short description.
func (c *PermissionCommand) ShortHelp() string { return "Manage permission rules" }

// LongHelp returns usage help.
func (c *PermissionCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for permission subcommands.
func (c *PermissionCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	parts := strings.SplitN(prefix, ":", 2)
	if len(parts) == 1 {
		return permSubcompletions(prefix)
	}
	if len(parts) == 2 {
		return permArgCompletions(c, parts[0], parts[1])
	}
	return nil
}

func permSubcompletions(prefix string) []core.ArgCompletion {
	subs := []string{"list", "add", "remove", "clear"}
	var comps []core.ArgCompletion
	for _, s := range subs {
		if prefix == "" || strings.HasPrefix(s, prefix) {
			comps = append(comps, core.ArgCompletion{Value: s})
		}
	}
	return comps
}

func permArgCompletions(cmd *PermissionCommand, sub, argPrefix string) []core.ArgCompletion {
	if sub != "remove" {
		return nil
	}
	cmd.mu.RLock()
	defer cmd.mu.RUnlock()
	var comps []core.ArgCompletion
	for i, r := range cmd.rules {
		id := fmt.Sprintf("%d", i)
		if argPrefix == "" || strings.HasPrefix(id, argPrefix) {
			comps = append(comps, core.ArgCompletion{Value: id, Description: r.Pattern + " → " + string(r.Decision)})
		}
	}
	return comps
}

// Rules returns a copy of the current rules.
func (c *PermissionCommand) Rules() []perms.Rule {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]perms.Rule, len(c.rules))
	copy(out, c.rules)
	return out
}

// SetRules replaces the internal rule list.
func (c *PermissionCommand) SetRules(rules []perms.Rule) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rules = make([]perms.Rule, len(rules))
	copy(c.rules, rules)
}

// Run executes the command.
func (c *PermissionCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return c.listRules(ctx)
	}
	switch strings.ToLower(args[0]) {
	case "list", "ls":
		return c.listRules(ctx)
	case "add":
		return c.addRule(ctx, args[1:])
	case "remove", "rm":
		return c.removeRule(ctx, args[1:])
	case "clear":
		return c.clearRules(ctx)
	default:
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func (c *PermissionCommand) listRules(ctx core.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.rules) == 0 {
		ctx.Writef("No permission rules.\n")
		return nil
	}
	ctx.Writef("Permission rules:\n")
	for i, r := range c.rules {
		scope := ""
		if r.Mode != "" {
			scope = fmt.Sprintf(" mode=%s", r.Mode)
		}
		ctx.Writef("  [%d] %s → %s%s\n", i, r.Pattern, r.Decision, scope)
	}
	return nil
}

func (c *PermissionCommand) addRule(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /permission:add:<pattern>:<decision>[:mode]")
	}
	pattern := args[0]
	decision := perms.Decision(strings.ToLower(args[1]))
	if decision != perms.DecisionAllow && decision != perms.DecisionDeny && decision != perms.DecisionAsk {
		return fmt.Errorf("decision must be allow, deny, or ask")
	}
	r := perms.Rule{
		Pattern:  pattern,
		Decision: decision,
	}
	if len(args) >= 3 {
		r.Mode = args[2]
	}
	c.mu.Lock()
	c.rules = append(c.rules, r)
	c.mu.Unlock()

	msg := fmt.Sprintf("Added rule: %s → %s", pattern, decision)
	if r.Mode != "" {
		msg += fmt.Sprintf(" (mode: %s)", r.Mode)
	}
	ctx.Writef("%s\n", msg)
	return nil
}

func (c *PermissionCommand) removeRule(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /permission:remove:<id>")
	}
	var idx int
	if _, err := fmt.Sscanf(args[0], "%d", &idx); err != nil {
		return fmt.Errorf("invalid index: %s", args[0])
	}
	c.mu.Lock()
	if idx >= 0 && idx < len(c.rules) {
		c.rules = append(c.rules[:idx], c.rules[idx+1:]...)
	}
	c.mu.Unlock()
	ctx.Writef("Removed rule [%d]\n", idx)
	return nil
}

func (c *PermissionCommand) clearRules(ctx core.Context) error {
	c.mu.Lock()
	c.rules = nil
	c.mu.Unlock()
	ctx.Writef("All permission rules cleared.\n")
	return nil
}

// Ensure Rule/Decision types are visible to the package.
var _ = perms.Rule{}
var _ = perms.DecisionAllow

// RebuildEngine recreates a perms.Engine from the current rules.
func (c *PermissionCommand) RebuildEngine() *perms.Engine {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return perms.NewEngine(c.rules)
}
