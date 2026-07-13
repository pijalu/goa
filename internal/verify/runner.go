// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package verify implements a self-verify / test-remediation loop for Goa's
// autonomous fix workflows. It discovers the project's test framework, runs
// tests, captures structured results, and can repeat the loop with a
// remediator until the tests pass or a maximum attempt count is reached.
package verify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner executes the project's test suite and returns a structured report.
type Runner interface {
	// Run executes tests and returns a Report.
	Run(ctx context.Context) (Report, error)
	// Name identifies the runner for diagnostics.
	Name() string
}

// ArgSetter is implemented by runners whose command line can be customized
// with extra arguments (flags/package patterns) beyond the framework's
// built-in defaults. The verify tool uses it to forward the caller-provided
// args to a discovered runner.
type ArgSetter interface {
	SetArgs(args []string)
}

// Report is the structured output of a test run.
type Report struct {
	// Framework is the detected test framework (go, pytest, jest, etc.).
	Framework string
	// Passed is true when all tests succeeded.
	Passed bool
	// ExitCode is the raw process exit code.
	ExitCode int
	// Stdout and Stderr contain the captured output.
	Stdout string
	Stderr string
	// Failures is a list of extracted failure summaries.
	Failures []Failure
	// DurationMs is the elapsed time in milliseconds.
	DurationMs int64
}

// Failure summarises a single test failure.
type Failure struct {
	// Test is the failing test name or empty if unknown.
	Test string
	// File is the file path associated with the failure, if known.
	File string
	// Line is the line number, if known.
	Line int
	// Message is a short failure summary.
	Message string
	// Raw contains the unparsed failure block.
	Raw string
}

// IsEmpty reports whether the report contains no test output.
func (r Report) IsEmpty() bool {
	return r.Stdout == "" && r.Stderr == "" && len(r.Failures) == 0
}

// Summary returns a concise human-readable status.
func (r Report) Summary() string {
	if r.Passed {
		return fmt.Sprintf("%s tests passed", r.Framework)
	}
	return fmt.Sprintf("%s tests failed (%d failure(s))", r.Framework, len(r.Failures))
}

// GoTestRunner runs `go test` in a directory.
type GoTestRunner struct {
	// Dir is the working directory for the test command.
	Dir string
	// Args are extra arguments appended after `go test`. When non-empty they
	// REPLACE the default target (`./...`), so a caller that passes
	// `[-race ./internal/app/...]` gets exactly `go test -race ./internal/app/...`
	// rather than `go test ./... -race ./internal/app/...`.
	Args []string
}

// Name returns "go".
func (g *GoTestRunner) Name() string { return "go" }

// SetArgs sets the extra arguments.
func (g *GoTestRunner) SetArgs(args []string) { g.Args = args }

// Run executes `go test` and parses the output.
func (g *GoTestRunner) Run(ctx context.Context) (Report, error) {
	dir := g.Dir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return Report{}, fmt.Errorf("verify: cannot get working directory: %w", err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return Report{}, fmt.Errorf("verify: no go.mod in %q", dir)
	}

	args := []string{"test"}
	if len(g.Args) > 0 {
		args = append(args, g.Args...)
	} else {
		args = append(args, "./...", "-timeout", "60s")
	}

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	stdout := string(out)
	report := Report{
		Framework:  "go",
		Passed:     cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0,
		ExitCode:   exitCode(cmd, err),
		Stdout:     stdout,
		Failures:   parseGoFailures(stdout),
		DurationMs: elapsed.Milliseconds(),
	}
	return report, nil
}

func exitCode(cmd *exec.Cmd, err error) int {
	if cmd != nil && cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 0
}

// parseGoFailures extracts failure blocks from `go test` output.
func parseGoFailures(output string) []Failure {
	lines := strings.Split(output, "\n")
	parser := failureParser{lines: lines}
	return parser.parse()
}

type failureParser struct {
	lines    []string
	failures []Failure
	current  Failure
	inFailure bool
}

func (p *failureParser) parse() []Failure {
	for i := 0; i < len(p.lines); i++ {
		line := p.lines[i]
		if p.handleFailureStart(line) {
			continue
		}
		if p.inFailure {
			if p.isFailureEnd(line) {
				p.finalizeFailure()
				if strings.HasPrefix(line, "--- FAIL:") {
					i-- // re-process the new failure marker
				}
				continue
			}
			p.appendFailureLine(line)
		}
	}
	p.finalizeIfOpen()
	return p.failures
}

