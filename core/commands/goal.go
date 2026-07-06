// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// AutonomySwitcher controls the current autonomy level.
type AutonomySwitcher interface {
	Current() internal.AutonomyLevel
	SetAutonomy(level internal.AutonomyLevel) error
}

// GoalCommand handles /goal slash commands.
type GoalCommand struct {
	Mode             *goal.GoalMode
	Queue            *core.GoalQueueStore
	Driver           *core.GoalDriver
	AutonomySwitcher AutonomySwitcher
}

// Name returns the command name.
func (c *GoalCommand) Name() string { return "goal" }

// Aliases returns command aliases.
func (c *GoalCommand) Aliases() []string { return nil }

// ShortHelp returns a short help string.
func (c *GoalCommand) ShortHelp() string { return "Manage autonomous goals" }

// LongHelp returns detailed help.
func (c *GoalCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// Run executes the /goal command.
//
// The router splits only on ':', so args[0] is the subcommand keyword and the
// remaining args (joined with spaces) form the objective text:
//
//	/goal:new:fix tests  → args=["new", "fix tests"]
//	/goal:next:fix tests → args=["next", "fix tests"]
//	/goal:pause          → args=["pause"]
//	/goal                → args=[]
//
// goalDispatch maps a parsed kind to its handler. Table-driven to keep Run
// under the cyclomatic-complexity budget.
var goalDispatch = map[string]func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error{
	"status":   func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.showStatus(ctx) },
	"pause":    func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.pause(ctx) },
	"resume":   func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.resume(ctx) },
	"cancel":   func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.cancel(ctx) },
	"manage":   func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.showQueueManager(ctx) },
	"next-add": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error { return c.queueNext(ctx, p.objective) },
	"next-interactive": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error {
		return c.promptCreateInteractive(ctx, placementLast)
	},
	"reorder": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		return c.reorderQueue(ctx, p.objective)
	},
	"create": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error { return c.create(ctx, p.objective) },
	"create-interactive": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error {
		return c.promptCreateInteractive(ctx, placementAsk)
	},
	"replace":             func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error { return c.replace(ctx, p.objective) },
	"replace-interactive": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.promptReplaceInteractive(ctx) },
}

func (c *GoalCommand) Run(ctx core.Context, args []string) error {
	parsed := c.parseArgs(args)
	if parsed.kind == "error" {
		if parsed.severity == "hint" {
			ctx.Flash(parsed.message)
			return nil
		}
		return fmt.Errorf("%s", parsed.message)
	}
	if handler, ok := goalDispatch[parsed.kind]; ok {
		return handler(c, ctx, parsed)
	}
	return nil
}

type parsedGoalArgs struct {
	kind      string
	objective string
	message   string
	severity  string
}

func (c *GoalCommand) parseArgs(args []string) parsedGoalArgs {
	if len(args) == 0 {
		return parsedGoalArgs{kind: "create-interactive"}
	}
	return c.parseSubcommand(args)
}

// subcommandMode classifies a subcommand keyword by how it consumes its text arg.
type subcommandMode int

const (
	subNone     subcommandMode = iota // status/pause/resume/cancel/manage
	subOptional                       // new/next/replace: bare → interactive, with text → action
	subRequired                       // reorder: requires a mapping arg
)

// goalSubcommandKinds maps each subcommand keyword to its parse behavior and
// resulting parsedGoalArgs.kind (or kind pattern). Table-driven to keep
// parseSubcommand under the cyclomatic budget.
var goalSubcommandKinds = map[string]struct {
	mode      subcommandMode
	kind      string // kind when text is present
	bareKind  string // kind when no text (subOptional)
	errorHint string // non-empty → emit this usage hint when text missing
}{
	"status":  {mode: subNone, kind: "status"},
	"pause":   {mode: subNone, kind: "pause"},
	"resume":  {mode: subNone, kind: "resume"},
	"cancel":  {mode: subNone, kind: "cancel"},
	"manage":  {mode: subNone, kind: "manage"},
	"new":     {mode: subOptional, kind: "create", bareKind: "create-interactive"},
	"next":    {mode: subOptional, kind: "next-add", bareKind: "next-interactive"},
	"replace": {mode: subOptional, kind: "replace", bareKind: "replace-interactive"},
	"reorder": {mode: subRequired, kind: "reorder", errorHint: "usage: /goal:reorder <mapping> (e.g. 1B,2C,3A)"},
}

