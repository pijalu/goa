// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
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
		Description: "Run the test suite, return pass/fail report.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "test command (optional, auto-detected from go.mod/package.json)",
				},
				"args": map[string]any{
					"type": "array",
					"items": map[string]any{"type": "string"},
					"description": "extra args for test command",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "max time in seconds (default: 60)",
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
	framework, extra, err := verify.ResolveFramework(v.ProjectDir, args.Command, args.Args)
	if err != nil {
		return nil, err
	}
	// Unknown / arbitrary command: run it verbatim as a raw shell command so the
	// model can verify with any runner (make, cargo, ...), not just the three
	// with structured parsing.
	if framework == "" {
		return newRawCommandRunner(args.Command, args.Args, v.ProjectDir), nil
	}
	runner := verify.NewFrameworkRunner(framework, v.ProjectDir)
	if runner == nil {
		return newRawCommandRunner(args.Command, args.Args, v.ProjectDir), nil
	}
	if as, ok := runner.(verify.ArgSetter); ok && len(extra) > 0 {
		as.SetArgs(extra)
	}
	return runner, nil
}

// rawCommandRunner executes an arbitrary command line and returns a raw
// report (no structured failure parsing) so verify supports custom runners.
type rawCommandRunner struct {
	command string
	args    []string
	dir     string
}

func newRawCommandRunner(command string, args []string, dir string) *rawCommandRunner {
	return &rawCommandRunner{command: command, args: args, dir: dir}
}

func (r *rawCommandRunner) Name() string { return "command" }

func (r *rawCommandRunner) Run(ctx context.Context) (verify.Report, error) {
	fields := strings.Fields(strings.TrimSpace(r.command))
	if len(fields) == 0 {
		return verify.Report{}, fmt.Errorf("verify: empty command")
	}
	args := append(append([]string{}, fields[1:]...), r.args...)
	cmd := exec.CommandContext(ctx, fields[0], args...)
	if r.dir != "" {
		cmd.Dir = r.dir
	}
	out, err := cmd.CombinedOutput()
	report := verify.Report{
		Framework: "command",
		Stdout:    string(out),
	}
	if cmd.ProcessState != nil {
		report.ExitCode = cmd.ProcessState.ExitCode()
		report.Passed = report.ExitCode == 0
	} else if err != nil {
		return report, fmt.Errorf("verify: %w", err)
	}
	return report, nil
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
		// Sanitize: command output is untrusted — raw ESC bytes must never
		// reach the model context or the TUI renderer.
		b.WriteString(ansi.Sanitize(r.Stdout))
	}
	if r.Stderr != "" {
		b.WriteString("\n--- stderr ---\n")
		b.WriteString(ansi.Sanitize(r.Stderr))
	}
	return b.String()
}

// Ensure VerifyTool implements the required interfaces.
var (
	_ agentic.Tool        = (*VerifyTool)(nil)
	_ agentic.ContextTool = (*VerifyTool)(nil)
)
