// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
)

func configSubcommandCompletions(prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"set", "set a config key"},
		{"add", "add a provider or model"},
		{"remove", "remove a provider or model"},
		{"reload", "reload config"},
		{"temp", "session-level temp overrides (loop detection)"},
	} {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

// configTempCompletions returns actionable /config:temp:<setting>:<on|off>
// argument completions. Only the value that changes the current state is
// proposed; the matching value is filtered out so the user always picks an
// action.
func configTempCompletions(ctx core.Context, settingPrefix, valuePrefix string) []core.ArgCompletion {
	settings := []struct{ val, desc, kind string }{
		{"think_loop_detection", "thinking-loop detection", "think"},
		{"tool_loop_detection", "tool-call loop detection", "tool"},
		{"stream_loop_detection", "stream-text loop detection", "stream"},
		{"thinking_stall_detection", "thinking-stall watchdog", "stall"},
	}
	var comps []core.ArgCompletion
	for _, s := range settings {
		if settingPrefix != "" && !strings.HasPrefix(s.val, settingPrefix) {
			continue
		}
		currentDisabled := ctx.LoopDetector != nil && ctx.LoopDetector.TempOverride(s.kind)
		nextValue := "off"
		state := "disable"
		if currentDisabled {
			nextValue = "on"
			state = "enable"
		}
		if valuePrefix != "" && !strings.HasPrefix(nextValue, valuePrefix) {
			continue
		}
		comps = append(comps, core.ArgCompletion{
			Value:       "temp:" + s.val + ":" + nextValue,
			Description: fmt.Sprintf("%s %s", state, s.desc),
		})
	}
	return comps
}

// configTempArgCompletions parses the raw prefix after "/config" and returns
// actionable temp completions, or nil if the prefix is not a temp request.
func configTempArgCompletions(ctx core.Context, prefix string) []core.ArgCompletion {
	if prefix == "" {
		return nil
	}
	clean := strings.TrimSpace(prefix)
	parts := strings.SplitN(clean, ":", 3)
	head := parts[0]

	if head != "temp" && !strings.HasPrefix("temp", head) && !strings.HasPrefix(head, "temp") {
		return nil
	}

	if len(parts) == 1 {
		return configTempCompletions(ctx, "", "")
	}

	setting := strings.TrimSpace(parts[1])
	if len(parts) == 2 {
		return configTempCompletions(ctx, setting, "")
	}

	valuePrefix := parts[2]
	return configTempCompletions(ctx, setting, valuePrefix)
}

func prefixKeys(subPrefix, key string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, k := range configKeyCompletions(key) {
		comps = append(comps, core.ArgCompletion{Value: subPrefix + k.Value, Description: k.Description})
	}
	return comps
}

func prefixValues(subPrefix, key, valuePrefix string, ctx core.Context) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range configValueCompletions(ctx, key, valuePrefix) {
		comps = append(comps, core.ArgCompletion{Value: subPrefix + key + ":" + v.Value, Description: v.Description})
	}
	return comps
}

