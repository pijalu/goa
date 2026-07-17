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
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// PlanCommand manages structured work plans.
type PlanCommand struct {
	// RootDir is the working directory root (injected by the app layer).
	RootDir string
	// Cfg is the current configuration (injected by the app layer).
	Cfg *config.Config
	// StartExecution is an optional function to begin execution on approve.
	// Set by the app layer when the orchestrator is wired. On success it takes
	// ownership of the store (closing it when the run ends); on error the
	// caller keeps ownership.
	StartExecution func(store *plan.Store) error
	// OnPlanApproved is an optional callback invoked after a plan is approved
	// from the review pager, so the app layer can start execution with a
	// fresh store handle (the pager keeps its own store until closed).
	OnPlanApproved func(planID string)
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

	store, err := plan.Open(c.plansDir(), id)
	if err != nil {
		return fmt.Errorf("plan: open: %w", err)
	}

	if ctx.EventBus == nil {
		// Headless fallback: no pager without a TUI — print the plan instead.
		defer store.Close()
		md, _ := plan.Render(store.Plan())
		ctx.Writef("%s\n", md)
		return nil
	}

	pager := tui.NewPlanPager(store)
	// The pager owns the store while open; the app layer wires overlay-close
	// into OnClose and may wrap OnApprovePlan to hand the store to the
	// execution runner (which then owns it).
	pager.OnClose = func() { _ = store.Close() }
	pager.OnSubmitAnnotations = func(text string) {
		if ctx.SubmitToAgent != nil {
			ctx.SubmitToAgent(text)
		}
	}
	pager.OnApprovePlan = func() {
		if err := store.Approve(); err != nil {
			ctx.Flash(fmt.Sprintf("plan %s: %v", id, err))
			return
		}
		ctx.Flash(fmt.Sprintf("✓ plan %s approved", id))
		if c.OnPlanApproved != nil {
			c.OnPlanApproved(id)
		}
	}
	ctx.EventBus.Chat <- event.ChatEvent{ShowPlanPager: &event.ShowPlanPager{Pager: pager}}
	return nil
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
	// Ownership transfers to StartExecution on success; only close while we
	// still own the store.
	owned := true
	defer func() {
		if owned {
			_ = store.Close()
		}
	}()

	// Idempotent retry: a previous /plan approve may have succeeded while the
	// execution start failed, leaving the plan approved but idle. Approving
	// again would error ("cannot approve plan in status approved"), so skip
	// straight to the execution start in that case.
	if store.Plan().Status == plan.PlanApproved {
		ctx.Writef("Plan %q already approved; starting execution.\n", id)
	} else if err := store.Approve(); err != nil {
		return fmt.Errorf("plan: approve: %w", err)
	}

	if c.StartExecution != nil {
		if err := c.StartExecution(store); err != nil {
			return fmt.Errorf("plan: start execution: %w", err)
		}
		owned = false
	}

	ctx.Writef("Plan %q approved and execution started.\n", id)
	return nil
}

func (c *PlanCommand) cmdStatus(ctx core.Context, in planInput) error {
	id, err := c.resolveID(ctx, in)
	if err != nil {
		return err
	}

	store, err := plan.Open(c.plansDir(), id)
	if err != nil {
		return fmt.Errorf("plan: open: %w", err)
	}

	if ctx.EventBus == nil {
		// Headless fallback: print the plan status as Markdown.
		defer store.Close()
		md, _ := plan.Render(store.Plan())
		ctx.Writef("%s\n", md)
		return nil
	}

	// Ownership passes to the overlay; the app layer closes the store when
	// the overlay is dismissed.
	ctx.EventBus.Chat <- event.ChatEvent{ShowPlanStatus: &event.ShowPlanStatus{Store: store}}
	return nil
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
