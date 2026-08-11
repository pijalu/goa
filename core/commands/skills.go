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
	"github.com/pijalu/goa/internal/agentic"
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

	// Level 1: propose subcommands (run, show, enable, disable)
	if len(parts) <= 1 {
		return skillSubcommandCompletions(parts[0])
	}

	// Level 2+: propose skill names for run:/show:/enable:/disable:
	switch parts[0] {
	case "run", "show":
		return skillNameCompletions(parts[0], strings.Join(parts[1:], ":"), ctx.SkillRegistry)
	case "enable":
		return skillEnableCompletions(parts[0], strings.Join(parts[1:], ":"), ctx)
	case "disable":
		return skillDisableCompletions(parts[0], strings.Join(parts[1:], ":"), ctx)
	}
	return nil
}

// skillSubcommandCompletions proposes the run/show/enable/disable subcommands
// for completion.
func skillSubcommandCompletions(prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	subcmds := []struct{ val, desc string }{
		{"run", "execute a skill"},
		{"show", "show skill details"},
		{"enable", "enable a disabled skill"},
		{"disable", "disable an enabled skill"},
	}
	for _, v := range subcmds {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

// skillEnableCompletions proposes disabled skills for /skill:enable. Disabled
// skills are not loaded in the registry, so candidates come from the config's
// disabled list.
func skillEnableCompletions(subcmd, searchPrefix string, ctx core.Context) []core.ArgCompletion {
	var names []string
	if ctx.Config != nil {
		names = append(names, ctx.Config.Skills.Disabled...)
	}
	return skillNameCompletionsFromNames(subcmd, searchPrefix, names, ctx.SkillRegistry)
}

// skillDisableCompletions proposes enabled skills for /skill:disable.
func skillDisableCompletions(subcmd, searchPrefix string, ctx core.Context) []core.ArgCompletion {
	return skillNameCompletionsFiltered(subcmd, searchPrefix, ctx.SkillRegistry, func(s skills.SkillSummary) bool {
		return ctx.Config == nil || skillEnabled(ctx.Config, s.Name, ctx.SkillRegistry)
	})
}

// skillNameCompletionsFromNames builds completions for the given candidate
// names (used when the candidates are not loaded in the registry, e.g.
// disabled skills).
func skillNameCompletionsFromNames(subcmd, searchPrefix string, names []string, reg core.SkillRegistry) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, name := range names {
		if searchPrefix != "" && !strings.HasPrefix(name, searchPrefix) {
			continue
		}
		desc := ""
		if reg != nil {
			if skill, ok := reg.Get(name); ok {
				desc = skill.Meta.Description
			}
		}
		comps = append(comps, core.ArgCompletion{Value: subcmd + ":" + name, Description: desc})
	}
	return comps
}

// skillNameCompletions proposes skill names matching the search prefix.
func skillNameCompletions(subcmd, searchPrefix string, reg core.SkillRegistry) []core.ArgCompletion {
	return skillNameCompletionsFiltered(subcmd, searchPrefix, reg, nil)
}

// skillNameCompletionsFiltered proposes skill names matching the search prefix
// and the include predicate (nil includes all).
func skillNameCompletionsFiltered(subcmd, searchPrefix string, reg core.SkillRegistry, include func(skills.SkillSummary) bool) []core.ArgCompletion {
	if reg == nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, s := range reg.List() {
		if include != nil && !include(s) {
			continue
		}
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
	case "enable":
		return enableSkill(ctx, args[1:])
	case "disable":
		return disableSkill(ctx, args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand: %s (use 'run', 'show', 'enable', or 'disable')", args[0])
	}
}

// enableSkill enables a skill and persists the change: embedded skills are
// global (home config), other loaded skills are per-project (project config).
func enableSkill(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /skill:enable <name>")
	}
	name := args[0]
	// A disabled skill is not loaded in the registry; verify it exists
	// somewhere so a typo does not silently write config.
	if !skillExistsSomewhere(ctx, name) {
		writeFmt(ctx, "Skill not found: %s. Use /skills to list available skills.\n", name)
		return nil
	}
	if ctx.Config != nil && skillEnabled(ctx.Config, name, ctx.SkillRegistry) {
		writeFmt(ctx, "Skill %s is already enabled.\n", name)
		return nil
	}
	if err := setSkillEnabledState(ctx, name, true); err != nil {
		return err
	}
	writeFmt(ctx, "Enabled skill %s.\n", name)
	return nil
}

// disableSkill disables a skill and persists the change: embedded skills are
// global (home config), other loaded skills are per-project (project config).
func disableSkill(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /skill:disable <name>")
	}
	name := args[0]
	if !skillExistsSomewhere(ctx, name) {
		writeFmt(ctx, "Skill not found: %s. Use /skills to list available skills.\n", name)
		return nil
	}
	if ctx.Config != nil && !skillEnabled(ctx.Config, name, ctx.SkillRegistry) {
		writeFmt(ctx, "Skill %s is already disabled.\n", name)
		return nil
	}
	if err := setSkillEnabledState(ctx, name, false); err != nil {
		return err
	}
	writeFmt(ctx, "Disabled skill %s.\n", name)
	return nil
}

