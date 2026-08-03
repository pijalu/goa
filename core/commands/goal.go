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
	// FreshContextDefault reports the configured default context mode for new
	// goals (goals.fresh_context; default true = clean context).
	// /goal:new:fresh and /goal:new:reuse override it per command. Nil = true.
	FreshContextDefault func() bool
}

// resolveFresh maps the parsed per-command context token ("" | "fresh" |
// "reuse") onto the configured default (goals.fresh_context, default true).
func (c *GoalCommand) resolveFresh(contextMode string) bool {
	switch contextMode {
	case "fresh":
		return true
	case "reuse":
		return false
	}
	if c.FreshContextDefault != nil {
		return c.FreshContextDefault()
	}
	return true
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
	"status":     func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.showStatus(ctx) },
	"current":    func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.showCurrent(ctx) },
	"list":       func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.showList(ctx) },
	"pause":      func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.pause(ctx) },
	"resume":     func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.resume(ctx) },
	"cancel":     func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.cancel(ctx) },
	"cancel-all": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.cancelAll(ctx) },
	"manage":     func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.showQueueManager(ctx) },
	"log":        func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.showEventLog(ctx) },
	"verify":     func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.runVerify(ctx) },
	"next-add": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		if p.placement == placementLast {
			return c.queueLast(ctx, p.objective, c.resolveFresh(p.contextMode))
		}
		return c.queueNext(ctx, p.objective, c.resolveFresh(p.contextMode))
	},
	"next-interactive": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		return c.promptCreateInteractive(ctx, p.placement)
	},
	"reorder": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		return c.reorderQueue(ctx, p.objective)
	},
	"create": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		return c.create(ctx, p.objective, c.resolveFresh(p.contextMode))
	},
	"create-interactive": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error {
		return c.promptCreateInteractive(ctx, placementAsk)
	},
	"replace":             func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error { return c.replace(ctx, p.objective) },
	"replace-interactive": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.promptReplaceInteractive(ctx) },
	"settings":            func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.openSettings(ctx) },
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
	// contextMode carries the per-command context token from
	// /goal:new:fresh|reuse (or /goal:next:fresh|reuse): "" = configured
	// default, "fresh" = clean context, "reuse" = keep conversation.
	contextMode string
	// placement carries the /goal:next placement token (/goal:next:first|
	// last, default first). Zero value (placementAsk) for other commands.
	placement goalPlacement
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
	subNone     subcommandMode = iota // status/pause/resume/manage
	subOptional                       // new/next/replace: bare → interactive, with text → action
	subRequired                       // reorder: requires a mapping arg
	// subScope maps an optional scope token via scopeKinds ("" is the bare
	// form); unknown tokens emit errorHint as a hint. Used by /goal:cancel
	// for its :current/:all variants.
	subScope
)

// goalSubcommandKinds maps each subcommand keyword to its parse behavior and
// resulting parsedGoalArgs.kind (or kind pattern). Table-driven to keep
// parseSubcommand under the cyclomatic budget.
var goalSubcommandKinds = map[string]struct {
	mode      subcommandMode
	kind      string // kind when text is present
	bareKind  string // kind when no text (subOptional)
	errorHint string // non-empty → emit this usage hint when text missing
	// scopeKinds (subScope only): allowed scope tokens (lowercased) → kind;
	// the "" key is the bare form.
	scopeKinds map[string]string
}{
	"status":  {mode: subNone, kind: "status"},
	"current": {mode: subNone, kind: "current"},
	"list":    {mode: subNone, kind: "list"},
	"pause":   {mode: subNone, kind: "pause"},
	"resume":  {mode: subNone, kind: "resume"},
	"cancel": {mode: subScope, errorHint: "usage: /goal:cancel[:current|all]",
		scopeKinds: map[string]string{"": "cancel", "current": "cancel", "all": "cancel-all"}},
	"manage":   {mode: subNone, kind: "manage"},
	"log":      {mode: subNone, kind: "log"},
	"verify":   {mode: subNone, kind: "verify"},
	"new":      {mode: subOptional, kind: "create", bareKind: "create-interactive"},
	"next":     {mode: subOptional, kind: "next-add", bareKind: "next-interactive"},
	"replace":  {mode: subOptional, kind: "replace", bareKind: "replace-interactive"},
	"reorder":  {mode: subRequired, kind: "reorder", errorHint: "usage: /goal:reorder <mapping> (e.g. 1B,2C,3A)"},
	"settings": {mode: subNone, kind: "settings"},
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
	case subScope:
		// Optional scope token (/goal:cancel, /goal:cancel:current,
		// /goal:cancel:all); anything else is a usage hint, not an objective.
		kind, ok := spec.scopeKinds[strings.ToLower(text)]
		if !ok {
			return parsedGoalArgs{kind: "error", message: spec.errorHint, severity: "hint"}
		}
		return parsedGoalArgs{kind: kind}
	default: // subOptional
		return parseOptionalGoalArgs(cmd, spec.kind, spec.bareKind, text)
	}
}

