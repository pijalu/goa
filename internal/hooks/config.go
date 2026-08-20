// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadConfig loads hook configurations from user and project directories.
// Project-level hooks are appended after user-level hooks, so project hooks
// can override or extend user hooks by event. The returned configuration is
// the merged result.
//
// In addition to goa-native .goa/hooks.yaml files, a Claude Code hook config
// is accepted as an additional source (gap TL4): ~/.claude/hooks.json and
// ~/.claude/settings.json (user scope) and .claude/hooks.json and
// .claude/settings.json (project scope). Both the bare event-map shape
// ({"PreToolUse": [...]}) and the settings shape ({"hooks": {...}}) are
// accepted; only command hooks run, and other hook types are reported in
// Config.Warnings. PreToolUse/PostToolUse map onto beforeTool/afterTool with
// their tool-name matcher, SessionStart/SessionEnd map onto sessionStart/
// sessionEnd (matchers discarded: goa's session payloads carry no source or
// reason).
func LoadConfig(homeDir, projectDir string) (*Config, error) {
	var merged Config

	// User scope: goa-native first, then Claude Code.
	userGoa := filepath.Join(homeDir, ".goa", "hooks.yaml")
	if _, err := os.Stat(userGoa); err == nil {
		cfg, err := loadFile(userGoa)
		if err != nil {
			return nil, fmt.Errorf("load user hooks: %w", err)
		}
		merged.Hooks = append(merged.Hooks, cfg.Hooks...)
	}
	if err := appendClaudeSources(&merged, homeDir, projectDir); err != nil {
		return nil, err
	}

	// Project scope: goa-native first, then Claude Code.
	projectGoa := filepath.Join(projectDir, ".goa", "hooks.yaml")
	if _, err := os.Stat(projectGoa); err == nil {
		cfg, err := loadFile(projectGoa)
		if err != nil {
			return nil, fmt.Errorf("load project hooks: %w", err)
		}
		merged.Hooks = append(merged.Hooks, cfg.Hooks...)
	}
	if err := appendClaudeSources(&merged, projectDir, projectDir); err != nil {
		return nil, err
	}

	if err := merged.Validate(); err != nil {
		return nil, err
	}
	return &merged, nil
}

// appendClaudeSources loads a Claude Code hook config from baseDir (either the
// home or the project directory): hooks.json (bare event map or {hooks:...})
// and settings.json (hooks key). projectDir is used for ${CLAUDE_PROJECT_DIR}
// substitution and as the hook working directory.
func appendClaudeSources(merged *Config, baseDir, projectDir string) error {
	for _, name := range []string{"hooks.json", "settings.json"} {
		path := filepath.Join(baseDir, ".claude", name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		ccHooks, warnings, err := loadClaudeFile(path, projectDir)
		if err != nil {
			return fmt.Errorf("load claude hooks %s: %w", path, err)
		}
		merged.Hooks = append(merged.Hooks, ccHooks...)
		merged.Warnings = append(merged.Warnings, warnings...)
	}
	return nil
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// claudeEventSpec maps a Claude Code hook event onto a goa event and records
// whether the event consumes a tool-name matcher.
type claudeEventSpec struct {
	cc      string
	goa     Event
	matcher bool
}

// claudeEventOrder is the supported CC hook subset (gap TL4). The other CC
// events (UserPromptSubmit, Stop, SubagentStart/Stop, ...) are ignored before
// group parsing, so they cannot invalidate or register hooks.
var claudeEventOrder = []claudeEventSpec{
	{cc: "PreToolUse", goa: EventBeforeTool, matcher: true},
	{cc: "PostToolUse", goa: EventAfterTool, matcher: true},
	{cc: "SessionStart", goa: EventSessionStart, matcher: false},
	{cc: "SessionEnd", goa: EventSessionEnd, matcher: false},
}

// loadClaudeFile parses a Claude Code hooks.json / settings.json file into
// goa Hook values carrying the claude-code dialect. Only command hooks run;
// non-command hook types are skipped and reported as warnings.
func loadClaudeFile(path, projectDir string) ([]Hook, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return parseClaudeHooks(data, projectDir)
}

// parseClaudeHooks parses Claude Code hook JSON: either the bare event map
// ({"PreToolUse": [...]}) or a settings/plugin shape with a "hooks" key
// ({"hooks": {...}}). Unsupported events are ignored before their groups are
// parsed; command hooks are converted with ${CLAUDE_PROJECT_DIR} substituted
// into command and args, and the CC default 600s timeout applied when the
// handler sets none. Non-command handlers are skipped and reported.
func parseClaudeHooks(data []byte, projectDir string) ([]Hook, []string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	events := root
	if hooksRaw, ok := root["hooks"]; ok {
		var hooksMap map[string]json.RawMessage
		if err := json.Unmarshal(hooksRaw, &hooksMap); err != nil {
			return nil, nil, fmt.Errorf("hooks: %w", err)
		}
		events = hooksMap
	}

	var out []Hook
	var warnings []string
	for _, spec := range claudeEventOrder {
		rawGroups, ok := events[string(spec.cc)]
		if !ok {
			continue
		}
		hs, ws, err := parseClaudeEvent(spec, rawGroups, projectDir)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, hs...)
		warnings = append(warnings, ws...)
	}
	return out, warnings, nil
}

// parseClaudeEvent parses one supported CC event's matcher groups.
func parseClaudeEvent(spec claudeEventSpec, rawGroups json.RawMessage, projectDir string) ([]Hook, []string, error) {
	var groups []json.RawMessage
	if err := json.Unmarshal(rawGroups, &groups); err != nil {
		return nil, nil, fmt.Errorf("event %s: %w", spec.cc, err)
	}
	var out []Hook
	var warnings []string
	for gi, rawGroup := range groups {
		hs, ws, err := parseClaudeGroup(spec, rawGroup, gi, projectDir)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, hs...)
		warnings = append(warnings, ws...)
	}
	return out, warnings, nil
}

