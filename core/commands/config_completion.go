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
		{"mode.plan_file_path", "path to plan file (default: .goa/plan.md)"},
		{"execution.max_tool_calls", "integer"},
		{"execution.max_tool_repeat_total", "integer"},
		{"execution.max_tool_repeat_consecutive", "integer"},
		{"execution.max_tool_repeat", "integer"},
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
		{"orchestrator.roles.*", "{ model: <id>, provider: <id>, allowed_tools: [<names>] }"},
		{"orchestrator.pool.max_total_agents", "integer (0 = unlimited pool delegation)"},
		{"orchestrator.pool.max_agents_per_model.*", "integer >= 1"},
		{"orchestrator.defaults.topology", "hub | fanout | pipeline"},
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
	switch key {
	case "mode.default.major":
		return profileCompletionValues(ctx, prefix)
	case "execution.mode":
		return modeCompletionValues(prefix)
	case "mode.plan_file_path":
		return []core.ArgCompletion{{Value: ".goa/plan.md", Description: "default plan file in project root"}}
	case "tui.theme":
		return themeCompletionValues(prefix)
	case "tui.transparency.show_thinking", "tui.transparency.thinking_collapsed", "multi_agent.enabled":
		return configBoolCompletionValues(ctx, key, prefix)
	case "thinking_level":
		return thinkingLevelCompletionValues(prefix)
	case "active_model":
		return modelCompletionValues(ctx, prefix)
	case "active_provider", "multi_agent.companion_provider":
		return providerCompletionValues(ctx, prefix)
	case "tools.enabled.goal":
		return boolCompletionValues(prefix)
	case "orchestrator.defaults.topology":
		return filteredCompletions([]string{"hub", "fanout", "pipeline"}, prefix, "")
	}
	return nil
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