func (c *GoalCommand) parseSubcommand(args []string) parsedGoalArgs {
	cmd := strings.ToLower(args[0])
	spec, known := goalSubcommandKinds[cmd]
	if !known {
		// No subcommand keyword: treat all args as the objective (create).
		return parseObjectiveArg(args, "create-interactive", "create")
	}
	text := strings.TrimSpace(strings.Join(args[1:], " "))
	switch spec.mode {
	case subNone:
		return parsedGoalArgs{kind: spec.kind}
	case subRequired:
		if text == "" {
			return parsedGoalArgs{kind: "error", message: spec.errorHint, severity: "hint"}
		}
		return parsedGoalArgs{kind: spec.kind, objective: text}
	default: // subOptional
		if text == "" {
			return parsedGoalArgs{kind: spec.bareKind}
		}
		return parsedGoalArgs{kind: spec.kind, objective: text}
	}
}

// parseObjectiveArg joins args into an objective, returning emptyKind when the
// text is empty, else filledKind with the objective.
func parseObjectiveArg(args []string, emptyKind, filledKind string) parsedGoalArgs {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return parsedGoalArgs{kind: emptyKind}
	}
	return parsedGoalArgs{kind: filledKind, objective: text}
}

func (c *GoalCommand) showStatus(ctx core.Context) error {
	result := c.Mode.GetGoal()
	if result.Goal == nil {
		writeStr(ctx, "No current goal.\n")
		return nil
	}
	g := result.Goal
	name := g.Name
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	writeFmt(ctx, "Goal [%s]: %s\n", name, g.Objective)
	writeFmt(ctx, "Status: %s\n", g.Status)
	writeFmt(ctx, "Turns: %d\n", g.TurnsUsed)
	writeFmt(ctx, "Tokens: %s\n", goal.FormatTokens(g.TokensUsed))
	writeFmt(ctx, "Elapsed: %s\n", goal.FormatElapsed(g.WallClockMs))
	return nil
}

func (c *GoalCommand) pause(ctx core.Context) error {
	if err := c.rejectIfManaged("pause"); err != nil {
		return err
	}
	if c.Mode.GetGoal().Goal == nil {
		return fmt.Errorf("no current goal to pause")
	}
	reason := "paused by user"
	_, err := c.Mode.PauseGoal(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorUser)
	if err != nil {
		return err
	}
	writeStr(ctx, "Goal paused.\n")
	return nil
}

func (c *GoalCommand) resume(ctx core.Context) error {
	if err := c.rejectIfManaged("resume"); err != nil {
		return err
	}
	if c.Mode.GetGoal().Goal == nil {
		return fmt.Errorf("no current goal to resume")
	}
	_, err := c.Mode.ResumeGoal(goal.GoalReasonInput{}, goal.GoalActorUser)
	if err != nil {
		return err
	}
	writeStr(ctx, "Goal resumed.\n")
	return nil
}

func (c *GoalCommand) cancel(ctx core.Context) error {
	if err := c.rejectIfManaged("cancel"); err != nil {
		return err
	}
	if c.Mode.GetGoal().Goal == nil {
		return fmt.Errorf("no current goal to cancel")
	}
	_, err := c.Mode.CancelGoal(goal.GoalActorUser)
	if err != nil {
		return err
	}
	writeStr(ctx, "Goal cancelled.\n")
	return nil
}

func (c *GoalCommand) queueNext(ctx core.Context, objective string) error {
	goals, err := c.Queue.Append(objective)
	if err != nil {
		return err
	}
	added := goals[len(goals)-1]
	name := added.Name
	if name == "" {
		writeFmt(ctx, "Queued goal #%d: %s\n", len(goals), objective)
	} else {
		writeFmt(ctx, "Queued goal #%d [%s]: %s\n", len(goals), name, objective)
	}
	return nil
}

