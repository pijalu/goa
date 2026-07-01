// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"fmt"
	"sort"
)

// Engine evaluates a set of permission rules against tool names.
type Engine struct {
	rules []Rule
}

// NewEngine creates an engine from the given rules. Rules are sorted so that
// the most specific pattern is evaluated first; ties preserve declaration order.
func NewEngine(rules []Rule) *Engine {
	cpy := make([]Rule, len(rules))
	copy(cpy, rules)
	sort.SliceStable(cpy, func(i, j int) bool {
		return specificity(cpy[i].Pattern) > specificity(cpy[j].Pattern)
	})
	return &Engine{rules: cpy}
}

// Result is the outcome of evaluating a tool name.
type Result struct {
	Decision Decision
	Reason   string
	Matched  bool
}

// Evaluate returns the decision for toolName in the given mode. If no rule
// matches, it returns {Decision: "", Matched: false}.
func (e *Engine) Evaluate(toolName, mode string) Result {
	for _, r := range e.rules {
		if !r.IsScopedToMode(mode) {
			continue
		}
		if !Match(r.Pattern, toolName) {
			continue
		}
		return Result{
			Decision: r.Decision,
			Matched:  true,
			Reason:   fmt.Sprintf("rule %q (%s)", r.Pattern, r.Decision),
		}
	}
	return Result{Matched: false}
}

// Rules returns the sorted rules.
func (e *Engine) Rules() []Rule {
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

// specificity ranks a pattern by how specific it is. Higher numbers are
// evaluated first. Exact segments add 2, '*' adds 1, '**' adds 0.
func specificity(pattern string) int {
	segs := splitSegments(pattern)
	score := 0
	for _, s := range segs {
		switch s {
		case "**":
			// least specific
		case "*":
			score += 1
		default:
			score += 2
		}
	}
	return score
}
