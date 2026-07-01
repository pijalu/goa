// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
)

var (
	templateVarRe = regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)`)
	dotPathRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
)

// EvalExpression evaluates an expr-lang expression against an environment.
func EvalExpression(input string, env map[string]any) (any, error) {
	program, err := expr.Compile(input, expr.Env(env))
	if err != nil {
		return nil, fmt.Errorf("compile expression %q: %w", input, err)
	}
	return expr.Run(program, env)
}

// ApplyTemplate replaces $var and ${var} placeholders. If the variable is a
// dot-path (e.g. $model.id) and env contains a matching map, it is resolved.
// Unknown variables are left unchanged.
func ApplyTemplate(input string, env map[string]any) string {
	return templateVarRe.ReplaceAllStringFunc(input, func(match string) string {
		key := templateVarRe.ReplaceAllString(match, "$1$2")
		if val, ok := resolveDotPath(env, key); ok {
			return fmt.Sprintf("%v", val)
		}
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
		return match
	})
}

func resolveDotPath(env map[string]any, path string) (any, bool) {
	if !dotPathRe.MatchString(path) {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var current any = env
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// ToString converts a value to string for header/template use.
func ToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}
