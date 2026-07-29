// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/goal"
)

// TodoCommand handles /todo slash commands: CRUD over the ACTIVE goal's todo
// list (bugs.md Issue 5). Todos are goal-scoped — without an active goal
// every subcommand reports a clear error.
//
// The router splits only on ':', so:
//
//	/todo                  → args=[]
//	/todo:list             → args=["list"]
//	/todo:add:write tests  → args=["add", "write tests"]
//	/todo:edit:1           → args=["edit", "1"]            (prompts, prefilled)
//	/todo:edit:1:new title → args=["edit", "1", "new title"]
//	/todo:done:2           → args=["done", "2"]
//
// Positions are 1-based indexes into the active goal's todo list, as printed
// by /todo:list.
type TodoCommand struct {
	Mode *goal.GoalMode
}

// Name returns the command name.
func (c *TodoCommand) Name() string { return "todo" }

// Aliases returns command aliases.
func (c *TodoCommand) Aliases() []string { return nil }

// ShortHelp returns a short help string.
func (c *TodoCommand) ShortHelp() string { return "Manage the active goal's todos" }

// LongHelp returns detailed help.
func (c *TodoCommand) LongHelp() string { return help.LongHelp(c.Name()) }

// parsedTodoArgs is the parse result for /todo arguments.
type parsedTodoArgs struct {
	kind     string // list | add | add-interactive | edit | edit-interactive | done | undone | delete | error
	position int    // 1-based todo position for positional subcommands
	text     string // title text (add/edit)
	message  string // error detail when kind == "error"
}

// positionalSubs maps subcommands that take a 1-based todo position to their
// parse kind.
var positionalSubs = map[string]string{
	"edit":   "edit",
	"done":   "done",
	"undone": "undone",
	"delete": "delete",
	"rm":     "delete",
}

func (c *TodoCommand) parseArgs(args []string) parsedTodoArgs {
	if len(args) == 0 {
		return parsedTodoArgs{kind: "list"}
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "list":
		return parsedTodoArgs{kind: "list"}
	case "add":
		text := strings.TrimSpace(strings.Join(args[1:], " "))
		if text == "" {
			return parsedTodoArgs{kind: "add-interactive"}
		}
		return parsedTodoArgs{kind: "add", text: text}
	}
	kind, positional := positionalSubs[sub]
	if !positional {
		return parsedTodoArgs{kind: "error", message: fmt.Sprintf(
			"unknown /todo subcommand %q: use list, add, edit, done, undone, or delete", sub)}
	}
	if len(args) < 2 {
		return parsedTodoArgs{kind: "error", message: fmt.Sprintf(
			"usage: /todo:%s:<n> — %s needs a todo number from /todo:list", sub, sub)}
	}
	pos, err := strconv.Atoi(args[1])
	if err != nil {
		return parsedTodoArgs{kind: "error", message: fmt.Sprintf(
			"/todo:%s: %q is not a todo number — see /todo:list", sub, args[1])}
	}
	if kind == "edit" {
		text := strings.TrimSpace(strings.Join(args[2:], " "))
		if text == "" {
			return parsedTodoArgs{kind: "edit-interactive", position: pos}
		}
		return parsedTodoArgs{kind: "edit", position: pos, text: text}
	}
	return parsedTodoArgs{kind: kind, position: pos}
}

// Run executes the /todo command.
func (c *TodoCommand) Run(ctx core.Context, args []string) error {
	parsed := c.parseArgs(args)
	if parsed.kind == "error" {
		return fmt.Errorf("%s", parsed.message)
	}
	if c.activeGoal() == nil {
		return fmt.Errorf("no active goal — todos live on goals; start one with /goal:new:<objective>")
	}
	switch parsed.kind {
	case "list":
		c.showList(ctx)
	case "add":
		return c.add(ctx, parsed.text)
	case "add-interactive":
		return c.promptAdd(ctx)
	case "edit":
		return c.edit(ctx, parsed.position, parsed.text)
	case "edit-interactive":
		return c.promptEdit(ctx, parsed.position)
	case "done":
		return c.setStatus(ctx, parsed.position, goal.TodoDone)
	case "undone":
		return c.setStatus(ctx, parsed.position, goal.TodoPending)
	case "delete":
		return c.delete(ctx, parsed.position)
	}
	return nil
}

