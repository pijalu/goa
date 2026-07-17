// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/plan"
)

// PlanCommand manages structured work plans.
type PlanCommand struct {
	// RootDir is the working directory root (injected by the app layer).
	RootDir string
	// Cfg is the current configuration (injected by the app layer).
	Cfg *config.Config
	// StartExecution is an optional function to begin execution on approve.
	// Set by the app layer when the orchestrator is wired.
	StartExecution func(planID string) error
}

// planInput is the parsed form of a /plan colon command.
type planInput struct {
	Subcommand string
	ID         string
	Objective  string
	Confirm    bool
}

// planSubcommands maps each subcommand to the keys it accepts.
var planSubcommands = map[string][]string{
	"new":     {"objective"},
	"review":  {"id"},
	"approve": {"id"},
	"status":  {"id"},
	"replan":  {"id"},
	"list":    {},
	"delete":  {"id", "confirm"},
}

func (c *PlanCommand) Name() string      { return "plan" }
func (c *PlanCommand) Aliases() []string { return []string{} }
func (c *PlanCommand) IsInternal() bool  { return false }
func (c *PlanCommand) ShortHelp() string { return "Manage structured work plans" }
func (c *PlanCommand) LongHelp() string  { return help.LongHelp(c.Name()) }

func (c *PlanCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	return nil
}

func (c *PlanCommand) Run(ctx core.Context, args []string) error {
	in, err := c.parseInput(args)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}

	switch in.Subcommand {
	case "":
		return c.interactiveList(ctx)
	case "new":
		return c.cmdNew(ctx, in)
	case "review":
		return c.cmdReview(ctx, in)
	case "approve":
		return c.cmdApprove(ctx, in)
	case "status":
		return c.cmdStatus(ctx, in)
	case "replan":
		return c.cmdReplan(ctx, in)
	case "list":
		return c.cmdList(ctx)
	case "delete":
		return c.cmdDelete(ctx, in)
	default:
		return fmt.Errorf("plan: unknown subcommand %q", in.Subcommand)
	}
}

func (c *PlanCommand) plansDir() string {
	if c.RootDir == "" {
		return ""
	}
	return filepath.Join(c.RootDir, ".goa", "plans")
}

// parseInput parses the colon-separated arguments.
func (c *PlanCommand) parseInput(args []string) (planInput, error) {
	var in planInput
	if len(args) == 0 {
		return in, nil
	}

	in.Subcommand = strings.ToLower(args[0])
	if _, ok := planSubcommands[in.Subcommand]; !ok {
		return in, fmt.Errorf("unknown subcommand %q", in.Subcommand)
	}

	in.parseKeyValues(c, args[1:])
	return in, nil
}

// parseKeyValues parses key=value pairs from the remaining arguments.
func (in *planInput) parseKeyValues(c *PlanCommand, args []string) {
	for _, arg := range args {
		for _, pair := range strings.Split(arg, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) != 2 {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(kv[0]))
			value := strings.TrimSpace(kv[1])

			if !c.isKnownKey(in.Subcommand, key) {
				continue
			}

			in.applyKeyValue(key, value)
		}
	}
}

// applyKeyValue sets a field on the input from a parsed key=value pair.
func (in *planInput) applyKeyValue(key, value string) {
	switch key {
	case "id":
		in.ID = value
	case "objective":
		in.Objective = value
	case "confirm":
		in.Confirm, _ = strconv.ParseBool(value)
	}
}

func (c *PlanCommand) isKnownKey(subcommand, key string) bool {
	keys, ok := planSubcommands[subcommand]
	if !ok {
		return false
	}
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

func (c *PlanCommand) resolveID(ctx core.Context, in planInput) (string, error) {
	dir := c.plansDir()
	if dir == "" {
		return "", fmt.Errorf("plan: root not configured")
	}
	if in.ID != "" {
		return plan.Resolve(dir, in.ID)
	}
	return "", fmt.Errorf("plan: id is required")
}

func (c *PlanCommand) interactiveList(ctx core.Context) error {
	return c.cmdList(ctx)
}

func (c *PlanCommand) cmdNew(ctx core.Context, in planInput) error {
	if in.Objective == "" {
		return fmt.Errorf("plan: objective is required for /plan:new")
	}

	store, err := plan.Create(c.plansDir(), in.Objective)
	if err != nil {
		return fmt.Errorf("plan: create: %w", err)
	}
	defer store.Close()

	ctx.Writef( "Plan %q created (ID: %s)\n", in.Objective, store.ID())
	return nil
}

func (c *PlanCommand) cmdReview(ctx core.Context, in planInput) error {
	id, err := c.resolveID(ctx, in)
	if err != nil {
		return err
	}
	_ = id
	// Plan pager opening is handled by the TUI layer via ShowPlanPager event.
	return fmt.Errorf("plan: review not yet wired to TUI")
}

func (c *PlanCommand) cmdApprove(ctx core.Context, in planInput) error {
	id, err := c.resolveID(ctx, in)
	if err != nil {
		return err
	}

	store, err := plan.Open(c.plansDir(), id)
	if err != nil {
		return fmt.Errorf("plan: open: %w", err)
	}
	defer store.Close()

	if err := store.Approve(); err != nil {
		return fmt.Errorf("plan: approve: %w", err)
	}

	if c.StartExecution != nil {
		if err := c.StartExecution(id); err != nil {
			return fmt.Errorf("plan: start execution: %w", err)
		}
	}

	ctx.Writef( "Plan %q approved and execution started.\n", id)
	return nil
}

func (c *PlanCommand) cmdStatus(ctx core.Context, in planInput) error {
	_ = in
	return fmt.Errorf("plan: status not yet wired to TUI")
}

func (c *PlanCommand) cmdReplan(ctx core.Context, in planInput) error {
	_, err := c.resolveID(ctx, in)
	if err != nil {
		return err
	}
	return fmt.Errorf("plan: replan not yet implemented")
}

func (c *PlanCommand) cmdList(ctx core.Context) error {
	dir := c.plansDir()
	if dir == "" {
		return fmt.Errorf("plan: root directory not configured")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			ctx.Writef( "No plans found.\n")
			return nil
		}
		return fmt.Errorf("plan: list: %w", err)
	}

	ctx.Writef( "Plans:\n")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "plan.json"))
		if err != nil {
			continue
		}
		var p plan.Plan
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		ctx.Writef( "  %-20s  %-10s  rev=%-2d  items=%d\n",
			p.Name, p.Status, p.Revision, len(p.Items))
	}
	return nil
}

func (c *PlanCommand) cmdDelete(ctx core.Context, in planInput) error {
	if !in.Confirm {
		return fmt.Errorf("plan: delete requires confirm=true")
	}

	id, err := c.resolveID(ctx, in)
	if err != nil {
		return err
	}

	dir := filepath.Join(c.plansDir(), id)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("plan: delete: %w", err)
	}

	ctx.Writef( "Plan %q deleted.\n", id)
	return nil
}