// skillExistsSomewhere reports whether the named skill is loaded or
// discoverable (disabled skills are not loaded but still exist on disk).
func skillExistsSomewhere(ctx core.Context, name string) bool {
	if ctx.SkillRegistry == nil {
		return false
	}
	if _, ok := ctx.SkillRegistry.Get(name); ok {
		return true
	}
	if reg, ok := ctx.SkillRegistry.(*skills.SkillRegistry); ok {
		_, ok := reg.SourceOf(name)
		return ok
	}
	return false
}

// listSkills displays all available skills.
// Depends on OutputWriter + SkillRegistry.
func listSkills(ctx core.Context, reg core.SkillRegistry) error {
	w := ctx
	if reg == nil {
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
	var summaries []skills.SkillSummary
	if reg != nil {
		summaries = reg.List()
	}
	if ctx.Config != nil && ctx.Config.Skills.ExecutionMode == config.AgenticSkillModeInline {
		summaries = filterSkillsForMode(summaries, true)
	}
	if len(summaries) > 0 {
		writeStr(w, "# Skills\n\n")
		for _, s := range summaries {
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
		writeFmt(w, "\n%d skill(s). Use `/skill:run:<name>` to execute.\n", len(summaries))
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

// filterSkillsForMode removes skills that require a sub-agent when the global
// execution mode is inline.
func filterSkillsForMode(summaries []skills.SkillSummary, inlineMode bool) []skills.SkillSummary {
	if !inlineMode {
		return summaries
	}
	filtered := make([]skills.SkillSummary, 0, len(summaries))
	for _, s := range summaries {
		if !s.RequiresSubAgent {
			filtered = append(filtered, s)
		}
	}
	return filtered
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

	if reg.HasSubSkills(name) || skillExecutionMode(ctx.Config) == config.AgenticSkillModeSubAgent {
		return runSkillViaTool(ctx, reg, skill, task, submitFunc, name)
	}
	return runSkillInline(ctx, skill, task, submitFunc, name)
}

// runSkillViaTool executes a skill by invoking the run_skill tool if it is
// registered; otherwise it falls back to the SkillSubAgentRunner.
func runSkillViaTool(ctx core.Context, reg core.SkillRegistry, skill *skills.Skill, task string, submitFunc func(string), name string) error {
	if ctx.ToolRegistry != nil {
		if tool, ok := ctx.ToolRegistry.Get("run_skill"); ok {
			return executeRunSkillTool(ctx, tool, skill, task, submitFunc, name)
		}
	}
	return runSkillSubAgent(ctx, reg, skill, task, submitFunc, name)
}

func executeRunSkillTool(ctx core.Context, tool agentic.Tool, skill *skills.Skill, task string, submitFunc func(string), name string) error {
	if task == "" {
		task = "Run the commands in the skill instructions and return the raw output. Do not plan or explain."
	}
	writeFmt(ctx, "Running skill '%s' via run_skill tool...\n", name)
	input := fmt.Sprintf(`{"skill_name":%q,"task":%q}`, name, task)
	result, err := tool.Execute(input)
	if err != nil {
		writeFmt(ctx, "Skill '%s' failed: %v\n", name, err)
		return nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Skill result: %s]\n%s\n", name, result))
	if submitFunc != nil {
		submitFunc(sb.String())
		writeFmt(ctx, "Skill '%s' completed. Result sent to agent.\n", name)
	} else {
		writeStr(ctx, sb.String())
	}
	return nil
}

// skillExecutionMode returns the effective skill execution mode from config.
// It defaults to inline when no config is present or the mode is empty.
func skillExecutionMode(cfg *config.Config) string {
	if cfg == nil {
		return config.AgenticSkillModeInline
	}
	mode := cfg.Skills.ExecutionMode
	if mode == "" {
		return config.AgenticSkillModeInline
	}
	return mode
}

func runSkillSubAgent(ctx core.Context, reg core.SkillRegistry, skill *skills.Skill, task string, submitFunc func(string), name string) error {
	if ctx.SkillSubAgentRunner == nil {
		writeFmt(ctx, "Sub-agent execution is not available for skill '%s' (no runner configured).\n", name)
		return nil
	}
	if task == "" {
		task = "Run the commands in the skill instructions and return the raw output. Do not plan or explain."
	}
	writeFmt(ctx, "Running skill '%s' in sub-agent...\n", name)
	systemPrompt := "You are a skill executor. Execute the instructions in the user message and return the final output. Do not plan, summarize, or explain the instructions; perform the work immediately. Use the bash tool for shell commands. Return only the final output."
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Skill: %s]\n%s\n", name, skill.Body))
	if subs := reg.SubSkills(name); len(subs) > 0 {
		b.WriteString("\n## Sub-skills\n")
		for _, sub := range subs {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", sub.Meta.Name, sub.Body))
		}
	}
	if imports := reg.ImportedSkills(name); len(imports) > 0 {
		b.WriteString("\n## Imported skills\n")
		for _, imp := range imports {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", imp.Meta.Name, imp.Body))
		}
	}
	b.WriteString(fmt.Sprintf("\nTask: %s\n", task))
	result, err := ctx.SkillSubAgentRunner.Run(context.Background(), systemPrompt, b.String(), skill.Meta.Tools)
	if err != nil {
		writeFmt(ctx, "Skill '%s' failed: %v\n", name, err)
		return nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Skill result: %s]\n%s\n", name, result))
	if submitFunc != nil {
		submitFunc(sb.String())
		writeFmt(ctx, "Skill '%s' completed. Result sent to agent.\n", name)
	} else {
		writeStr(ctx, sb.String())
	}
	return nil
}

// runSkillInline executes a skill in the main agent: the skill body is
// framed as active instructions and submitted as a user message. Skill bodies
// are never injected into the system prompt.
func runSkillInline(ctx core.Context, skill *skills.Skill, task string, submitFunc func(string), name string) error {
	var sb strings.Builder
	// Strip SPDX/comment noise and the bare "[Skill:]" marker (Issue B), and
	// frame the body as active instructions to execute — not documentation to
	// read (Issue A) — so the model performs the task instead of describing it.
	sb.WriteString(fmt.Sprintf("The skill %q is now active. Its instructions follow; execute them using the available tools. Do not just describe or re-read them.\n\n", skill.Meta.Name))
	sb.WriteString(skills.StripSkillNoise(skill.Body))
	sb.WriteString("\n")
	if task != "" {
		sb.WriteString(fmt.Sprintf("\n## Task\n%s\n", task))
	} else {
		sb.WriteString("\n## Task\nApply the skill instructions above to the current context and complete the work now.\n")
	}
	if submitFunc != nil {
		submitFunc(sb.String())
		writeFmt(ctx, "Skill '%s' sent to agent.\n", name)
	} else {
		writeStr(ctx, sb.String())
	}
	return nil
}

func skillTypeLabel(skill *skills.Skill) string {
	if skill.Meta.Inline {
		return "inline"
	}
	if skill.Meta.Category == skills.SkillCategoryKnowledge {
		return "knowledge"
	}
	return "action"
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
			writeFmt(w, "Type: %s\n", skillTypeLabel(s))
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