func configKeyCompletions(prefix string) []core.ArgCompletion {
	keys := []struct{ value, description string }{
		{"mode.default.major", "coder | planner | reviewer | <custom>"},
		{"active_provider", "provider id"},
		{"active_model", "model id"},
		{"execution.mode", "yolo | confirm | review | solo"},
		{"execution.auto_save_model", "true | false (persist model changes to project config)"},
		{"mode.plan_file_path", "path to plan file (default: .goa/plan.md)"},
		{"execution.max_tool_calls", "integer"},
		{"execution.max_tool_repeat_total", "integer"},
		{"execution.max_tool_repeat_consecutive", "integer"},
		{"execution.max_tool_repeat", "integer"},
		{"execution.max_consecutive_tool_rounds", "integer (0 = disabled, default 15)"},
		{"tui.theme", "dark | light"},
		{"tui.spinner", "spinner name or none"},
		{"tui.transparency.show_thinking", "true | false"},
		{"tui.transparency.thinking_collapsed", "true | false"},
		{"logging.level", "debug | info | warn | error"},
		{"logging.file", "path"},
		{"thinking_level", "off | minimal | low | medium | high | xhigh"},
		{"multi_agent.enabled", "true | false"},
		{"multi_agent.companion_model", "model id"},
		{"multi_agent.companion_provider", "provider id"},
		{"tools.enabled.goal", "enable goal tools (default false)"},
		{"tools.bash.enable_complexity_analysis", "true | false"},
		{"tools.bash.warn_file_edits", "true | false (default true)"},
		{"tools.bash.jail", "true | false"},
		{"tools.bash.max_complexity_score", "integer (0 = default)"},
		{"tools.terminal.sandbox.enabled", "true | false"},
		{"orchestrator.roles.*", "{ model: <id>, provider: <id>, allowed_tools: [<names>] }"},
		{"orchestrator.pool.max_total_agents", "integer (0 = unlimited pool delegation)"},
		{"orchestrator.pool.max_agents_per_model.*", "integer >= 1"},
		{"orchestrator.defaults.topology", "hub | fanout | pipeline"},
		{"teams.active", "team name from teams.definitions"},
		{"teams.definitions.*.main.model", "model id"},
		{"teams.definitions.*.companion.model", "model id"},
		{"teams.definitions.*.members.*.model", "model id"},
		{"teams.definitions.*.members.*.role", "main | reviewer | worker"},
		{"teams.definitions.*.members.*.thinking_level", "off | minimal | low | medium | high | xhigh"},
		{"teams.definitions.*.review", "off | agent | framework | gated"},
		{"teams.definitions.*.review_gates.triggers", "comma list: turn_end, goal_complete, goal_turn, file_commit, run_complete"},
		{"teams.definitions.*.review_gates.quorum", "all | any"},
		{"teams.definitions.*.delegation", "agent | off"},
	}
	var comps []core.ArgCompletion
	for _, k := range keys {
		if prefix == "" || strings.HasPrefix(k.value, prefix) {
			comps = append(comps, core.ArgCompletion{Value: k.value, Description: k.description})
		}
	}
	return comps
}

func configValueCompletions(ctx core.Context, key, prefix string) []core.ArgCompletion {
	if completion := contextCompletion(key, ctx); completion != nil {
		return completion(prefix)
	}
	return staticCompletion(key, prefix)
}

type completionFunc func(string) []core.ArgCompletion

func contextCompletion(key string, ctx core.Context) completionFunc {
	return map[string]completionFunc{
		"mode.default.major":                  func(prefix string) []core.ArgCompletion { return profileCompletionValues(ctx, prefix) },
		"tui.transparency.show_thinking":      func(prefix string) []core.ArgCompletion { return configBoolCompletionValues(ctx, key, prefix) },
		"tui.transparency.thinking_collapsed": func(prefix string) []core.ArgCompletion { return configBoolCompletionValues(ctx, key, prefix) },
		"multi_agent.enabled":                 func(prefix string) []core.ArgCompletion { return configBoolCompletionValues(ctx, key, prefix) },
		"active_model":                        func(prefix string) []core.ArgCompletion { return modelCompletionValues(ctx, prefix) },
		"active_provider":                     func(prefix string) []core.ArgCompletion { return providerCompletionValues(ctx, prefix) },
		"multi_agent.companion_provider":      func(prefix string) []core.ArgCompletion { return providerCompletionValues(ctx, prefix) },
		"teams.active":                        func(prefix string) []core.ArgCompletion { return teamCompletionValues(ctx, prefix) },
	}[key]
}

