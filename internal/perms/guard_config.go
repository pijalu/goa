// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

// GuardRule defines a single access-control rule for a mode.
// It restricts named tools to paths. A rule passes when every referenced
// path matches at least one allow regex, or when Expr (if set) evaluates
// to true for every referenced path.
//
// Expr expressions receive:
//   - tool: the tool name (string)
//   - path: the path being checked (string)
//
// Helper functions:
//   - regexMatch(s, regex string) bool
//   - contains(s, substr string) bool
//   - hasPrefix(s, prefix string) bool
//   - hasSuffix(s, suffix string) bool
//   - base(s) string  // filepath base
type GuardRule struct {
	Tools   []string `yaml:"tools,omitempty" json:"tools,omitempty"`
	Allow   []string `yaml:"allow,omitempty" json:"allow,omitempty"`
	Expr    string   `yaml:"expr,omitempty" json:"expr,omitempty"`
	Message string   `yaml:"message,omitempty" json:"message,omitempty"`
}

// GuardConfig holds access-control rules for a mode.
type GuardConfig struct {
	Rules []GuardRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}
