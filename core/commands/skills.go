// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/skills"
)

// SkillsCommand lists, runs, and shows skills.
type SkillsCommand struct{}

func (c *SkillsCommand) Name() string      { return "skill" }
func (c *SkillsCommand) Aliases() []string { return []string{} }
func (c *SkillsCommand) ShortHelp() string { return "Manage and execute skills" }
func (c *SkillsCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *SkillsCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	parts := strings.Split(prefix, ":")

	// Level 1: propose subcommands (run, show)
	if len(parts) <= 1 {
		return skillSubcommandCompletions(parts[0])
	}

	// Level 2+: propose skill names for run:/show:
	if parts[0] == "run" || parts[0] == "show" {
		return skillNameCompletions(parts[0], strings.Join(parts[1:], ":"), ctx.SkillRegistry)
	}
	return nil
}

// skillSubcommandCompletions proposes the run/show subcommands for completion.
func skillSubcommandCompletions(prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	subcmds := []struct{ val, desc string }{{"run", "execute a skill"}, {"show", "show skill details"}}
	for _, v := range subcmds {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

// skillNameCompletions proposes skill names matching the search prefix.
func skillNameCompletions(subcmd, searchPrefix string, reg core.SkillRegistry) []core.ArgCompletion {
	if reg == nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, s := range reg.List() {
		if searchPrefix != "" && !strings.HasPrefix(s.Name, searchPrefix) {
			continue
		}
		val := subcmd + ":" + s.Name
		desc := s.Description
		if skill, ok := reg.Get(s.Name); ok {
			if params := skillInputSchemaDesc(skill.Meta.InputSchema); params != "" {
				desc += " | params: " + params
			}
		}
		comps = append(comps, core.ArgCompletion{Value: val, Description: desc})
	}
	return comps
}

func (c *SkillsCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return listSkills(ctx, ctx.SkillRegistry)
	}
	switch args[0] {
	case "run":
		return runSkill(ctx, ctx.SkillRegistry, ctx.SubmitToAgent, args[1:])
	case "show":
		return showSkill(ctx, ctx.SkillRegistry, args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand: %s (use 'run' or 'show')", args[0])
	}
}

// listSkills displays all available skills.
// Depends on OutputWriter + SkillRegistry.
func listSkills(w core.OutputWriter, reg core.SkillRegistry) error {
	if reg != nil {
		skills := reg.List()
		writeStr(w, "# Skills\n\n")
		for _, s := range skills {
				cat := s.Category
				if cat == "" {
					cat = "action"
				}
				icon := typeIcon(cat)
				typ := ""
				if s.Inline {
					typ = " (inline)"
				}
				writeFmt(w, "- %s **%s** — %s%s\n", icon, s.Name, s.Description, typ)
		}
		writeFmt(w, "\n%d skill(s). Use `/skill:run:<name>` to execute.\n", len(skills))
		return nil
	}
	writeStr(w, "# Skills\n\n")
	writeStr(w, "- **refactor** — Refactor code for clarity and correctness\n")
	writeStr(w, "- **test-gen** — Generate unit tests\n")
	writeStr(w, "- **document** — Add GoDoc comments\n")
	writeStr(w, "- **review** — Code review analysis\n")
	writeStr(w, "- **explain** — Explain code in detail\n")
	writeStr(w, "- **commit-msg** — Generate commit messages\n")
	writeStr(w, "- **debug** — Debug analysis\n")
	return nil
}

// SkillRunCommand executes a skill.
type SkillRunCommand struct{} // unused — kept for API compatibility; use runSkill directly

// runSkill executes a named skill.
// Depends on Context + SkillRegistry + an optional submitFunc
// for forwarding inline skill content to the agent.
func runSkill(
	ctx core.Context,
	reg core.SkillRegistry,
	submitFunc func(string),
	args []string,
) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /skill:run <name> [args...]")
	}
	if reg == nil {
		writeStr(ctx, "Skill registry not available.\n")
		return nil
	}
	name := args[0]
	skill, ok := reg.Get(name)
	if !ok {
		writeFmt(ctx, "Skill not found: %s. Use /skills to list available skills.\n", name)
		return nil
	}
	task := strings.Join(args[1:], " ")

	// Determine effective execution mode: skill frontmatter inline flag wins;
	// otherwise fall back to the global config's skills.execution_mode.
	isInline := effectiveInline(skill, ctx.Config)

	if isInline {
		// If the conversation hasn't started yet, load the skill into the
		// active mode's skill stack so it becomes part of the system prompt
		// instead of triggering an agent response.
		if !conversationStarted(ctx) {
			current := ctx.CurrentMode()
			ctx.SetMode(current.AddSkill(name))
			writeFmt(ctx, "Skill '%s' loaded into system prompt.\n", name)
			return nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("[Skill: %s]\n%s\n", skill.Meta.Name, skill.Body))
		if task != "" {
			sb.WriteString(fmt.Sprintf("\nTask: %s\n", task))
		} else {
			sb.WriteString("\nApply the skill instructions to the current context.\n")
		}
		if submitFunc != nil {
			submitFunc(sb.String())
		} else {
			writeStr(ctx, sb.String())
		}
		return nil
	}

	// Sub-agent execution: spawn a sub-agent via AgentPool.
	writeFmt(ctx, "⟡ Running skill: %s\n", skill.Meta.Name)
	writeFmt(ctx, "  Description: %s\n", skill.Meta.Description)
	if task != "" {
		writeFmt(ctx, "  Task: %s\n", task)
	}
	writeStr(ctx, "\n")

	if ctx.AgentPool == nil {
		writeStr(ctx, "Sub-agent execution requires AgentPool (not configured).\n")
		return nil
	}

	// Create a sub-agent with the skill body as its system prompt.
	roleName := "skill-" + skill.Meta.Name
	ctx.AgentPool.SetConfig(roleName, multiagent.AgentConfig{
		SystemPrompt: skill.Body,
	})

	agent, err := ctx.AgentPool.GetOrCreate(roleName)
	if err != nil {
		writeFmt(ctx, "Error creating sub-agent: %v\n", err)
		return nil
	}

	result, err := agent.RunAndCollect(context.Background(), "Task: "+task)
	if err != nil {
		writeFmt(ctx, "Skill sub-agent failed: %v\n", err)
		return nil
	}

	writeStr(ctx, "── Skill Result ──────────────────────────────\n")
	writeStr(ctx, result)
	writeStr(ctx, "\n──────────────────────────────────────────────\n")
	return nil
}

