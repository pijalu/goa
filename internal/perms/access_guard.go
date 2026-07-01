// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/expr-lang/expr"
)

// AccessGuard enforces mode-specific access rules declared in mode metadata.
type AccessGuard struct {
	rules []GuardRule
}

// NewAccessGuard creates a guard from a mode's GuardConfig.
func NewAccessGuard(cfg GuardConfig) *AccessGuard {
	return &AccessGuard{rules: cfg.Rules}
}

// Validate returns an error when a tool call violates a guard rule.
func (g *AccessGuard) Validate(toolName, input string) error {
	for _, r := range g.rules {
		if !g.ruleAppliesToTool(r, toolName) {
			continue
		}
		if g.matchesRule(r, toolName, input) {
			continue
		}
		msg := r.Message
		if msg == "" {
			msg = fmt.Sprintf("mode restriction: %s is not allowed here", toolName)
		}
		return fmt.Errorf("%s", strings.TrimSpace(msg))
	}
	return nil
}

func (g *AccessGuard) ruleAppliesToTool(r GuardRule, toolName string) bool {
	if len(r.Tools) == 0 {
		return true
	}
	for _, t := range r.Tools {
		if Match(t, toolName) {
			return true
		}
	}
	return false
}

func (g *AccessGuard) matchesRule(r GuardRule, toolName, input string) bool {
	paths := pathsFromInput(toolName, input)
	if len(paths) == 0 {
		return true
	}

	if r.Expr != "" {
		for _, p := range paths {
			if !g.evalExpr(r.Expr, toolName, p) {
				return false
			}
		}
		return true
	}

	if len(r.Allow) == 0 {
		return true
	}
	for _, p := range paths {
		if !matchesAnyRegex(p, r.Allow) {
			return false
		}
	}
	return true
}

func (g *AccessGuard) evalExpr(exprStr, toolName, path string) bool {
	env := map[string]any{
		"tool":      toolName,
		"path":      path,
		"regexMatch": regexMatchFunc,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"base":      filepath.Base,
	}
	prog, err := expr.Compile(exprStr, expr.Env(env))
	if err != nil {
		return false
	}
	out, err := expr.Run(prog, env)
	if err != nil {
		return false
	}
	b, _ := out.(bool)
	return b
}

func pathsFromInput(toolName, input string) []string {
	switch toolName {
	case "bash":
		return pathsFromBash(input)
	default:
		p := extractPath(input)
		if p == "" {
			return nil
		}
		return []string{p}
	}
}

func pathsFromBash(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	var paths []string
	if after, ok := strings.CutPrefix(cmd, "cd "); ok {
		target := strings.TrimSpace(after)
		if target != "" {
			paths = append(paths, target)
		}
	}
	// Strip quoted strings so text in commit messages, echo arguments, etc.
	// is not mistaken for filesystem paths.
	flat := stripQuoted(cmd)
	for _, tok := range strings.Fields(flat) {
		if p := looksLikePath(tok); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func matchesAnyRegex(path string, patterns []string) bool {
	for _, pat := range patterns {
		if pat == "" {
			continue
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

func regexMatchFunc(args ...any) (any, error) {
	if len(args) != 2 {
		return false, fmt.Errorf("matches requires 2 arguments")
	}
	s, ok1 := args[0].(string)
	pat, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return false, fmt.Errorf("matches requires string arguments")
	}
	matched, err := regexp.MatchString(pat, s)
	return matched, err
}