func (p *failureParser) handleFailureStart(line string) bool {
	if !strings.HasPrefix(line, "--- FAIL: ") {
		return false
	}
	if p.inFailure && p.current.Raw != "" {
		p.failures = append(p.failures, p.current)
	}
	p.current = Failure{Test: extractTestName(line), Message: line, Raw: line + "\n"}
	p.inFailure = true
	return true
}

func extractTestName(line string) string {
	name := strings.TrimPrefix(line, "--- FAIL: ")
	if idx := strings.Index(name, " "); idx > 0 {
		name = name[:idx]
	}
	return name
}

func (p *failureParser) isFailureEnd(line string) bool {
	return strings.HasPrefix(line, "--- PASS:") || strings.HasPrefix(line, "--- FAIL:") ||
		strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "ok  \t") ||
		strings.HasPrefix(line, "?   \t")
}

func (p *failureParser) finalizeFailure() {
	p.failures = append(p.failures, p.current)
	p.current = Failure{}
	p.inFailure = false
}

func (p *failureParser) finalizeIfOpen() {
	if p.inFailure && p.current.Raw != "" {
		p.failures = append(p.failures, p.current)
	}
}

func (p *failureParser) appendFailureLine(line string) {
	p.current.Raw += line + "\n"
	if p.current.Message == "" {
		p.current.Message = line
	}
	if file, lineNo := parseGoFileLine(line); file != "" {
		p.current.File = file
		p.current.Line = lineNo
	}
}

func parseGoFileLine(line string) (string, int) {
	idx := strings.Index(line, ".go:")
	if idx < 0 {
		return "", 0
	}
	start := idx - 1
	for start >= 0 && (line[start] == '/' || line[start] == '.' || line[start] == '_' ||
		(line[start] >= 'a' && line[start] <= 'z') ||
		(line[start] >= 'A' && line[start] <= 'Z') ||
		(line[start] >= '0' && line[start] <= '9')) {
		start--
	}
	start++
	rest := line[idx+4:]
	var lineNo int
	fmt.Sscanf(rest, "%d", &lineNo)
	return line[start : idx+3], lineNo
}

// NewFrameworkRunner returns a fresh runner for a known framework name
// ("go", "npm", "pytest"). It does NOT auto-discover; the caller has already
// determined the framework (e.g. from an explicit command). Returns nil for
// unknown frameworks.
func NewFrameworkRunner(name, dir string) Runner {
	switch name {
	case "go":
		return &GoTestRunner{Dir: dir}
	case "npm":
		return &NPMTestRunner{Dir: dir}
	case "pytest":
		return &PytestRunner{Dir: dir}
	}
	return nil
}

// FrameworkBaseCommand returns the canonical base test command for a known
// framework ("go test", "npm test", "pytest"), or "" for unknown frameworks.
// It is the single source of truth for both runner construction and display.
func FrameworkBaseCommand(name string) string {
	switch name {
	case "go":
		return "go test"
	case "npm":
		return "npm test"
	case "pytest":
		return "pytest"
	}
	return ""
}

// ResolveFramework determines the test framework and the extra arguments to
// pass to its runner from the user inputs (an optional explicit command plus
// optional extra args) and the project directory.
//
//   - With an explicit command whose first token is a known framework, the
//     framework is that token and the extra args are the command's remaining
//     tokens (after the framework's base subcommand, e.g. "test") followed by
//     the user-provided extra args. So "go test -race ./pkg" + ["-count=1"]
//     yields framework "go", extra ["-race", "./pkg", "-count=1"].
//   - With no command, the framework is discovered from dir and the extra args
//     are exactly the user-provided extra args (these used to be silently
//     dropped — the root cause of verify running the whole suite).
//   - With an explicit command whose first token is NOT a known framework,
//     framework is "" and extra is nil, signalling the caller should run the
//     raw command verbatim.
func ResolveFramework(dir, command string, extra []string) (framework string, resolvedExtra []string, err error) {
	command = strings.TrimSpace(command)
	if command == "" {
		r, derr := DiscoverRunner(dir)
		if derr != nil {
			return "", nil, derr
		}
		return r.Name(), extra, nil
	}
	fields := strings.Fields(command)
	name := fields[0]
	if FrameworkBaseCommand(name) == "" {
		return "", nil, nil // arbitrary command
	}
	baseTokens := strings.Fields(FrameworkBaseCommand(name))
	rest := trimTokenPrefix(fields[1:], baseTokens[1:])
	resolvedExtra = append(append([]string{}, rest...), extra...)
	return name, resolvedExtra, nil
}