// parseOptionalGoalArgs parses subOptional subcommands (new/next/replace):
// bare → bareKind (interactive); with text → kind. /goal:next consumes
// optional leading placement (first|last, default first) and context-mode
// (fresh|reuse) tokens in any order; /goal:new consumes a context token.
func parseOptionalGoalArgs(cmd, kind, bareKind, text string) parsedGoalArgs {
	if cmd == "next" {
		placement, mode, rest := splitGoalNextArgs(text)
		if rest == "" {
			return parsedGoalArgs{kind: bareKind, contextMode: mode, placement: placement}
		}
		return parsedGoalArgs{kind: kind, objective: rest, contextMode: mode, placement: placement}
	}
	// /goal:new:fresh <text> and /goal:new:reuse <text> carry a leading
	// context-mode token that overrides the configured default.
	if cmd == "new" {
		mode, rest := splitGoalContextToken(text)
		if rest == "" {
			return parsedGoalArgs{kind: bareKind, contextMode: mode}
		}
		return parsedGoalArgs{kind: kind, objective: rest, contextMode: mode}
	}
	if text == "" {
		return parsedGoalArgs{kind: bareKind}
	}
	return parsedGoalArgs{kind: kind, objective: text}
}

// splitGoalNextArgs parses /goal:next arguments: optional leading placement
// (first|last, default first) and context-mode (fresh|reuse) tokens in any
// order, followed by the objective text. Examples:
//
//	"fix tests"        → (placementNext, "", "fix tests")
//	"last fresh audit" → (placementLast, "fresh", "audit")
//	"last"             → (placementLast, "", "") → interactive
func splitGoalNextArgs(text string) (placement goalPlacement, mode, rest string) {
	placement = placementNext
	rest = text
	for {
		tok, tail, ok := splitLeadingToken(rest, "first", "last", "fresh", "reuse")
		if !ok {
			return placement, mode, rest
		}
		if tok == "last" {
			placement = placementLast
		} else if tok == "fresh" || tok == "reuse" {
			mode = tok
		}
		rest = tail
	}
}

// splitGoalContextToken extracts a leading fresh/reuse context-mode token
// from the objective text (/goal:new:fresh fix, /goal:next:reuse investigate).
// Returns ("", text) unchanged when there is no token.
func splitGoalContextToken(text string) (mode, rest string) {
	tok, tail, ok := splitLeadingToken(text, "fresh", "reuse")
	if !ok {
		return "", text
	}
	return tok, tail
}

