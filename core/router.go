// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"fmt"
	"strings"
)

// DocSuffix indicates whether the user is requesting documentation.
type DocSuffix int

const (
	DocSuffixNone  DocSuffix = 0 // execute the command
	DocSuffixShort DocSuffix = 1 // /command?  → short help
	DocSuffixLong  DocSuffix = 2 // /command?? → long help
)

// RouteResult holds the result of routing a user input.
type RouteResult struct {
	Command  Command
	Args     []string
	DocLevel DocSuffix
	IsHelp   bool   // true if the user asked for help on a command
	CmdName  string // original command name (for not-found messages)
}

// CommandRouter parses user input and routes it to the appropriate
// command or documentation engine.
type CommandRouter struct {
	registry *CommandRegistry
	docEng   *DocEngine
	aliases  map[string]string // user-defined command aliases
}

// NewCommandRouter creates a router with the given registry and doc engine.
func NewCommandRouter(registry *CommandRegistry, docEng *DocEngine) *CommandRouter {
	return &CommandRouter{
		registry: registry,
		docEng:   docEng,
	}
}

// SetAliases configures user-defined command aliases for the router.
// The map keys are alias names, values are the target command invocation
// (e.g. "n" → "session:new"). Aliases are resolved in Parse before looking
// up the command in the registry.
func (r *CommandRouter) SetAliases(aliases map[string]string) {
	r.aliases = aliases
}

// Parse parses a user input string and returns the route result.
// Input should start with '/'. Uses colon as the argument separator:
//
//	/mode:coder         → command=mode, args=["coder"]
//	/skill:run:refactor → command=skill, args=["run", "refactor"]
//	/help               → command=help
//	/help?              → short help suffix
//	/help??             → long help suffix
//
// The old space-separated syntax is NOT supported.
func (r *CommandRouter) Parse(input string) *RouteResult {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return nil
	}

	// Strip the leading slash
	rest := input[1:]

	// Check for doc suffixes (must remove before colon split,
	// since args like "cmd:test" end in actual content, not ?/??)
	docLevel := DocSuffixNone
	if strings.HasSuffix(rest, "??") {
		docLevel = DocSuffixLong
		rest = strings.TrimSuffix(rest, "??")
	} else if strings.HasSuffix(rest, "?") {
		docLevel = DocSuffixShort
		rest = strings.TrimSuffix(rest, "?")
	}

	// Split by colon only. First element is the command name.
	parts := strings.Split(rest, ":")
	if len(parts) == 0 || parts[0] == "" {
		return nil
	}

	cmdName := parts[0]
	args := parts[1:]

	// Resolve user-defined aliases before registry lookup.
	if r.aliases != nil {
		if target, ok := r.aliases[cmdName]; ok {
			// The alias target may itself contain colons (e.g. "session:new").
			// Re-parse the target and prepend any remaining original args.
			aliasParts := strings.Split(target, ":")
			cmdName = aliasParts[0]
			args = append(aliasParts[1:], args...)
		}
	}

	cmd, found := r.registry.Resolve(cmdName)
	if !found {
		return &RouteResult{
			IsHelp:   false,
			DocLevel: docLevel,
			Args:     args,
			CmdName:  cmdName,
		}
	}

	return &RouteResult{
		Command:  cmd,
		Args:     args,
		DocLevel: docLevel,
		IsHelp:   docLevel != DocSuffixNone,
		CmdName:  cmdName,
	}
}

// resolveShortStatus returns the output for the "/<cmd>?" suffix. Commands
// implementing StatusProvider get to print their live state; everything else
// falls back to the static ShortHelp text.
func resolveShortStatus(cmd Command, ctx Context) string {
	if sp, ok := cmd.(StatusProvider); ok {
		if s := sp.Status(ctx); s != "" {
			return s
		}
	}
	return cmd.ShortHelp()
}

// Execute runs the command with the given context. Returns the output string.
func (r *CommandRouter) Execute(ctx Context, result *RouteResult) (string, error) {
	if result == nil {
		return "", nil
	}

	// Not found handling
	if result.Command == nil {
		name := result.CmdName
		if name == "" && len(result.Args) > 0 {
			name = result.Args[0]
		}
		if name == "" {
			name = "unknown"
		}
		msg := fmt.Sprintf("Unknown command: /%s. Type /help for available commands.", name)
		return msg, nil
	}

	// Doc suffix handling
	switch result.DocLevel {
	case DocSuffixShort:
		return resolveShortStatus(result.Command, ctx), nil
	case DocSuffixLong:
		return result.Command.LongHelp(), nil
	}

	// Set up OutputBuffer so commands can write their response
	buf := new(strings.Builder)
	ctx.OutputBuffer = buf

	// Execute the command
	err := result.Command.Run(ctx, result.Args)
	if err != nil {
		buf.WriteString(fmt.Sprintf("Error: %v", err))
		return buf.String(), err
	}
	if buf.Len() > 0 {
		return buf.String(), nil
	}
	// Descriptive fallback when command produces no output
	cmdName := result.Command.Name()
	args := result.Args
	feedback := fmt.Sprintf("✓ /%s", cmdName)
	if len(args) > 0 {
		feedback += ":" + strings.Join(args, ",")
	}
	feedback += " completed successfully"
	return feedback, nil
}
