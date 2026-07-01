// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package perms implements a permission-rule engine for tool-call approval.
// Rules are matched by tool name (with '*' and '**' wildcards) and decide
// whether a matching tool is allowed, denied, or requires confirmation.
package perms

// Decision is the result of a permission rule.
type Decision string

const (
	// DecisionAllow permits the tool call.
	DecisionAllow Decision = "allow"
	// DecisionDeny rejects the tool call.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires user confirmation.
	DecisionAsk Decision = "ask"
)

// IsValidDecision reports whether d is a known decision value.
func IsValidDecision(d Decision) bool {
	switch d {
	case DecisionAllow, DecisionDeny, DecisionAsk:
		return true
	}
	return false
}

// Rule is a single user-configured permission rule.
type Rule struct {
	// Pattern matches a tool name. It may contain '*' (one segment) or
	// '**' (any number of segments). Segments are separated by '__' or '/'.
	Pattern string `yaml:"pattern" json:"pattern"`
	// Decision is one of allow, deny, ask.
	Decision Decision `yaml:"decision" json:"decision"`
	// Mode optionally scopes the rule to a specific autonomy mode.
	// Empty means the rule applies to all modes.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// Description is an optional human-readable note.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// IsScopedToMode reports whether the rule applies to the given mode.
func (r Rule) IsScopedToMode(mode string) bool {
	return r.Mode == "" || r.Mode == mode
}