func staticCompletion(key, prefix string) []core.ArgCompletion {
	values := map[string]completionFunc{
		"execution.mode": modeCompletionValues, "tui.theme": themeCompletionValues,
		"thinking_level":     thinkingLevelCompletionValues,
		"tools.enabled.goal": boolCompletionValues, "tools.bash.enable_complexity_analysis": boolCompletionValues,
		"tools.bash.jail": boolCompletionValues, "tools.terminal.sandbox.enabled": boolCompletionValues,
		"orchestrator.defaults.topology": func(p string) []core.ArgCompletion {
			return filteredCompletions([]string{"hub", "fanout", "pipeline"}, p, "")
		},
		"teams.definitions.*.review": func(p string) []core.ArgCompletion {
			return filteredCompletions([]string{"off", "agent", "framework", "gated"}, p, "")
		},
		"teams.definitions.*.members.*.role": func(p string) []core.ArgCompletion {
			return filteredCompletions([]string{"main", "reviewer", "worker"}, p, "")
		},
		"teams.definitions.*.members.*.thinking_level": thinkingLevelCompletionValues,
		"teams.definitions.*.review_gates.quorum":      func(p string) []core.ArgCompletion { return filteredCompletions([]string{"all", "any"}, p, "") },
		"teams.definitions.*.delegation":               func(p string) []core.ArgCompletion { return filteredCompletions([]string{"agent", "off"}, p, "") },
		"mode.plan_file_path": func(string) []core.ArgCompletion {
			return []core.ArgCompletion{{Value: ".goa/plan.md", Description: "default plan file in project root"}}
		},
	}[key]
	if values == nil {
		return nil
	}
	return values(prefix)
}

// teamCompletionValues completes team names from the merged definitions.
func teamCompletionValues(ctx core.Context, prefix string) []core.ArgCompletion {
	if ctx.Config == nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, name := range ctx.Config.TeamNames() {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			comps = append(comps, core.ArgCompletion{Value: name, Description: "defined team"})
		}
	}
	return comps
}

func profileCompletionValues(ctx core.Context, prefix string) []core.ArgCompletion {
	if ctx.ModeRegistry != nil {
		majors := ctx.ModeRegistry.Majors()
		values := make([]string, 0, len(majors))
		for _, m := range majors {
			values = append(values, string(m))
		}
		return filteredCompletions(values, prefix, "")
	}
	return filteredCompletions([]string{"coder", "planner", "reviewer"}, prefix, "")
}

func modeCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"yolo", "solo", "confirm", "review"}, prefix, "")
}

func themeCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"dark", "light"}, prefix, "")
}

func configBoolCompletionValues(ctx core.Context, key, prefix string) []core.ArgCompletion {
	var current bool
	switch key {
	case "tui.transparency.show_thinking":
		current = ctx.Config.TUI.Transparency.ShowThinking
	case "tui.transparency.thinking_collapsed":
		current = ctx.Config.TUI.Transparency.ThinkingCollapsed
	case "multi_agent.enabled":
		current = ctx.Config.MultiAgent.Enabled
	}
	next := "false"
	if !current {
		next = "true"
	}
	if prefix != "" && !strings.HasPrefix(next, prefix) {
		return nil
	}
	return []core.ArgCompletion{{Value: next, Description: ""}}
}

func boolCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"true", "false"}, prefix, "")
}

func thinkingLevelCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"off", "minimal", "low", "medium", "high", "xhigh"}, prefix, "")
}

func modelCompletionValues(ctx core.Context, prefix string) []core.ArgCompletion {
	var values []string
	for _, m := range ctx.Config.Models {
		values = append(values, m.ID)
	}
	return filteredCompletions(values, prefix, "")
}

func providerCompletionValues(ctx core.Context, prefix string) []core.ArgCompletion {
	var values []string
	seen := map[string]bool{}
	for _, p := range ctx.Config.Providers {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		values = append(values, p.ID)
	}
	return filteredCompletions(values, prefix, "")
}

func filteredCompletions(values []string, prefix, desc string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range values {
		if prefix == "" || strings.HasPrefix(v, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v, Description: desc})
		}
	}
	return comps
}