// parseClaudeGroup parses one matcher group into command hooks. Non-command
// hooks are skipped with a warning; empty commands are skipped too.
func parseClaudeGroup(spec claudeEventSpec, rawGroup json.RawMessage, gi int, projectDir string) ([]Hook, []string, error) {
	var group struct {
		Matcher string            `json:"matcher"`
		Hooks   []json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(rawGroup, &group); err != nil {
		return nil, nil, fmt.Errorf("event %s group %d: %w", spec.cc, gi, err)
	}
	var out []Hook
	var warnings []string
	for hi, rawHook := range group.Hooks {
		var h struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Timeout *int     `json:"timeout"`
		}
		if err := json.Unmarshal(rawHook, &h); err != nil {
			return nil, nil, fmt.Errorf("event %s group %d hook %d: %w", spec.cc, gi, hi, err)
		}
		hookType := h.Type
		if hookType == "" {
			hookType = "command"
		}
		if hookType != "command" {
			warnings = append(warnings, fmt.Sprintf("%s: skipped non-command hook of type %q", spec.cc, hookType))
			continue
		}
		if h.Command == "" {
			warnings = append(warnings, fmt.Sprintf("%s: skipped hook with empty command", spec.cc))
			continue
		}
		hook := Hook{
			Event:          spec.goa,
			Command:        substituteCommand(h.Command, projectDir),
			Args:           substituteArgs(h.Args, projectDir),
			Dialect:        DialectClaudeCode,
			TimeoutSeconds: claudeTimeout(h.Timeout),
			WorkDir:        projectDir,
		}
		if spec.matcher {
			hook.Matcher = group.Matcher
		}
		out = append(out, hook)
	}
	return out, warnings, nil
}

// claudeTimeout applies the CC reference default of 600 seconds when a command
// hook sets no per-hook timeout.
func claudeTimeout(t *int) int {
	if t != nil && *t > 0 {
		return *t
	}
	return 600
}

// substituteCommand replaces ${CLAUDE_PROJECT_DIR} in a CC command string.
// ${CLAUDE_PLUGIN_ROOT} / ${CLAUDE_PLUGIN_DATA} have no goa equivalent and
// stay verbatim (matching dsh's rule that an unset token remains unchanged).
func substituteCommand(command, projectDir string) string {
	if projectDir == "" {
		return command
	}
	return strings.ReplaceAll(command, "${CLAUDE_PROJECT_DIR}", projectDir)
}

// substituteArgs applies ${CLAUDE_PROJECT_DIR} substitution to exec-form args.
func substituteArgs(args []string, projectDir string) []string {
	if projectDir == "" || len(args) == 0 {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "${CLAUDE_PROJECT_DIR}", projectDir)
	}
	return out
}
