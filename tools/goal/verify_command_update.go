// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

const defaultVerifyTimeoutLimit = time.Hour

// validateVerifyCommandTimeoutUpdate permits a model to correct the Go test
// timeout without weakening the recorded verification scope. Both commands
// must be the same literal, simple `go test` invocation after removing the
// timeout flag; package patterns, test selection, and every other argument must
// remain equivalent.
func validateVerifyCommandTimeoutUpdate(oldCommand, newCommand string, limit time.Duration) error {
	oldBase, _, err := parseGoTestTimeout(oldCommand)
	if err != nil {
		return fmt.Errorf("existing verifyCommand: %w", err)
	}
	newBase, timeoutText, err := parseGoTestTimeout(newCommand)
	if err != nil {
		return err
	}
	if !slices.Equal(oldBase, newBase) {
		return fmt.Errorf("verifyCommand update may change only its timeout; package scope, test selection, and all other arguments must stay unchanged")
	}
	if timeoutText == "" {
		return fmt.Errorf("replacement verifyCommand must include a -timeout value")
	}
	timeout, err := time.ParseDuration(timeoutText)
	if err != nil {
		return fmt.Errorf("invalid go test timeout %q: %w", timeoutText, err)
	}
	if timeout <= 0 {
		return fmt.Errorf("go test timeout must be positive (got %s)", timeout)
	}
	if limit <= 0 {
		limit = defaultVerifyTimeoutLimit
	}
	if timeout > limit {
		return fmt.Errorf("go test timeout must be at most %s (got %s)", limit, timeout)
	}
	return nil
}

// parseGoTestTimeout parses one literal simple go test command and returns its
// arguments with at most one timeout flag removed. Shell expansion, pipelines,
// compound commands, redirects, and environment assignments are rejected: none
// can be proven scope-preserving by static comparison.
func parseGoTestTimeout(command string) (base []string, timeout string, err error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, "", fmt.Errorf("invalid go test verifyCommand: %w", err)
	}
	if len(file.Stmts) != 1 {
		return nil, "", fmt.Errorf("verifyCommand timeout update requires one simple go test command")
	}
	call, err := simpleLiteralCall(file.Stmts[0])
	if err != nil {
		return nil, "", err
	}
	args, err := literalShellWords(call.Args)
	if err != nil {
		return nil, "", err
	}
	if len(args) < 2 || args[0] != "go" || args[1] != "test" {
		return nil, "", fmt.Errorf("verifyCommand timeout update requires a go test command")
	}
	return stripGoTestTimeout(args)
}

func simpleLiteralCall(stmt *syntax.Stmt) (*syntax.CallExpr, error) {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || hasShellOperators(stmt) || len(call.Assigns) > 0 {
		return nil, fmt.Errorf("verifyCommand timeout update requires one simple go test command without shell operators or expansions")
	}
	return call, nil
}

func hasShellOperators(stmt *syntax.Stmt) bool {
	return stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) > 0
}

func literalShellWords(words []*syntax.Word) ([]string, error) {
	args := make([]string, len(words))
	for i, word := range words {
		value, ok := literalShellWord(word)
		if !ok {
			return nil, fmt.Errorf("verifyCommand timeout update requires literal go test arguments without shell expansion")
		}
		args[i] = value
	}
	return args, nil
}

func literalShellWord(word *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			value, ok := literalDoubleQuoted(p)
			if !ok {
				return "", false
			}
			b.WriteString(value)
		default:
			return "", false
		}
	}
	return b.String(), true
}

func literalDoubleQuoted(quoted *syntax.DblQuoted) (string, bool) {
	if quoted.Dollar {
		return "", false
	}
	var b strings.Builder
	for _, part := range quoted.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			return "", false
		}
		b.WriteString(lit.Value)
	}
	return b.String(), true
}

func stripGoTestTimeout(args []string) ([]string, string, error) {
	base := make([]string, 0, len(args))
	var timeout string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-timeout" || arg == "--timeout" {
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("go test %s requires a duration", arg)
			}
			if timeout != "" {
				return nil, "", fmt.Errorf("go test command contains multiple timeout flags")
			}
			timeout = args[i+1]
			i++
			continue
		}
		if value, ok := inlineGoTestTimeout(arg); ok {
			if timeout != "" {
				return nil, "", fmt.Errorf("go test command contains multiple timeout flags")
			}
			timeout = value
			continue
		}
		base = append(base, arg)
	}
	return base, timeout, nil
}

func inlineGoTestTimeout(arg string) (string, bool) {
	for _, prefix := range []string{"-timeout=", "--timeout="} {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix), true
		}
	}
	return "", false
}
