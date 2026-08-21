// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
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
	"pause-next": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error { return c.setPauseNext(ctx, true) },
	"pause-next-off": func(c *GoalCommand, ctx core.Context, _ parsedGoalArgs) error {
		return c.setPauseNext(ctx, false)
	},
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
		return c.promptCreateInteractive(ctx, p.placement, c.resolveFresh(p.contextMode))
	},
	"reorder": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		return c.reorderQueue(ctx, p.objective)
	},
	"create": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		return c.create(ctx, p.objective, c.resolveFresh(p.contextMode))
	},
	"create-interactive": func(c *GoalCommand, ctx core.Context, p parsedGoalArgs) error {
		return c.promptCreateInteractive(ctx, placementAsk, c.resolveFresh(p.contextMode))
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
	"pause": {mode: subScope, errorHint: "usage: /goal:pause[:current|next|next:off]",
		scopeKinds: map[string]string{
			"": "pause", "current": "pause",
			"next": "pause-next", "next off": "pause-next-off",
		}},
	"resume": {mode: subNone, kind: "resume"},
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
// (first|last, default first), reuse+placement shorthand (rfirst|rlast), and
// context-mode (fresh|reuse) tokens in any order, followed by the objective
// text. Examples:
//
//	"fix tests"        → (placementNext, "", "fix tests")
//	"last fresh audit" → (placementLast, "fresh", "audit")
//	"rlast audit"      → (placementLast, "reuse", "audit")
//	"last"             → (placementLast, "", "") → interactive
func splitGoalNextArgs(text string) (placement goalPlacement, mode, rest string) {
	placement = placementNext
	rest = text
	for {
		tok, tail, ok := splitLeadingToken(rest, "first", "last", "fresh", "reuse", "rfirst", "rlast")
		if !ok {
			return placement, mode, rest
		}
		switch tok {
		case "last":
			placement = placementLast
		case "fresh", "reuse":
			mode = tok
		case "rfirst":
			placement = placementNext
			mode = "reuse"
		case "rlast":
			placement = placementLast
			mode = "reuse"
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