func (c *GoalCommand) showQueueManager(ctx core.Context) error {
	goals, err := c.Queue.Read()
	if err != nil {
		return err
	}
	if len(goals) == 0 {
		writeStr(ctx, "No queued goals.\n")
		return nil
	}
	items := make([]tui.SelectorItem, 0, len(goals)+1)
	for i, g := range goals {
		label := truncate(g.Objective, 60)
		if g.Name != "" {
			label = fmt.Sprintf("%s — %s", g.Name, label)
		}
		items = append(items, tui.SelectorItem{
			Value:       g.ID,
			Label:       label,
			Description: fmt.Sprintf("%c", 'A'+i),
		})
	}
	items = append(items, tui.SelectorItem{Value: "__done__", Label: "Done", Description: "Close queue manager"})
	ctx.SelectOption("Manage queued goals", items, "", func(selected string, ok bool) {
		if !ok || selected == "__done__" {
			return
		}
		c.promptQueueAction(ctx, selected)
	})
	return nil
}

func (c *GoalCommand) promptQueueAction(ctx core.Context, id string) {
	actions := []tui.SelectorItem{
		{Value: "up", Label: "Move up"},
		{Value: "down", Label: "Move down"},
		{Value: "delete", Label: "Delete"},
		{Value: "cancel", Label: "Cancel"},
	}
	ctx.SelectOption("Queue action", actions, "", func(action string, ok bool) {
		if !ok || action == "cancel" {
			return
		}
		switch action {
		case "up", "down":
			if _, err := c.Queue.Move(id, action); err != nil {
				ctx.Flash(err.Error())
			}
		case "delete":
			if _, _, err := c.Queue.Remove(id); err != nil {
				ctx.Flash(err.Error())
			}
		}
	})
}

func (c *GoalCommand) reorderQueue(ctx core.Context, mapping string) error {
	goals, err := c.Queue.ReorderByMapping(mapping)
	if err != nil {
		return err
	}
	writeStr(ctx, "Queue reordered:\n")
	for i, g := range goals {
		name := g.Name
		if name == "" {
			name = "(unnamed)"
		}
		writeFmt(ctx, "%d. %s — %s\n", i+1, name, truncate(g.Objective, 60))
	}
	return nil
}

// goalPlacement describes where a newly-created goal should go.
type goalPlacement int

const (
	// placementAsk prompts the user (first/active vs last/queue) — used when a
	// goal is already active. Equivalent to the item-4 "1st or last" prompt.
	placementAsk goalPlacement = iota
	// placementFirst replaces the active goal (becomes first).
	placementFirst
	// placementLast appends to the queue (runs after current).
	placementLast
)

// create handles /goal:new:<text> and bare /goal:<text>.
// When a goal is already active, it asks whether to become first (replace) or
// last (queue) — the item-4 prompt.
func (c *GoalCommand) create(ctx core.Context, objective string) error {
	if c.Mode.GetGoal().Goal != nil {
		return c.promptFirstOrLast(ctx, objective)
	}
	return c.startGoal(ctx, objective, false)
}

// replace handles /goal:replace:<text>. It asks for confirmation before
// discarding the current goal, then proceeds through the autonomy permission
// flow.
func (c *GoalCommand) replace(ctx core.Context, objective string) error {
	current := c.Mode.GetGoal().Goal
	if current == nil {
		return fmt.Errorf("no current goal to replace")
	}
	if err := c.rejectIfManaged("replace"); err != nil {
		return err
	}
	c.promptReplaceConfirm(ctx, current, objective)
	return nil
}

