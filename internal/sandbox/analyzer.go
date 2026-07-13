// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// AnalysisResult holds the safety classification of a shell command produced
// by AST-based static analysis.
type AnalysisResult struct {
	// Destructive is true when the command invokes a known destructive
	// program (rm, dd, mkfs, etc.).
	Destructive bool

	// Network is true when the command invokes a known network program
	// (curl, wget, ssh, nc, etc.).
	Network bool

	// Interactive is true when the command invokes a known interactive
	// program (vim, nano, less, etc.).
	Interactive bool

	// TooComplex is true when the command AST cannot be reliably analyzed
	// (e.g. unsupported syntax, dynamic command construction).
	TooComplex bool

	// Blocked is true when any command name matches an entry in Blocked.
	Blocked bool

// Allowed is false when Allowed is non-empty and no command name matches.
	Allowed bool

	// Commands is the deduplicated list of command names found in the script.
	Commands []string

	// Reason is a human-readable summary of the findings.
	Reason string
}

// Analyzer statically classifies shell commands using an AST parser.
// It is intended to be used before execution as a defence-in-depth layer.
type Analyzer struct {
	// Blocked is the exact set of command names that are rejected.
	Blocked []string

	// Allowed, when non-empty, restricts execution to these command names.
	// Commands not in Allowed are rejected.
	Allowed []string

	// DestructiveCommands are programs considered destructive to local state.
	// When empty a built-in list is used.
	DestructiveCommands []string

	// NetworkCommands are programs that perform network I/O.
	// When empty a built-in list is used.
	NetworkCommands []string

	// InteractiveCommands are programs that require a terminal.
	// When empty a built-in list is used.
	InteractiveCommands []string

	// MaxComplexityScore caps the tolerable complexity of a command AST.
	// Zero defaults to a conservative threshold.
	MaxComplexityScore int

	// EnableComplexity, when explicitly set to false, skips the AST
	// complexity/dynamic-command checks while still enforcing blocked/allowed
	// lists and category flags. A nil pointer preserves the default enabled
	// behaviour for backward-compatible analyzer usage.
	EnableComplexity *bool
}

// built-in command category lists.
var (
	defaultDestructiveCommands = []string{
		"rm", "rmdir", "shred", "dd", "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.ntfs",
		"fdisk", "parted", "partprobe", "wipefs", "truncate", "chattr", "xattr",
	}
	defaultNetworkCommands = []string{
		"curl", "wget", "nc", "netcat", "ssh", "scp", "sftp", "ftp", "telnet",
		"nmap", "ping", "traceroute", "dig", "host", "nslookup", "whois",
	}
	defaultInteractiveCommands = []string{
		"vim", "vi", "nvim", "nano", "pico", "emacs", "ed", "less", "more",
		"watch", "top", "htop", "btop", "psql", "mysql", "sqlite3",
	}
)

// defaultMaxComplexity is the default AST complexity score at which a command
// is considered too complex for reliable static analysis.
const defaultMaxComplexity = 50

// NewAnalyzer creates an Analyzer from blocked and allowed command lists.
// Blocked commands are rejected; when Allowed is non-empty only those
// commands are accepted. Empty built-in category lists are used. Complexity
// analysis is enabled by default for backward-compatible behaviour.
func NewAnalyzer(blocked, allowed []string) *Analyzer {
	return &Analyzer{
		Blocked:          blocked,
		Allowed:          allowed,
		EnableComplexity: boolPtr(true),
	}
}

// boolPtr returns a pointer to a bool value.
func boolPtr(v bool) *bool { return &v }

// complexityEnabled reports whether the AST complexity/dynamic-command checks
// should run. The default is enabled unless explicitly disabled.
func (a *Analyzer) complexityEnabled() bool {
	if a.EnableComplexity == nil {
		return true
	}
	return *a.EnableComplexity
}

// Analyze parses cmd and returns a safety classification.
func (a *Analyzer) Analyze(cmd string) (AnalysisResult, error) {
	if strings.TrimSpace(cmd) == "" {
		return AnalysisResult{Allowed: true}, nil
	}

	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(cmd), "")
	if err != nil {
		return AnalysisResult{
			TooComplex: true,
			Reason:     fmt.Sprintf("command parse failed: %v", err),
		}, nil
	}

	commands, dynamic := extractCommandNames(file)

	if a.complexityEnabled() {
		score := complexityScore(file)
		max := a.MaxComplexityScore
		if max <= 0 {
			max = defaultMaxComplexity
		}
		if score > max {
			return AnalysisResult{
				TooComplex: true,
				Reason:     fmt.Sprintf("command complexity score %d exceeds threshold %d", score, max),
			}, nil
		}
		if dynamic {
			return AnalysisResult{
				Commands:   commands,
				TooComplex: true,
				Reason:     "dynamic command construction (command substitution or variable expansion in command position)",
			}, nil
		}
	}

	blocked, reason := a.classify(commands, complexityScore(file))
	return AnalysisResult{
		Destructive: blocked.Destructive,
		Network:     blocked.Network,
		Interactive: blocked.Interactive,
		TooComplex:  blocked.TooComplex,
		Blocked:     blocked.Blocked,
		Allowed:     blocked.Allowed,
		Commands:    commands,
		Reason:      reason,
	}, nil
}