// splitLeadingToken returns the first word of text when it is one of tokens,
// plus the remaining text, trimmed. ok is false when no token matches.
func splitLeadingToken(text string, tokens ...string) (tok, rest string, ok bool) {
	for _, t := range tokens {
		if text == t {
			return t, "", true
		}
		if strings.HasPrefix(text, t+" ") {
			return t, strings.TrimSpace(strings.TrimPrefix(text, t+" ")), true
		}
	}
	return "", text, false
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

// showCurrent implements /goal:current: print the currently executed goal
// with its full (untruncated) objective, completion criterion, verify command
// and todo list with statuses — richer than /goal:status, which shows only
// counters. Rendered as markdown so it formats in the chat panel.
func (c *GoalCommand) showCurrent(ctx core.Context) error {
	result := c.Mode.GetGoal()
	if result.Goal == nil {
		writeStr(ctx, "No current goal.\n")
		return nil
	}
	g := result.Goal
	var sb strings.Builder
	name := g.Name
	if name == "" {
		name = "(unnamed)"
	}
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	fmt.Fprintf(&sb, "**%s** — status %s · turns %d · %s tokens · %s\n\n",
		name, g.Status, g.TurnsUsed, goal.FormatTokens(g.TokensUsed), goal.FormatElapsed(g.WallClockMs))
	fmt.Fprintf(&sb, "%s\n\n", g.Objective)
	if g.CompletionCriterion != nil {
		fmt.Fprintf(&sb, "- Completion criterion: %s\n", *g.CompletionCriterion)
	}
	if g.VerifyCommand != nil {
		fmt.Fprintf(&sb, "- Verify command: `%s`\n", *g.VerifyCommand)
	}
	if g.CompletionCriterion != nil || g.VerifyCommand != nil {
		sb.WriteString("\n")
	}
	writeGoalTodos(&sb, g.Todos)
	writeStr(ctx, sb.String())
	return nil
}

// writeGoalTodos renders the todo list with status markers, one per line.
func writeGoalTodos(sb *strings.Builder, todos []goal.GoalTodoItem) {
	if len(todos) == 0 {
		return
	}
	done := 0
	for _, td := range todos {
		if td.Status == goal.TodoDone {
			done++
		}
	}
	fmt.Fprintf(sb, "Todos (%d/%d done):\n\n", done, len(todos))
	for _, td := range todos {
		fmt.Fprintf(sb, "- %s %s\n", todoStatusMark(td.Status), td.Title)
	}
}

// todoStatusMark renders a todo status as a checkbox-style marker.
func todoStatusMark(status string) string {
	switch status {
	case goal.TodoDone:
		return "[x]"
	case goal.TodoInProgress:
		return "[>]"
	default:
		return "[ ]"
	}
}

// showList implements /goal:list: list the current goal and every queued goal
// in execution order, showing ALL information recorded for each goal —
// placement, name, status, counters, context run type (fresh/reuse),
// completion criterion, verify command, handover, budget, terminal state and
// todos — plus the COMPLETE objective (no truncation). The output is markdown
// so it renders formatted in the chat panel — the counterpart to the goal
// bubble, which caps its display at 3 lines.
func (c *GoalCommand) showList(ctx core.Context) error {
	active := c.Mode.GetGoal().Goal
	queued, err := c.Queue.Read()
	if err != nil {
		return err
	}
	if active == nil && len(queued) == 0 {
		writeStr(ctx, "No goals.\n")
		return nil
	}

	var sb strings.Builder
	sb.WriteString("## Goals\n\n")
	order := 1
	if active != nil {
		writeCurrentGoalListEntry(&sb, order, active)
		order++
	}
	for i := range queued {
		writeQueuedGoalListEntry(&sb, order, &queued[i])
		order++
	}
	writeStr(ctx, sb.String())
	return nil
}

// writeGoalListEntry renders one goal as a markdown list item: a bold header
// with order number, placement and name, an optional metadata line, then the
// complete untruncated objective on its own line. The parts are separated by
// blank lines so the markdown renderer keeps them as DISTINCT blocks —
// consecutive plain lines would soft-join into a single paragraph (bugs.md
// "Goal list issue"). Blank separator lines are dropped by the renderer, so
// this costs no extra rows.
func writeGoalListEntry(sb *strings.Builder, order int, placement, name, meta, objective string) {
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(sb, "**%d. [%s] %s**\n\n", order, placement, name)
	if meta != "" {
		fmt.Fprintf(sb, "*%s*\n\n", meta)
	}
	fmt.Fprintf(sb, "%s\n\n", objective)
}

// contextRunLabel renders the goal's context run type: "fresh" runs the
// continuation turns on a clean context (objective + handover only), "reuse"
// reuses the current conversation.
func contextRunLabel(fresh bool) string {
	if fresh {
		return "fresh"
	}
	return "reuse"
}

// formatGoalBudget renders a compact "budget used/limit" summary for goals
// with hard limits; unlimited goals yield "".
func formatGoalBudget(g *goal.GoalSnapshot) string {
	parts := make([]string, 0, 3)
	if g.Budget.TurnBudget != nil {
		parts = append(parts, fmt.Sprintf("turns %d/%d", g.TurnsUsed, *g.Budget.TurnBudget))
	}
	if g.Budget.TokenBudget != nil {
		parts = append(parts, fmt.Sprintf("tokens %s/%s", goal.FormatTokens(g.TokensUsed), goal.FormatTokens(*g.Budget.TokenBudget)))
	}
	if g.Budget.WallClockBudgetMs != nil {
		parts = append(parts, fmt.Sprintf("time %s/%s", goal.FormatElapsed(g.WallClockMs), goal.FormatElapsed(*g.Budget.WallClockBudgetMs)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "budget " + strings.Join(parts, " · ")
}

// writeGoalAttrs renders the optional attributes shared by current and queued
// goals — completion criterion, verify command and handover — as one markdown
// bullet block. A set handover is shown as a presence marker only: its text
// is untrusted free-form content carried into the goal's reminder, not list
// material.
func writeGoalAttrs(sb *strings.Builder, criterion, verifyCommand, handover *string) {
	wrote := false
	if criterion != nil {
		fmt.Fprintf(sb, "- Completion criterion: %s\n", *criterion)
		wrote = true
	}
	if verifyCommand != nil {
		fmt.Fprintf(sb, "- Verify command: `%s`\n", *verifyCommand)
		wrote = true
	}
	if handover != nil {
		sb.WriteString("- Handover: attached\n")
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

// writeGoalTerminalState renders the terminal reason/expectation of a paused
// or blocked goal as a markdown bullet block. Goals without a terminal state
// yield nothing.
func writeGoalTerminalState(sb *strings.Builder, g *goal.GoalSnapshot) {
	wrote := false
	if g.TerminalReason != nil {
		fmt.Fprintf(sb, "- Reason: %s\n", *g.TerminalReason)
		wrote = true
	}
	if g.TerminalExpectation != nil {
		fmt.Fprintf(sb, "- Needs: %s\n", *g.TerminalExpectation)
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

// writeCurrentGoalListEntry renders the current goal (whatever its status)
// with its full information: counters, context run type, budget, criterion,
// verify command, handover, terminal state and todos.
func writeCurrentGoalListEntry(sb *strings.Builder, order int, g *goal.GoalSnapshot) {
	name := g.Name
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	meta := fmt.Sprintf("status %s · turns %d · %s tokens · %s · context %s",
		g.Status, g.TurnsUsed, goal.FormatTokens(g.TokensUsed), goal.FormatElapsed(g.WallClockMs), contextRunLabel(g.FreshContext))
	if budget := formatGoalBudget(g); budget != "" {
		meta += " · " + budget
	}
	writeGoalListEntry(sb, order, "active", name, meta, g.Objective)
	writeGoalAttrs(sb, g.CompletionCriterion, g.VerifyCommand, g.Handoff)
	writeGoalTerminalState(sb, g)
	if len(g.Todos) > 0 {
		writeGoalTodos(sb, g.Todos)
		sb.WriteString("\n")
	}
}

// writeQueuedGoalListEntry renders a queued goal with its full information:
// context run type, queue time, criterion, verify command and handover.
func writeQueuedGoalListEntry(sb *strings.Builder, order int, g *goal.UpcomingGoal) {
	name := g.Name
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	meta := "context " + contextRunLabel(g.FreshContext)
	if !g.CreatedAt.IsZero() {
		meta += " · queued " + g.CreatedAt.Format("2006-01-02 15:04")
	}
	writeGoalListEntry(sb, order, "queued", name, meta, g.Objective)
	writeGoalAttrs(sb, g.CompletionCriterion, g.VerifyCommand, g.Handoff)
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
	// Resume must actually restart autonomous execution, not just flip state:
	// schedule the continuation turn the same way goal creation does, so the
	// goal proceeds without requiring a user message. Start is a no-op when a
	// drive loop is already running.
	if c.Driver != nil {
		c.Driver.Start(context.Background())
	}
	return nil
}

// cancel implements /goal:cancel (and /goal:cancel:current): discard the
// current goal. The clear event promotes the queued successor PAUSED — a
// cancel never auto-starts the next goal; the user resumes it explicitly.
func (c *GoalCommand) cancel(ctx core.Context) error {
	if err := c.rejectIfManaged("cancel"); err != nil {
		return err
	}
	if c.Mode.GetGoal().Goal == nil {
		return fmt.Errorf("no current goal to cancel")
	}
	// Snapshot the queue BEFORE the cancel: the successor promotion happens
	// asynchronously once the clear event crosses the bus.
	queued := 0
	if c.Queue != nil {
		if q, err := c.Queue.Read(); err == nil {
			queued = len(q)
		}
	}
	_, err := c.Mode.CancelGoal(goal.GoalActorUser)
	if err != nil {
		return err
	}
	// A cancelled goal has nothing left to execute: stop the drive loop so
	// the in-flight continuation turn is interrupted (its context is the
	// loop's) and no further continuation turns launch — the "Answering..."
	// spinner must not survive the cancel. Mirrors the goal half of the ESC
	// hard stop (handleEscape). Deliberately NOT AgentManager.Interrupt: a
	// user-owned turn in flight is not the goal's business. Order matters:
	// CancelGoal first clears the state, so the interrupted turn's error
	// handling (PauseActiveGoal) no-ops instead of pausing a dead goal.
	if c.Driver != nil {
		c.Driver.Stop()
	}
	writeStr(ctx, "Goal cancelled.\n")
	if queued > 0 {
		writeStr(ctx, "The next queued goal is promoted paused — /goal:resume to start it.\n")
	}
	return nil
}

// cancelAll implements /goal:cancel:all: discard the current goal AND clear
// every queued goal. The queue is cleared FIRST: queue operations emit no
// goal events, so the clear event's successor promotion (async, on the
// event-forwarder goroutine) then finds an empty queue and stays a no-op.
func (c *GoalCommand) cancelAll(ctx core.Context) error {
	if err := c.rejectIfManaged("cancel"); err != nil {
		return err
	}
	cleared := 0
	if c.Queue != nil {
		goals, err := c.Queue.Clear()
		if err != nil {
			return err
		}
		cleared = len(goals)
	}
	if c.Mode.GetGoal().Goal == nil {
		if cleared == 0 {
			return fmt.Errorf("nothing to cancel: no current goal and the queue is empty")
		}
		writeFmt(ctx, "Cleared %d queued goal(s).\n", cleared)
		return nil
	}
	if _, err := c.Mode.CancelGoal(goal.GoalActorUser); err != nil {
		return err
	}
	if c.Driver != nil {
		c.Driver.Stop()
	}
	writeFmt(ctx, "Goal cancelled; %d queued goal(s) cleared.\n", cleared)
	return nil
}

// goalLogLimit caps how many event-log records /goal:log renders.
const goalLogLimit = 20

// showEventLog implements /goal:log: render the recent goal event records
// (time, type, actor, status, reason, expectation) from the durable log.
func (c *GoalCommand) showEventLog(ctx core.Context) error {
	records, err := c.Mode.EventLog()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		writeStr(ctx, "No goal events recorded.\n")
		return nil
	}
	if len(records) > goalLogLimit {
		writeFmt(ctx, "(showing last %d of %d records)\n", goalLogLimit, len(records))
		records = records[len(records)-goalLogLimit:]
	}
	for _, r := range records {
		writeStr(ctx, formatGoalEventRecord(r))
	}
	return nil
}

// formatGoalEventRecord renders one event-log record as a single line,
// appending optional fields only when present.
func formatGoalEventRecord(r goal.GoalEventRecord) string {
	var b strings.Builder
	b.WriteString(r.Timestamp.Format("15:04:05"))
	b.WriteString("  ")
	b.WriteString(string(r.Type))
	if r.Actor != nil {
		b.WriteString("  actor=")
		b.WriteString(*r.Actor)
	}
	if r.Status != nil {
		b.WriteString("  status=")
		b.WriteString(*r.Status)
	}
	if r.Name != nil {
		b.WriteString("  name=")
		b.WriteString(*r.Name)
	}
	if r.Reason != nil {
		b.WriteString("  reason=")
		b.WriteString(truncate(*r.Reason, 60))
	}
	if r.Expectation != nil {
		b.WriteString("  expectation=")
		b.WriteString(truncate(*r.Expectation, 60))
	}
	b.WriteByte('\n')
	return b.String()
}

// runVerify implements /goal:verify: execute the current goal's recorded
// verify command on demand and print its output plus PASS/FAIL.
func (c *GoalCommand) runVerify(ctx core.Context) error {
	output, ok, err := c.Mode.RunVerifyCommand(context.Background())
	if err != nil {
		return err
	}
	if output != "" {
		writeStr(ctx, output)
		if !strings.HasSuffix(output, "\n") {
			writeStr(ctx, "\n")
		}
	}
	if ok {
		writeStr(ctx, "verify command: PASS\n")
	} else {
		writeStr(ctx, "verify command: FAIL\n")
	}
	return nil
}

// openSettings implements /goal:settings — a selector mirroring the
// /config → Goals menu, so both entry points expose the same toggles with the
// same UX. Currently: auto-unblock on/off.
func (c *GoalCommand) openSettings(ctx core.Context) error {
	enabled := ctx.Config.Goals.AutoUnblockEnabled()
	items := []tui.SelectorItem{
		{Value: "auto_unblock", Label: "Auto-unblock goals", Description: boolLabel(enabled)},
	}
	ctx.SelectOption("Goal settings:", items, "", func(field string, ok bool) {
		if !ok || field != "auto_unblock" {
			return
		}
		g := &ctx.Config.Goals
		next := !g.AutoUnblockEnabled()
		g.AutoUnblock = &next
		if ctx.ConfigSaver != nil {
			if err := ctx.ConfigSaver.Save(ctx.Config); err != nil {
				ctx.Flash("Failed to save config: " + err.Error())
				return
			}
		}
		ctx.Flash("Auto-unblock goals " + goalOnOffLabel(next))
	})
	return nil
}

// goalOnOffLabel renders an on/off state for flash messages (matches the
// config menu's toggle feedback).
func goalOnOffLabel(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

// queueNext inserts a goal at the FRONT of the durable queue (/goal:next and
// /goal:next:first): it is promoted NEXT, right after the active goal
// completes. fresh is the resolved context mode — stored with the goal so
// its turns run on a clean context (or the surviving conversation) when it
// is promoted.
func (c *GoalCommand) queueNext(ctx core.Context, objective string, fresh bool) error {
	goals, err := c.Queue.PrependGoal(goal.UpcomingGoalInput{
		Objective:    objective,
		FreshContext: fresh,
	})
	if err != nil {
		return err
	}
	name := goals[0].Name
	if name == "" {
		writeFmt(ctx, "Queued goal to run next (queue: %d): %s\n", len(goals), objective)
	} else {
		writeFmt(ctx, "Queued goal to run next (queue: %d) [%s]: %s\n", len(goals), name, objective)
	}
	return nil
}

// queueLast appends a goal to the END of the durable queue (/goal:next:last
// and the "Queue it for later" choice of the first-or-last prompt).
func (c *GoalCommand) queueLast(ctx core.Context, objective string, fresh bool) error {
	goals, err := c.Queue.AppendGoal(goal.UpcomingGoalInput{
		Objective:    objective,
		FreshContext: fresh,
	})
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

// goalManagerKeymap repurposes '+'/'-' from add/delete to direct reordering
// for /goal:manage; Delete/Backspace keeps emitting the delete sentinel.
var goalManagerKeymap = tui.SelectorKeymap{ReorderMode: true}

// showQueueManager implements /goal:manage: an interactive manager listing
// the goals in EXECUTION ORDER — the active goal first (marked [active], not
// movable), then the queued goals in run order — framed by sentinel rows to
// add goals at the start/end. With the reorder keymap, '+' moves the
// highlighted goal up one position and '-' moves it down (direct hotkeys, no
// submenu); Delete/Backspace asks for confirmation before removing; 'e'
// edits the description; Enter on an add row opens the create-goal flow.
func (c *GoalCommand) showQueueManager(ctx core.Context) error {
	return c.showQueueManagerAt(ctx, "")
}

// showQueueManagerAt opens the manager with the cursor (and ✓ marker) on the
// row with the given value — every hotkey emit closes the selector, so the
// manager reopens after each action and cursor keeps it on the row the user
// is working with ("" starts on the first row).
func (c *GoalCommand) showQueueManagerAt(ctx core.Context, cursor string) error {
	queued, err := c.Queue.Read()
	if err != nil {
		return err
	}
	items := c.managerItems(queued)
	ctx.SelectOptionKeyed("Goal manager — execution order", items, cursor, goalManagerKeymap, func(selected string, ok bool) {
		c.handleManagerSelection(ctx, selected, ok)
	})
	return nil
}

// managerItems builds the manager rows in execution order: the add-at-start
// sentinel, the active goal (marked, if any), the queued goals in run order,
// the add-at-end sentinel and the Done row. PreserveOrder on every row keeps
// the selector in caller order — the default alphabetical Label sort would
// scramble the execution order the manager is meant to show. Goal rows opt
// into the 'e' edit hotkey via Editable.
func (c *GoalCommand) managerItems(queued []goal.UpcomingGoal) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, len(queued)+4)
	items = append(items, tui.SelectorItem{
		Value: "__add_first__", Label: "-- add at start --",
		Description: "queue a goal to run next", PreserveOrder: true,
	})
	if active := c.Mode.GetGoal().Goal; active != nil {
		items = append(items, tui.SelectorItem{
			Value:         "__active__",
			Label:         "[active] " + goalRowLabel(active.Name, active.Objective),
			Description:   "running — not reorderable here",
			PreserveOrder: true,
		})
	}
	for i, g := range queued {
		items = append(items, tui.SelectorItem{
			Value:         g.ID,
			Label:         goalRowLabel(g.Name, g.Objective),
			Description:   fmt.Sprintf("%c", 'A'+i),
			Editable:      true,
			PreserveOrder: true,
		})
	}
	items = append(items,
		tui.SelectorItem{Value: "__add_last__", Label: "-- add at end --",
			Description: "queue a goal to run last", PreserveOrder: true},
		tui.SelectorItem{Value: "__done__", Label: "Done",
			Description: "Close goal manager", PreserveOrder: true},
	)
	return items
}

// goalRowLabel renders one goal row as "name — objective…" with the
// objective truncated to fit the selector width.
func goalRowLabel(name, objective string) string {
	label := truncate(objective, 60)
	if name != "" {
		label = fmt.Sprintf("%s — %s", name, label)
	}
	return label
}

// handleManagerSelection dispatches one selector emit from the manager:
// hotkey emits (move up/down, delete, edit), the add rows, the active-goal
// row, and plain Enter on a goal row.
func (c *GoalCommand) handleManagerSelection(ctx core.Context, selected string, ok bool) {
	if !ok || selected == "__done__" {
		return
	}
	if id, yes := strings.CutPrefix(selected, "__moveup__"); yes {
		c.moveManagerGoal(ctx, id, "up")
		return
	}
	if id, yes := strings.CutPrefix(selected, "__movedown__"); yes {
		c.moveManagerGoal(ctx, id, "down")
		return
	}
	if id, yes := strings.CutPrefix(selected, "__delete__"); yes {
		c.confirmDeleteManagerGoal(ctx, id)
		return
	}
	// The selector's 'e' hotkey emits "__edit__"+id for the highlighted goal
	// (goal rows opt in via SelectorItem.Editable): prompt for the new
	// description instead of treating the sentinel as a goal id.
	if id, yes := strings.CutPrefix(selected, "__edit__"); yes {
		c.promptEditQueuedGoal(ctx, id)
		return
	}
	switch selected {
	case "__add_first__":
		c.promptCreateForPlacement(ctx, placementNext)
	case "__add_last__":
		c.promptCreateForPlacement(ctx, placementLast)
	case "__add__":
		// Generic '+' emit — only reachable through a host without the
		// reorder keymap (SelectOptionKeyed fallback). Route it to the add
		// flow: it previously fell through to the queue-action menu and
		// failed with "queued goal … not found" (bugs.md goal manager).
		c.promptCreateForPlacement(ctx, placementLast)
	case "__active__":
		ctx.Flash("The active goal is running — use /goal:pause, /goal:cancel or /goal:replace.")
		_ = c.showQueueManagerAt(ctx, "__active__")
	default:
		// Enter on a queued goal: reorder and delete are hotkey-driven now
		// (the two-step action menu is gone) — remind and reopen.
		ctx.Flash("Hotkeys: '+' move up · '-' move down · 'e' edit · del delete (with confirmation)")
		_ = c.showQueueManagerAt(ctx, selected)
	}
}

// moveManagerGoal implements the '+/-' reorder hotkeys: move the goal one
// position and reopen the manager with the cursor on it, so repeated presses
// keep moving the same goal. The active goal is not movable from the manager.
func (c *GoalCommand) moveManagerGoal(ctx core.Context, id, direction string) {
	if id == "__active__" {
		ctx.Flash("The active goal is running — queued goals reorder around it.")
		_ = c.showQueueManagerAt(ctx, "__active__")
		return
	}
	if _, err := c.Queue.Move(id, direction); err != nil {
		ctx.Flash(err.Error())
		_ = c.showQueueManagerAt(ctx, "")
		return
	}
	_ = c.showQueueManagerAt(ctx, id)
}

// confirmDeleteManagerGoal implements the Delete/Backspace hotkey: deletion
// asks for confirmation before the goal is removed (previously it was
// removed immediately). Yes removes and reopens the manager; No (or Escape)
// returns to the manager with the cursor back on the goal. The active goal
// cannot be removed here — it goes through /goal:cancel.
func (c *GoalCommand) confirmDeleteManagerGoal(ctx core.Context, id string) {
	if id == "__active__" {
		ctx.Flash("The active goal is running — cancel it with /goal:cancel (or /goal:cancel:all).")
		_ = c.showQueueManagerAt(ctx, "__active__")
		return
	}
	label := c.managerGoalLabel(ctx, id)
	if label == "" {
		_ = c.showQueueManagerAt(ctx, "")
		return
	}
	opts := []tui.SelectorItem{
		{Value: "yes", Label: "Yes, delete it"},
		{Value: "no", Label: "No, keep it"},
	}
	ctx.SelectOption("Delete goal "+label+"?", opts, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			_ = c.showQueueManagerAt(ctx, id)
			return
		}
		if _, _, err := c.Queue.Remove(id); err != nil {
			ctx.Flash(err.Error())
		} else {
			ctx.Flash("Goal deleted.")
		}
		_ = c.showQueueManagerAt(ctx, "")
	})
}

// managerGoalLabel returns the display label of a queued goal for the
// delete-confirmation title, or "" (after flashing) when it is not queued.
func (c *GoalCommand) managerGoalLabel(ctx core.Context, id string) string {
	goals, err := c.Queue.Read()
	if err != nil {
		ctx.Flash(err.Error())
		return ""
	}
	for _, g := range goals {
		if g.ID == id {
			return goalRowLabel(g.Name, g.Objective)
		}
	}
	ctx.Flash(fmt.Sprintf("queued goal %q not found", id))
	return ""
}

// promptCreateForPlacement opens the create-goal flow for the manager's add
// rows: with an active goal, placementNext prepends to the queue (the goal
// runs next) and placementLast appends — neither silently replaces the
// running goal (replacement goes through /goal:replace). With no active
// goal, the new goal starts immediately regardless of placement.
func (c *GoalCommand) promptCreateForPlacement(ctx core.Context, placement goalPlacement) {
	if c.Mode.GetGoal().Goal == nil {
		_ = c.promptCreateInteractive(ctx, placementAsk)
		return
	}
	_ = c.promptCreateInteractive(ctx, placement)
}

// promptEditQueuedGoal implements the 'e' hotkey of /goal:manage: it opens an
// input prompt pre-filled with the queued goal's current objective and
// persists the edit via Queue.Update. The manager reopens afterwards —
// including on cancel or an empty submission — so it stays open until the
// user picks Done.
func (c *GoalCommand) promptEditQueuedGoal(ctx core.Context, id string) {
	goals, err := c.Queue.Read()
	if err != nil {
		ctx.Flash(err.Error())
		return
	}
	idx := -1
	for i := range goals {
		if goals[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		ctx.Flash(fmt.Sprintf("queued goal %q not found", id))
		_ = c.showQueueManagerAt(ctx, "")
		return
	}
	current := goals[idx].Objective
	ctx.ShowInput("Edit goal description:", current, func(value string, ok bool) {
		c.applyEditedObjective(ctx, id, current, value, ok)
		_ = c.showQueueManagerAt(ctx, id)
	})
}

// applyEditedObjective persists a submitted edit: a non-empty, changed
// objective is written via Queue.Update; cancel, blank, or unchanged input
// is a no-op.
func (c *GoalCommand) applyEditedObjective(ctx core.Context, id, current, value string, ok bool) {
	if !ok {
		return
	}
	v := strings.TrimSpace(value)
	if v == "" || v == current {
		return
	}
	if _, err := c.Queue.Update(id, v); err != nil {
		ctx.Flash(err.Error())
		return
	}
	ctx.Flash("Goal updated.")
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
	// placementNext inserts at the FRONT of the queue (/goal:next[:first]):
	// the goal is promoted right after the active goal completes. Unlike
	// placementFirst it never touches the active goal.
	placementNext
	// placementLast appends to the END of the queue (/goal:next:last).
	placementLast
)

// create handles /goal:new:<text> and bare /goal:<text>.
// When a goal is already active, it asks whether to become first (replace) or
// last (queue) — the item-4 prompt. fresh is the resolved context mode
// (/goal:new:fresh|reuse or the configured default).
func (c *GoalCommand) create(ctx core.Context, objective string, fresh bool) error {
	if c.Mode.GetGoal().Goal != nil {
		return c.promptFirstOrLast(ctx, objective, fresh)
	}
	return c.startGoal(ctx, objective, false, fresh)
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
func (c *GoalCommand) promptFirstOrLast(ctx core.Context, objective string, fresh bool) error {
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
			_ = c.startGoal(ctx, objective, true, fresh)
		case "last":
			_ = c.queueLast(ctx, objective, fresh)
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
		_ = c.startGoal(ctx, objective, true, c.resolveFresh(""))
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
// according to placement (front of queue, end of queue, replace, or — for
// placementAsk — the first/last prompt follows).
func (c *GoalCommand) promptCreateInteractive(ctx core.Context, placement goalPlacement) error {
	promptText := "Set new goal objective (ctrl-c to cancel)"
	switch placement {
	case placementNext:
		promptText = "Queue a goal to run next — right after the active goal (ctrl-c to cancel)"
	case placementLast:
		promptText = "Queue a goal at the end of the queue (ctrl-c to cancel)"
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
		case placementNext:
			_ = c.queueNext(ctx, objective, c.resolveFresh(""))
		case placementLast:
			_ = c.queueLast(ctx, objective, c.resolveFresh(""))
		case placementFirst:
			_ = c.startGoal(ctx, objective, true, c.resolveFresh(""))
		default: // placementAsk
			_ = c.create(ctx, objective, c.resolveFresh(""))
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

func (c *GoalCommand) startGoal(ctx core.Context, objective string, replace bool, fresh bool) error {
	if c.AutonomySwitcher != nil {
		level := c.AutonomySwitcher.Current()
		if level != internal.AutonomyYolo {
			c.promptStartPermission(ctx, objective, replace, level, fresh)
			return nil
		}
	}
	return c.doStartGoal(ctx, objective, replace, fresh)
}

func (c *GoalCommand) promptStartPermission(ctx core.Context, objective string, replace bool, current internal.AutonomyLevel, fresh bool) {
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
		_ = c.doStartGoal(ctx, objective, replace, fresh)
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

func (c *GoalCommand) doStartGoal(ctx core.Context, objective string, replace bool, fresh bool) error {
	snap, err := c.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:    objective,
		Replace:      replace,
		FreshContext: fresh,
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
	{"list", "list active + queued goals with full objectives"},
	{"log", "show recent goal event records"},
	{"verify", "run the recorded verify command now"},
	{"settings", "toggle goal settings (auto-unblock)"},
	{"pause", "pause the active goal"},
	{"resume", "resume a paused goal"},
	{"cancel", "discard the current goal (next queued promotes paused)"},
}

// goalCancelScopes is the nested /goal:cancel:<scope> list offered once the
// user typed "cancel:" (level-2 completion).
var goalCancelScopes = []struct {
	value string
	desc  string
}{
	{"current", "discard the current goal (queued goals stay)"},
	{"all", "discard the current goal and clear the queue"},
}

// goalNextOptions is the nested /goal:next:<option> list offered once the
// user typed "next:" (level-2 completion): queue placement and context mode.
var goalNextOptions = []struct {
	value string
	desc  string
}{
	{"first", "queue at the front — runs right after the active goal (default)"},
	{"last", "queue at the end — runs after all queued goals"},
	{"fresh", "queue on a clean context"},
	{"reuse", "queue reusing the current conversation"},
}

// CompleteArgs implements core.ArgCompleter, providing /goal:<tab> completion
// for subcommand keywords. The router passes the raw text after "goal" as
// prefix (e.g. "ne" for /goal:ne); a prefix containing ":" (e.g. "cancel:a"
// for /goal:cancel:a) is completed at the nested scope level.
func (c *GoalCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if sub, rest, nested := splitGoalCompletionPrefix(prefix); nested {
		switch sub {
		case "cancel":
			return cancelScopeCompletions(rest)
		case "next":
			return nextOptionCompletions(rest)
		}
	}
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

// splitGoalCompletionPrefix splits a nested completion prefix: "cancel:a" →
// ("cancel", "a", true); "can" → ("can", "", false).
func splitGoalCompletionPrefix(prefix string) (sub, rest string, nested bool) {
	idx := strings.Index(prefix, ":")
	if idx < 0 {
		return prefix, "", false
	}
	return prefix[:idx], prefix[idx+1:], true
}

// cancelScopeCompletions returns the /goal:cancel:<scope> completions whose
// scope starts with rest, with values fully spelled out ("cancel:current")
// so the completer prefixes them into /goal:cancel:current.
func cancelScopeCompletions(rest string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, sc := range goalCancelScopes {
		if rest == "" || strings.HasPrefix(sc.value, rest) {
			comps = append(comps, core.ArgCompletion{
				Value:       "cancel:" + sc.value,
				Description: sc.desc,
			})
		}
	}
	return comps
}

// nextOptionCompletions returns the /goal:next:<option> completions whose
// option starts with rest, with values fully spelled out ("next:first") so
// the completer prefixes them into /goal:next:first.
func nextOptionCompletions(rest string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, opt := range goalNextOptions {
		if rest == "" || strings.HasPrefix(opt.value, rest) {
			comps = append(comps, core.ArgCompletion{
				Value:       "next:" + opt.value,
				Description: opt.desc,
			})
		}
	}
	return comps
}