// effectiveInline returns true when the skill should be executed inline.
// The skill's own frontmatter inline flag takes precedence; if unset (false),
// the global config skills.execution_mode is used as a fallback.
func effectiveInline(skill *skills.Skill, cfg *config.Config) bool {
	if skill.Meta.Inline {
		return true
	}
	if cfg != nil && cfg.Skills.ExecutionMode == config.AgenticSkillModeInline {
		return true
	}
	return false
}

// typeIcon returns a single-character icon for the given skill category.
// 🧠 for knowledge, ⚡ for action.
func typeIcon(category string) string {
	switch category {
	case skills.SkillCategoryKnowledge:
		return "🧠"
	default:
		return "⚡"
	}
}

// showSkill displays detailed information about a skill.
// Depends on OutputWriter + SkillRegistry.
func showSkill(w core.OutputWriter, reg core.SkillRegistry, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /skill:show <name>")
	}
	if reg != nil {
		if s, ok := reg.Get(args[0]); ok {
			writeFmt(w, "Skill: %s\n", s.Meta.Name)
			writeFmt(w, "Description: %s\n", s.Meta.Description)
			writeFmt(w, "Type: %s\n", map[bool]string{true: "inline", false: "sub-agent"}[s.Meta.Inline])
			writeFmt(w, "Mode: %s\n", s.Meta.Mode)
			writeStr(w, "\n"+s.Body+"\n")
			return nil
		}
		writeFmt(w, "Skill not found: %s\n", args[0])
		return nil
	}
	writeFmt(w, "Skill '%s' details require SkillRegistry\n", args[0])
	return nil
}