// trimTokenPrefix removes leading tokens of rest that match prefix
// token-for-token (used to strip a framework's "test" subcommand).
func trimTokenPrefix(rest, prefix []string) []string {
	i := 0
	for i < len(prefix) && i < len(rest) && rest[i] == prefix[i] {
		i++
	}
	return rest[i:]
}

// DisplayCommandLine returns the human-readable command line for a resolved
// framework and extra args (e.g. "go test -race ./pkg"), mirroring what the
// runner will execute. For an unknown framework it returns "".
func DisplayCommandLine(framework string, extra []string) string {
	base := FrameworkBaseCommand(framework)
	if base == "" {
		return ""
	}
	if len(extra) == 0 {
		return base
	}
	return base + " " + strings.Join(extra, " ")
}

// DiscoverRunner selects a Runner implementation for the project in dir.
func DiscoverRunner(dir string) (Runner, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return &GoTestRunner{Dir: dir}, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return &NPMTestRunner{Dir: dir}, nil
	}
	for _, name := range []string{"pyproject.toml", "setup.py", "pytest.ini", "requirements.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return &PytestRunner{Dir: dir}, nil
		}
	}
	return nil, fmt.Errorf("verify: cannot discover test framework in %q", dir)
}

// NPMTestRunner runs `npm test` for JavaScript/TypeScript projects.
type NPMTestRunner struct {
	Dir  string
	Args []string
}

func (n *NPMTestRunner) Name() string { return "npm" }

// SetArgs sets the extra arguments passed after `npm test --`.
func (n *NPMTestRunner) SetArgs(args []string) { n.Args = args }

func (n *NPMTestRunner) Run(ctx context.Context) (Report, error) {
	args := []string{"test"}
	if len(n.Args) > 0 {
		args = append(args, "--")
		args = append(args, n.Args...)
	}
	cmd := exec.CommandContext(ctx, "npm", args...)
	cmd.Dir = n.Dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	stdout := string(out)
	return Report{
		Framework:  "npm",
		Passed:     cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0,
		ExitCode:   exitCode(cmd, err),
		Stdout:     stdout,
		Failures:   parseNPMFailures(stdout),
		DurationMs: elapsed.Milliseconds(),
	}, nil
}

// PytestRunner runs `pytest` for Python projects.
type PytestRunner struct {
	Dir  string
	Args []string
}

func (p *PytestRunner) Name() string { return "pytest" }

// SetArgs sets the extra arguments passed to pytest.
func (p *PytestRunner) SetArgs(args []string) { p.Args = args }

func (p *PytestRunner) Run(ctx context.Context) (Report, error) {
	args := p.Args
	if len(args) == 0 {
		args = []string{"-v"}
	}
	cmd := exec.CommandContext(ctx, "pytest", args...)
	cmd.Dir = p.Dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	stdout := string(out)
	return Report{
		Framework:  "pytest",
		Passed:     cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0,
		ExitCode:   exitCode(cmd, err),
		Stdout:     stdout,
		Failures:   parsePytestFailures(stdout),
		DurationMs: elapsed.Milliseconds(),
	}, nil
}

// parseNPMFailures extracts best-effort failure summaries from npm test output
// (mocha/jest). It is intentionally lenient: partial extraction still gives a
// remediator something to work with.
func parseNPMFailures(output string) []Failure {
	var failures []Failure
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "✕"), strings.Contains(trimmed, "failing"), strings.Contains(trimmed, "failed"):
			if trimmed == "" {
				continue
			}
			failures = append(failures, Failure{Message: trimmed, Raw: line})
		}
	}
	return failures
}

// parsePytestFailures extracts FAILED lines from pytest -v output.
func parsePytestFailures(output string) []Failure {
	var failures []Failure
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "FAILED") {
			continue
		}
		msg := strings.TrimSpace(line)
		failures = append(failures, Failure{Message: msg, Raw: line})
	}
	return failures
}