// classify derives the per-category flags and a human-readable reason.
func (a *Analyzer) classify(commands []string, score int) (AnalysisResult, string) {
	destructive := a.destructiveSet()
	network := a.networkSet()
	interactive := a.interactiveSet()
	blocked := a.blockedSet()
	allowed := a.allowedSet()

	var found []string
	res := AnalysisResult{Allowed: len(allowed) == 0}
	for _, c := range commands {
		base := baseName(c)
		if contains(destructive, base) {
			res.Destructive = true
			found = append(found, base+"(destructive)")
		}
		if contains(network, base) {
			res.Network = true
			found = append(found, base+"(network)")
		}
		if contains(interactive, base) {
			res.Interactive = true
			found = append(found, base+"(interactive)")
		}
		if contains(blocked, base) {
			res.Blocked = true
			found = append(found, base+"(blocked)")
		}
	}

	if len(allowed) > 0 {
		res.Allowed = true
		for _, c := range commands {
			base := baseName(c)
			if !contains(allowed, base) {
				res.Allowed = false
				found = append(found, base+"(not allowed)")
				break
			}
		}
	}

	reason := a.buildReason(res, commands, found, allowed, score)
	return res, reason
}

func (a *Analyzer) buildReason(res AnalysisResult, commands, found []string, allowed map[string]bool, score int) string {
	if len(found) == 0 && len(allowed) == 0 {
		return fmt.Sprintf("analyzed %d command(s), score=%d", len(commands), score)
	}
	var parts []string
	if res.Blocked {
		parts = append(parts, "blocked command detected")
	}
	if len(allowed) > 0 && !res.Allowed && len(commands) > 0 {
		parts = append(parts, "command not in allowed list")
	}
	if res.Destructive {
		parts = append(parts, "destructive command")
	}
	if res.Network {
		parts = append(parts, "network command")
	}
	if res.Interactive {
		parts = append(parts, "interactive command")
	}
	if len(parts) == 0 {
		return fmt.Sprintf("commands: %v, score=%d", commands, score)
	}
	return fmt.Sprintf("%s (commands: %v)", strings.Join(parts, ", "), found)
}

func (a *Analyzer) destructiveSet() map[string]bool {
	return set(a.DestructiveCommands, defaultDestructiveCommands)
}

func (a *Analyzer) networkSet() map[string]bool {
	return set(a.NetworkCommands, defaultNetworkCommands)
}

func (a *Analyzer) interactiveSet() map[string]bool {
	return set(a.InteractiveCommands, defaultInteractiveCommands)
}

func (a *Analyzer) blockedSet() map[string]bool {
	return set(a.Blocked, nil)
}

func (a *Analyzer) allowedSet() map[string]bool {
	return set(a.Allowed, nil)
}

func set(a []string, fallback []string) map[string]bool {
	if len(a) == 0 && len(fallback) > 0 {
		a = fallback
	}
	m := make(map[string]bool, len(a))
	for _, v := range a {
		if v != "" {
			m[v] = true
		}
	}
	return m
}

func contains(m map[string]bool, s string) bool {
	return m[s]
}

func baseName(cmd string) string {
	if i := strings.LastIndex(cmd, "/"); i >= 0 {
		return cmd[i+1:]
	}
	return cmd
}

// extractCommandNames walks the shell AST and returns the deduplicated command
// names in execution order. It also detects dynamic command invocation
// (command substitution or variable expansion in the command position) and
// reports that via the returned bool.
func extractCommandNames(file *syntax.File) ([]string, bool) {
	seen := make(map[string]bool)
	var names []string
	dynamic := false

	walk(file, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		arg := call.Args[0]
		if arg == nil {
			return true
		}
		if isDynamicCommand(arg) {
			dynamic = true
			return true
		}
		// Only literal command names are counted; otherwise the command is
		// dynamic and has been flagged above.
		if name := literalWord(arg); name != "" {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		return true
	})

	return names, dynamic
}

// isDynamicCommand reports whether the first argument of a call is a command
// substitution or variable expansion, meaning the command name is determined
// at runtime.
func isDynamicCommand(w *syntax.Word) bool {
	if w == nil || len(w.Parts) == 0 {
		return false
	}
	// Only a single non-literal part in the command position counts as dynamic.
	if len(w.Parts) != 1 {
		return false
	}
	switch w.Parts[0].(type) {
	case *syntax.CmdSubst, *syntax.ParamExp:
		return true
	}
	return false
}

// literalWord returns the literal string value of a word if it consists of a
// single literal part, otherwise the empty string.
func literalWord(w *syntax.Word) string {
	if w == nil || len(w.Parts) != 1 {
		return ""
	}
	if lit, ok := w.Parts[0].(*syntax.Lit); ok {
		return lit.Value
	}
	return ""
}

// walk traverses every AST node and calls f for each.
func walk(node syntax.Node, f func(syntax.Node) bool) {
	syntax.Walk(node, func(n syntax.Node) bool {
		if n == nil {
			return true
		}
		return f(n)
	})
}

// complexityScore returns a rough measure of command complexity based on AST
// node count and structural features. Higher scores indicate more complex
// commands that are harder to reason about statically.
func complexityScore(file *syntax.File) int {
	score := 0
	walk(file, func(n syntax.Node) bool {
		score++
		switch n.(type) {
		case *syntax.Subshell:
			score += 5
		case *syntax.BinaryCmd:
			score += 2
		case *syntax.IfClause, *syntax.ForClause, *syntax.WhileClause,
			*syntax.CaseClause, *syntax.FuncDecl:
			score += 10
		case *syntax.ArithmExp, *syntax.ArithmCmd:
			score += 3
		case *syntax.CmdSubst:
			score += 4
		case *syntax.ExtGlob:
			score += 2
		}
		return true
	})
	return score
}