// promptFirstOrLast asks the user where to put a new goal when one is already
// active (item 4). "First/active" replaces the current goal; "Last" queues it.
// Per the UX guideline, the active goal's details are shown FIRST so the user
// can decide what to do with the running goal.
func (c *GoalCommand) promptFirstOrLast(ctx core.Context, objective string) error {
	current := c.Mode.GetGoal().Goal
	c.describeActiveGoal(ctx, current)
	activeLabel := "<current goal>"
	if current != nil && current.Name != "" {
		activeLabel = current.Name
	}
	opts := []tui.SelectorItem{
		{Value: "first", Label: "Replace the active goal", Description: fmt.Sprintf("Discard %s and start the new goal now.", activeLabel)},
		{Value: "last", Label: "Queue it for later", Description: "Append to the queue; runs after the current goal completes."},
		{Value: "cancel", Label: "Do not create", Description: "Return to the input box."},
	}
	ctx.SelectOption("A goal is already active — where should the new goal go?", opts, "", func(selected string, ok bool) {
		if !ok || selected == "cancel" {
			return
		}
		switch selected {
		case "first":
			_ = c.startGoal(ctx, objective, true)
		case "last":
			_ = c.queueNext(ctx, objective)
		}
	})
	return nil
}

// promptReplaceConfirm asks the user to confirm replacing the active goal.
func (c *GoalCommand) promptReplaceConfirm(ctx core.Context, current *goal.GoalSnapshot, objective string) {
	activeLabel := current.Name
	if activeLabel == "" {
		activeLabel = "<current goal>"
	}
	opts := []tui.SelectorItem{
		{Value: "replace", Label: "Yes, replace it", Description: fmt.Sprintf("Discard %s and start the new goal.", activeLabel)},
		{Value: "cancel", Label: "No, keep it", Description: "Return to the input box."},
	}
	title := fmt.Sprintf("Replace goal %s (%s) with: %s", activeLabel, truncate(current.Objective, 40), truncate(objective, 60))
	ctx.SelectOption(title, opts, "", func(selected string, ok bool) {
		if !ok || selected == "cancel" {
			return
		}
		_ = c.startGoal(ctx, objective, true)
	})
}

func (c *GoalCommand) rejectIfManaged(op string) error {
	g := c.Mode.GetGoal().Goal
	if g != nil && g.ManagedBy == "orchestrator" {
		return fmt.Errorf("goal %s is managed by /orchestrate; cannot %s", g.Name, op)
	}
	return nil
}

// describeActiveGoal writes a short summary of the currently active goal to
// the output so the user has context before being asked what to do next.
// No-op when there is no active goal.
func (c *GoalCommand) describeActiveGoal(ctx core.Context, g *goal.GoalSnapshot) {
	if g == nil {
		return
	}
	name := g.Name
	if name == "" {
		name = "<unnamed>"
	}
	writeFmt(ctx, "Active goal [%s]: %s\n", name, g.Objective)
	writeFmt(ctx, "Status: %s | Turns: %d | Tokens: %s | Elapsed: %s\n",
		g.Status, g.TurnsUsed, goal.FormatTokens(g.TokensUsed), goal.FormatElapsed(g.WallClockMs))
}

// promptCreateInteractive drives the interactive create/queue flow via the
// main input line: ctrl-c (or empty) aborts; a typed objective proceeds
// according to placement. For placementAsk, the first/last prompt follows.
func (c *GoalCommand) promptCreateInteractive(ctx core.Context, placement goalPlacement) error {
	promptText := "Set new goal objective (ctrl-c to cancel)"
	if placement == placementLast {
		promptText = "Queue a goal objective — it runs after the current one (ctrl-c to cancel)"
	}
	if ctx.RequestMainInput == nil {
		return fmt.Errorf("main input not available")
	}
	ctx.RequestMainInput(promptText, func(value string) {
		objective := strings.TrimSpace(value)
		if objective == "" {
			return
		}
		switch placement {
		case placementLast:
			_ = c.queueNext(ctx, objective)
		case placementFirst:
			_ = c.startGoal(ctx, objective, true)
		default: // placementAsk
			_ = c.create(ctx, objective)
		}
	})
	return nil
}

// promptReplaceInteractive drives /goal:replace without text: asks for the
// objective on the main input line, then confirms before replacing.
func (c *GoalCommand) promptReplaceInteractive(ctx core.Context) error {
	current := c.Mode.GetGoal().Goal
	if current == nil {
		return fmt.Errorf("no current goal to replace")
	}
	if ctx.RequestMainInput == nil {
		return fmt.Errorf("main input not available")
	}
	ctx.RequestMainInput("Replace active goal with objective (ctrl-c to cancel)", func(value string) {
		objective := strings.TrimSpace(value)
		if objective == "" {
			return
		}
		c.promptReplaceConfirm(ctx, current, objective)
	})
	return nil
}

