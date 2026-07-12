// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/verify"
)

// VerifyTool runs the project's test suite and returns a structured report.
// It discovers the test framework (go test, npm test, pytest) from the project
// root and reports pass/fail status along with extracted failure summaries.
type VerifyTool struct {
	agentic.BaseTool
	// ProjectDir is the directory in which tests are discovered and run.
	ProjectDir string
	// Runner is an optional explicit runner. When nil, the tool discovers
	// the runner from ProjectDir.
	Runner verify.Runner
}

// verifyInput is the JSON input expected by VerifyTool.
type verifyInput struct {
	Command string `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Timeout int    `json:"timeout_seconds,omitempty"`
}

// Schema returns the tool's metadata and parameter schema.
func (v *VerifyTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "verify",
		Description: "Run the project's test suite and return a structured report with pass/fail status and failure summaries.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Optional explicit test command. If omitted, the tool discovers the framework from go.mod, package.json, or Python files.",
				},
				"args": map[string]any{
					"type": "array",
					"items": map[string]any{"type": "string"},
					"description": "Optional extra arguments to pass to the test command.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Maximum time in seconds to wait for the test command. Defaults to 60.",
					"default":     60,
				},
			},
		},
	}
}

// Execute runs the test suite and returns a formatted report.
func (v *VerifyTool) Execute(input string) (string, error) {
	return v.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the test suite with cancellation support.
func (v *VerifyTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var args verifyInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", fmt.Errorf("verify: parse input: %w", err)
	}

	runner, err := v.resolveRunner(args)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, timeoutForVerify(args.Timeout))
	defer cancel()

	report, err := runner.Run(ctx)
	if err != nil {
		return "", fmt.Errorf("verify: %w", err)
	}
	return formatReport(report), nil
}

func (v *VerifyTool) resolveRunner(args verifyInput) (verify.Runner, error) {
	if v.Runner != nil {
		return v.Runner, nil
	}
	if args.Command != "" {
		return runnerFromCommand(args.Command, args.Args, v.ProjectDir)
	}
	return verify.DiscoverRunner(v.ProjectDir)
}

func runnerFromCommand(command string, args []string, projectDir string) (verify.Runner, error) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil, fmt.Errorf("verify: empty command")
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil, fmt.Errorf("verify: empty command")
	}
	name := fields[0]
	var runnerArgs []string
	if len(fields) > 1 {
		runnerArgs = fields[1:]
	}
	if len(args) > 0 {
		runnerArgs = append(runnerArgs, args...)
	}
	return &genericRunner{
		name:    name,
		args:    runnerArgs,
		dir:     projectDir,
		factory: verifyRunnerFactory(name),
	}, nil
}

// verifyRunnerFactory returns a builder that wraps the named framework with
// explicit arguments, so the tool can honor a custom command while still using
// framework-specific parsing and reporting. The builder takes (dir, args).
func verifyRunnerFactory(name string) func(dir string, args []string) verify.Runner {
	switch name {
	case "go":
		return func(dir string, args []string) verify.Runner {
			return &verify.GoTestRunner{Dir: dir, Args: args}
		}
	case "npm":
		return func(dir string, args []string) verify.Runner {
			return &verify.NPMTestRunner{Dir: dir, Args: args}
		}
	case "pytest":
		return func(dir string, args []string) verify.Runner {
			return &verify.PytestRunner{Dir: dir, Args: args}
		}
	default:
		return func(dir string, args []string) verify.Runner {
			return &genericRunner{name: name, args: args, dir: dir}
		}
	}
}

// genericRunner runs an arbitrary shell-style command and returns a raw report.
type genericRunner struct {
	name    string
	args    []string
	dir     string
	factory func(string, []string) verify.Runner
}

func (g *genericRunner) Name() string { return g.name }

func (g *genericRunner) Run(ctx context.Context) (verify.Report, error) {
	if g.factory != nil {
		return g.factory(g.dir, g.args).Run(ctx)
	}
	return verify.Report{}, fmt.Errorf("verify: unsupported command %q", g.name)
}

func timeoutForVerify(seconds int) time.Duration {
	if seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func formatReport(r verify.Report) string {
	var b strings.Builder
	b.WriteString(r.Summary())
	b.WriteString("\n")
	if r.ExitCode != 0 {
		b.WriteString(fmt.Sprintf("exit code: %d\n", r.ExitCode))
	}
	if len(r.Failures) > 0 {
		b.WriteString("\nFailures:\n")
		for _, f := range r.Failures {
			b.WriteString("- ")
			if f.Test != "" {
				b.WriteString(f.Test)
				b.WriteString(": ")
			}
			b.WriteString(f.Message)
			b.WriteString("\n")
			if f.File != "" {
				b.WriteString(fmt.Sprintf("  %s:%d\n", f.File, f.Line))
			}
		}
	}
	if r.Stdout != "" {
		b.WriteString("\n--- stdout ---\n")
		b.WriteString(r.Stdout)
	}
	if r.Stderr != "" {
		b.WriteString("\n--- stderr ---\n")
		b.WriteString(r.Stderr)
	}
	return b.String()
}

// Ensure VerifyTool implements the required interfaces.
var (
	_ agentic.Tool        = (*VerifyTool)(nil)
	_ agentic.ContextTool = (*VerifyTool)(nil)
)
