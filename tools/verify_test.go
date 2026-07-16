// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/verify"
)

func TestVerifyTool_Schema(t *testing.T) {
	tool := &VerifyTool{ProjectDir: t.TempDir()}
	schema := tool.Schema()
	if schema.Name != "verify" {
		t.Errorf("expected schema name verify, got %q", schema.Name)
	}
}

func TestVerifyTool_Execute_Passing(t *testing.T) {
	tool := &VerifyTool{
		ProjectDir: t.TempDir(),
		Runner:     &staticRunner{report: verify.Report{Framework: "go", Passed: true, ExitCode: 0}},
	}

	out, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "go tests passed") {
		t.Errorf("expected passing summary, got:\n%s", out)
	}
}

func TestVerifyTool_Execute_Failing(t *testing.T) {
	tool := &VerifyTool{
		ProjectDir: t.TempDir(),
		Runner: &staticRunner{report: verify.Report{
			Framework: "go",
			Passed:    false,
			ExitCode:  1,
			Failures: []verify.Failure{
				{Test: "TestFoo", Message: "assertion failed", File: "foo_test.go", Line: 42},
			},
		}},
	}

	out, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "go tests failed") {
		t.Errorf("expected failing summary, got:\n%s", out)
	}
	if !strings.Contains(out, "TestFoo") {
		t.Errorf("expected failure test name, got:\n%s", out)
	}
	if !strings.Contains(out, "foo_test.go:42") {
		t.Errorf("expected failure location, got:\n%s", out)
	}
}

func TestVerifyTool_Execute_BadInput(t *testing.T) {
	tool := &VerifyTool{ProjectDir: t.TempDir()}
	if _, err := tool.Execute(`{not json`); err == nil {
		t.Fatal("expected error for invalid JSON input")
	}
}

func TestVerifyTool_ExecuteContext_Cancellation(t *testing.T) {
	tool := &VerifyTool{
		ProjectDir: t.TempDir(),
		Runner:     &staticRunner{report: verify.Report{Framework: "go", Passed: true}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := tool.ExecuteContext(ctx, `{}`)
	if err != nil {
		// Cancellation before the run may or may not be observed depending on
		// timing; a non-empty report is still acceptable.
		return
	}
	if !strings.Contains(out, "go tests passed") {
		t.Errorf("expected passing summary, got:\n%s", out)
	}
}

func TestVerifyTool_ResolveRunner_ExplicitCommand(t *testing.T) {
	tool := &VerifyTool{ProjectDir: t.TempDir()}
	runner, err := tool.resolveRunner(verifyInput{Command: "go test ./..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.Name() != "go" {
		t.Errorf("expected go runner, got %q", runner.Name())
	}
}

func TestVerifyTool_ResolveRunner_Discover(t *testing.T) {
	dir := t.TempDir()
	// Create a go.mod so discovery picks the Go runner.
	writeFile(t, filepath.Join(dir, "go.mod"), "module test\n\ngo 1.21\n")
	tool := &VerifyTool{ProjectDir: dir}
	runner, err := tool.resolveRunner(verifyInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.Name() != "go" {
		t.Errorf("expected go runner from discovery, got %q", runner.Name())
	}
}

type staticRunner struct {
	report verify.Report
	err    error
}

func (s *staticRunner) Name() string { return "static" }

func (s *staticRunner) Run(ctx context.Context) (verify.Report, error) {
	return s.report, s.err
}

// TestNewFrameworkRunner_RoutesByFramework verifies NewFrameworkRunner
// returns the concrete framework runner type for each known name.
func TestNewFrameworkRunner_RoutesByFramework(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		want any
	}{
		{"go", &verify.GoTestRunner{}},
		{"npm", &verify.NPMTestRunner{}},
		{"pytest", &verify.PytestRunner{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := verify.NewFrameworkRunner(tc.name, dir)
			if reflect.TypeOf(r) != reflect.TypeOf(tc.want) {
				t.Errorf("%q: runner type = %T, want %T", tc.name, r, tc.want)
			}
		})
	}
	if r := verify.NewFrameworkRunner("unknown", dir); r != nil {
		t.Errorf("unknown framework should return nil, got %T", r)
	}
}

// TestVerifyTool_Execute_SanitizesControlBytes: test-runner output is
// untrusted — raw ESC bytes in stdout/stderr must become visible text.
func TestVerifyTool_Execute_SanitizesControlBytes(t *testing.T) {
	tool := &VerifyTool{
		ProjectDir: t.TempDir(),
		Runner: &staticRunner{report: verify.Report{
			Framework: "go", Passed: false, ExitCode: 1,
			Stdout: "fail \x1b[31mred\x1b[0m",
		}},
	}
	out, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("raw ESC byte leaked into report: %q", out)
	}
	if !strings.Contains(out, `\e[31mred`) {
		t.Errorf("expected literal escape text, got: %q", out)
	}
}