// activeGoal returns the current goal snapshot (nil when no goal exists).
func (c *TodoCommand) activeGoal() *goal.GoalSnapshot {
	if c.Mode == nil {
		return nil
	}
	return c.Mode.GetGoal().Goal
}

// todoAt resolves a 1-based position to the todo's ID, or an out-of-range
// error naming the valid range.
func (c *TodoCommand) todoAt(pos int) (string, error) {
	g := c.activeGoal()
	if pos < 1 || pos > len(g.Todos) {
		return "", fmt.Errorf("todo %d out of range — the active goal has %d todo(s) (see /todo:list)", pos, len(g.Todos))
	}
	return g.Todos[pos-1].ID, nil
}

// showList implements /todo:list: numbered todos with status markers.
func (c *TodoCommand) showList(ctx core.Context) {
	g := c.activeGoal()
	if len(g.Todos) == 0 {
		writeStr(ctx, "No todos on the active goal.\n")
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Todos on **%s** (%s):\n\n", goalNameOrFallback(g), g.Objective)
	for i, td := range g.Todos {
		fmt.Fprintf(&sb, "%d. %s %s\n", i+1, todoStatusMark(td.Status), td.Title)
	}
	writeStr(ctx, sb.String())
}

// goalNameOrFallback renders the goal's friendly name (or a placeholder).
func goalNameOrFallback(g *goal.GoalSnapshot) string {
	if g.Name != "" {
		return g.Name
	}
	return "active goal"
}

func (c *TodoCommand) add(ctx core.Context, title string) error {
	item, err := c.Mode.AddGoalTodo(title, goal.GoalActorUser)
	if err != nil {
		return err
	}
	writeFmt(ctx, "Added todo %s: %s\n", item.ID, item.Title)
	return nil
}

// promptAdd asks for the new todo's title on the main input line.
func (c *TodoCommand) promptAdd(ctx core.Context) error {
	if ctx.RequestMainInput == nil {
		return fmt.Errorf("main input not available")
	}
	ctx.RequestMainInput("New todo title (ctrl-c to cancel)", func(value string) {
		if title := strings.TrimSpace(value); title != "" {
			_ = c.add(ctx, title)
		}
	})
	return nil
}

func (c *TodoCommand) edit(ctx core.Context, pos int, title string) error {
	id, err := c.todoAt(pos)
	if err != nil {
		return err
	}
	if _, err := c.Mode.RenameGoalTodo(id, title, goal.GoalActorUser); err != nil {
		return err
	}
	writeFmt(ctx, "Todo %d renamed: %s\n", pos, title)
	return nil
}

// promptEdit opens the main input line PREFILLED with the todo's current
// title; the prompt names the todo being edited (bugs.md Issue 5).
func (c *TodoCommand) promptEdit(ctx core.Context, pos int) error {
	if ctx.ShowInputFunc == nil {
		return fmt.Errorf("main input not available")
	}
	g := c.activeGoal()
	if _, err := c.todoAt(pos); err != nil {
		return err
	}
	current := g.Todos[pos-1]
	ctx.ShowInputFunc(fmt.Sprintf("Edit todo %d: %s (ctrl-c to cancel)", pos, current.Title), current.Title,
		func(value string, ok bool) {
			if title := strings.TrimSpace(value); ok && title != "" {
				_ = c.edit(ctx, pos, title)
			}
		})
	return nil
}

func (c *TodoCommand) setStatus(ctx core.Context, pos int, status string) error {
	id, err := c.todoAt(pos)
	if err != nil {
		return err
	}
	if _, err := c.Mode.UpdateGoalTodo(id, status, goal.GoalActorUser); err != nil {
		return err
	}
	writeFmt(ctx, "Todo %d marked %s.\n", pos, strings.ReplaceAll(status, "_", " "))
	return nil
}

func (c *TodoCommand) delete(ctx core.Context, pos int) error {
	id, err := c.todoAt(pos)
	if err != nil {
		return err
	}
	if _, err := c.Mode.RemoveGoalTodo(id, goal.GoalActorUser); err != nil {
		return err
	}
	writeFmt(ctx, "Todo %d deleted.\n", pos)
	return nil
}