func (c *GoalCommand) startGoal(ctx core.Context, objective string, replace bool) error {
	if c.AutonomySwitcher != nil {
		level := c.AutonomySwitcher.Current()
		if level != internal.AutonomyYolo {
			c.promptStartPermission(ctx, objective, replace, level)
			return nil
		}
	}
	return c.doStartGoal(ctx, objective, replace)
}

func (c *GoalCommand) promptStartPermission(ctx core.Context, objective string, replace bool, current internal.AutonomyLevel) {
	opts := c.permissionOptions(current)
	ctx.SelectOption("Start a goal?", opts, "", func(selected string, ok bool) {
		if !ok {
			return
		}
		switch selected {
		case "auto":
			_ = c.AutonomySwitcher.SetAutonomy(internal.AutonomyConfirm)
		case "yolo":
			_ = c.AutonomySwitcher.SetAutonomy(internal.AutonomyYolo)
		case "manual":
			_ = c.AutonomySwitcher.SetAutonomy(internal.AutonomyConfirm)
		case "cancel":
			ctx.Flash("Goal start cancelled.")
			return
		}
		_ = c.doStartGoal(ctx, objective, replace)
	})
}

func (c *GoalCommand) permissionOptions(current internal.AutonomyLevel) []tui.SelectorItem {
	if current == internal.AutonomyYolo {
		return []tui.SelectorItem{
			{Value: "auto", Label: "Switch to Auto and start", Description: "Tools approved automatically, questions skipped."},
			{Value: "yolo", Label: "Keep YOLO and start", Description: "Tools auto-approved, model may still ask questions."},
			{Value: "cancel", Label: "Do not start", Description: "Return to the input box."},
		}
	}
	return []tui.SelectorItem{
		{Value: "auto", Label: "Switch to Auto and start", Description: "Best for unattended work. Tools are approved automatically."},
		{Value: "yolo", Label: "Switch to YOLO and start", Description: "Tools auto-approved, model may still ask questions."},
		{Value: "manual", Label: "Start in Manual", Description: "Goal may stop and wait for your approval. Not suitable for unattended goal work."},
		{Value: "cancel", Label: "Do not start", Description: "Return to the input box."},
	}
}

func (c *GoalCommand) doStartGoal(ctx core.Context, objective string, replace bool) error {
	snap, err := c.Mode.CreateGoal(goal.CreateGoalInput{
		Objective: objective,
		Replace:   replace,
	}, goal.GoalActorUser)
	if err != nil {
		ctx.Flash(err.Error())
		return err
	}
	name := snap.Name
	ctx.Flash("Started goal: " + snap.Objective)
	if name != "" {
		writeFmt(ctx, "Started goal [%s]: %s\n", name, snap.Objective)
	} else {
		writeFmt(ctx, "Started goal: %s\n", snap.Objective)
	}
	if c.Driver != nil {
		c.Driver.Start(context.Background())
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// goalSubcommands is the fixed list of /goal:<sub> keywords offered by
// CompleteArgs, with short descriptions for the completion popup.
var goalSubcommands = []struct {
	value string
	desc  string
}{
	{"new", "create a new goal"},
	{"next", "queue a goal to run after the current one"},
	{"replace", "replace the current goal"},
	{"manage", "open the queued-goals manager"},
	{"reorder", "reorder queue with letter mapping"},
	{"status", "show current goal status"},
	{"pause", "pause the active goal"},
	{"resume", "resume a paused goal"},
	{"cancel", "discard the current goal"},
}

// CompleteArgs implements core.ArgCompleter, providing /goal:<tab> completion
// for subcommand keywords. The router passes the raw text after "goal" as
// prefix (e.g. "ne" for /goal:ne).
func (c *GoalCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var comps []core.ArgCompletion
	for _, sc := range goalSubcommands {
		if prefix == "" || strings.HasPrefix(sc.value, prefix) {
			comps = append(comps, core.ArgCompletion{
				Value:       sc.value,
				Description: sc.desc,
			})
		}
	}
	return comps
}
