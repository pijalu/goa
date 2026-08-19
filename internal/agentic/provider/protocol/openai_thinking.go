// SPDX-License-Identifier: GPL-3.0-or-later
package protocol

import "github.com/pijalu/goa/internal/agentic/provider/schema"

func resolveThinkingLevel(model schema.Model, opts schema.StreamOptions, profile schema.VariantProfile) string {
	if opts.Reasoning != "" && opts.Reasoning != schema.ThinkingOff {
		if native, ok := profile.Defaults.ThinkingLevelMap[opts.Reasoning]; ok {
			return native
		}
		return string(opts.Reasoning)
	}
	if profile.Defaults.Thinking != "" {
		return profile.Defaults.Thinking
	}
	return "medium"
}

func thinkingBodyForFormat(format, level string) map[string]any {
	builders := map[string]func(string) map[string]any{
		"openai": openaiThinking, "ant-ling": openaiThinking, "deepseek": deepseekThinking,
		"zai": zaiThinking, "together": togetherThinking, "openrouter": openrouterThinking,
		"string-thinking": deepseekThinking, "qwen": qwenThinking, "qwen-chat-template": qwenThinking,
		"chat-template": qwenThinking, "chat-template-arg": qwenThinking,
	}
	if b, ok := builders[format]; ok {
		return b(level)
	}
	return nil
}

func openaiThinking(level string) map[string]any { return map[string]any{"reasoning_effort": level} }
func deepseekThinking(level string) map[string]any {
	body := map[string]any{"thinking": map[string]any{"type": "enabled"}}
	if level != "" {
		body["reasoning_effort"] = level
	}
	return body
}
func zaiThinking(level string) map[string]any {
	return map[string]any{"thinking": map[string]any{"type": "enabled", "clear_thinking": false}}
}
func thinkingDisabledBodyForFormat(format string) map[string]any {
	switch format {
	case "zai", "deepseek", "string-thinking":
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	}
	return nil
}
func togetherThinking(level string) map[string]any {
	body := map[string]any{"reasoning": map[string]any{"enabled": true}}
	if level != "" {
		body["reasoning_effort"] = level
	}
	return body
}
func openrouterThinking(level string) map[string]any {
	return map[string]any{"reasoning": map[string]any{"effort": level}}
}
func qwenThinking(level string) map[string]any { return map[string]any{"thinking": true} }