// conversationStarted reports whether the user has already submitted at least
// one message in the current session. It is used by inline-skill loading to
// decide whether to add the skill to the system prompt (pre-conversation) or
// submit it as a user message (mid-conversation).
func conversationStarted(ctx core.Context) bool {
	if ctx.AgentManager == nil {
		return false
	}
	return ctx.AgentManager.LastUserInput() != ""
}

// SkillShortcutCommand is a command dynamically registered for a skill that
// defines a dedicated /<shortcut> in its frontmatter.
type SkillShortcutCommand struct {
	Skill    *skills.Skill
	LongDesc string
}

func (c *SkillShortcutCommand) Name() string      { return c.Skill.Meta.Command }
func (c *SkillShortcutCommand) Aliases() []string { return nil }
func (c *SkillShortcutCommand) ShortHelp() string { return c.Skill.Meta.Description }
func (c *SkillShortcutCommand) LongHelp() string {
	if c.LongDesc != "" {
		return c.LongDesc
	}
	return c.Skill.Meta.Description
}
func (c *SkillShortcutCommand) Run(ctx core.Context, args []string) error {
	// Delegate to /skill:run:name — forward arguments so parameters work
	return runSkill(ctx, ctx.SkillRegistry, ctx.SubmitToAgent, append([]string{c.Skill.Meta.Name}, args...))
}

// RegisterSkillShortcuts registers dedicated /<command> shortcuts for skills
// that define a "command:" field in their frontmatter.
func RegisterSkillShortcuts(registry *core.CommandRegistry, skillReg *skills.SkillRegistry) []string {
	var warnings []string
	for _, s := range skillReg.List() {
		skill, ok := skillReg.Get(s.Name)
		if !ok || skill.Meta.Command == "" {
			continue
		}
		cmd := &SkillShortcutCommand{Skill: skill}
		if !registry.RegisterSafe(cmd) {
			warnings = append(warnings, fmt.Sprintf(
				"skill '%s': command /%s already exists, keeping /skill:run:%s",
				skill.Meta.Name, skill.Meta.Command, skill.Meta.Name))
		}
	}
	return warnings
}

// skillInputSchemaDesc formats the input-schema as a human-readable
// parameter list for command completions.
func skillInputSchemaDesc(schema map[string]any) string {
	if len(schema) == 0 {
		return ""
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return ""
	}
	var parts []string
	for name, prop := range props {
		p, _ := prop.(map[string]any)
		if p == nil {
			continue
		}
		desc, _ := p["description"].(string)
		typ, _ := p["type"].(string)
		if desc != "" {
			if typ != "" {
				parts = append(parts, fmt.Sprintf("%s (%s): %s", name, typ, desc))
			} else {
				parts = append(parts, fmt.Sprintf("%s: %s", name, desc))
			}
		} else if typ != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", name, typ))
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

// UpdateCompletionsWithParams injects input-schema parameter descriptions
// into the completion entries for skill commands.
func UpdateCompletionsWithParams(comps []core.ArgCompletion, skillReg core.SkillRegistry) []core.ArgCompletion {
	for i, c := range comps {
		// Only process skill:run: completions
		if !strings.HasPrefix(c.Value, "run:") && !strings.HasPrefix(c.Value, "show:") {
			continue
		}
		name := strings.TrimPrefix(c.Value, "run:")
		name = strings.TrimPrefix(name, "show:")
		if skill, ok := skillReg.Get(name); ok {
			if params := skillInputSchemaDesc(skill.Meta.InputSchema); params != "" {
				comps[i].Description = c.Description + " | params: " + params
			}
		}
	}
	return comps
}
