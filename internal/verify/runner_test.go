// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoTestRunner_Passes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module testmod\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "foo_test.go"), `package testmod

import "testing"

func TestFoo(t *testing.T) {}
`)

	runner := &GoTestRunner{Dir: dir, Args: []string{"-timeout", "30s"}}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !report.Passed {
		t.Errorf("expected tests to pass, got exit code %d: %s", report.ExitCode, report.Stdout)
	}
	if report.Framework != "go" {
		t.Errorf("expected framework go, got %q", report.Framework)
	}
}

func TestGoTestRunner_Fails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module testmod\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "fail_test.go"), `package testmod

import "testing"

func TestFail(t *testing.T) {
	t.Fatal("boom")
}
`)

	runner := &GoTestRunner{Dir: dir, Args: []string{"-timeout", "30s"}}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if report.Passed {
		t.Error("expected tests to fail")
	}
	if len(report.Failures) == 0 {
		t.Errorf("expected failures, got: %s", report.Stdout)
	}
	if report.Failures[0].Test != "TestFail" {
		t.Errorf("expected TestFail failure, got %q", report.Failures[0].Test)
	}
}

func TestGoTestRunner_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	runner := &GoTestRunner{Dir: dir}
	_, err := runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for missing go.mod")
	}
	if !strings.Contains(err.Error(), "no go.mod") {
		t.Errorf("expected no go.mod error, got: %v", err)
	}
}

func TestParseGoFailures(t *testing.T) {
	output := `--- FAIL: TestSomething (0.00s)
    runner_test.go:42: expected true, got false
--- PASS: TestOther (0.00s)
FAIL
exit status 1
FAIL	pkg	0.1s`
	failures := parseGoFailures(output)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failures))
	}
	f := failures[0]
	if f.Test != "TestSomething" {
		t.Errorf("expected TestSomething, got %q", f.Test)
	}
	if !strings.Contains(f.File, "runner_test.go") {
		t.Errorf("expected file runner_test.go, got %q", f.File)
	}
	if f.Line != 42 {
		t.Errorf("expected line 42, got %d", f.Line)
	}
}

func TestParseGoFileLine(t *testing.T) {
	file, line := parseGoFileLine("/path/to/file.go:123 +0x45")
	if file != "/path/to/file.go" {
		t.Errorf("expected /path/to/file.go, got %q", file)
	}
	if line != 123 {
		t.Errorf("expected line 123, got %d", line)
	}
}

func TestDiscoverRunner_Go(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	runner, err := DiscoverRunner(dir)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if runner.Name() != "go" {
		t.Errorf("expected go runner, got %q", runner.Name())
	}
}

func TestDiscoverRunner_NPM(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{}`)
	runner, err := DiscoverRunner(dir)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if runner.Name() != "npm" {
		t.Errorf("expected npm runner, got %q", runner.Name())
	}
}

func TestDiscoverRunner_Pytest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pyproject.toml"), ``)
	runner, err := DiscoverRunner(dir)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if runner.Name() != "pytest" {
		t.Errorf("expected pytest runner, got %q", runner.Name())
	}
}

func TestDiscoverRunner_Unknown(t *testing.T) {
	dir := t.TempDir()
	_, err := DiscoverRunner(dir)
	if err == nil {
		t.Fatal("expected error for unknown framework")
	}
}

func TestReport_Summary(t *testing.T) {
	if got := (Report{Framework: "go", Passed: true}).Summary(); got != "go tests passed" {
		t.Errorf("summary = %q", got)
	}
	if got := (Report{Framework: "go", Passed: false, Failures: []Failure{{}, {}}}).Summary(); got != "go tests failed (2 failure(s))" {
		t.Errorf("summary = %q", got)
	}
}

func TestReport_IsEmpty(t *testing.T) {
	if !(Report{}).IsEmpty() {
		t.Error("empty report should be empty")
	}
	if (Report{Stdout: "x"}).IsEmpty() {
		t.Error("report with stdout should not be empty")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGoTestRunner_DefaultArgs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module testmod\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "foo_test.go"), `package testmod

import "testing"

func TestFoo(t *testing.T) {}
`)

	runner := &GoTestRunner{Dir: dir}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !report.Passed {
		t.Errorf("expected tests to pass, got exit code %d: %s", report.ExitCode, report.Stdout)
	}
}

func TestDiscoverRunner_SetupPy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), "")
	runner, err := DiscoverRunner(dir)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if runner.Name() != "pytest" {
		t.Errorf("expected pytest runner, got %q", runner.Name())
	}
}

func TestDiscoverRunner_PytestIni(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pytest.ini"), "")
	runner, err := DiscoverRunner(dir)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if runner.Name() != "pytest" {
		t.Errorf("expected pytest runner, got %q", runner.Name())
	}
}

func TestNPMTestRunner_Name(t *testing.T) {
	r := &NPMTestRunner{}
	if r.Name() != "npm" {
		t.Errorf("expected npm, got %q", r.Name())
	}
}

func TestPytestRunner_Name(t *testing.T) {
	r := &PytestRunner{}
	if r.Name() != "pytest" {
		t.Errorf("expected pytest, got %q", r.Name())
	}
}

func TestParseGoFailures_NoOutput(t *testing.T) {
	if failures := parseGoFailures(""); len(failures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(failures))
	}
}

func TestParseGoFailures_Multiple(t *testing.T) {
	output := `--- FAIL: TestA (0.00s)
    a_test.go:1: fail a
--- FAIL: TestB (0.00s)
    b_test.go:2: fail b
FAIL
exit status 1
`
	failures := parseGoFailures(output)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(failures))
	}
	if failures[0].Test != "TestA" || failures[1].Test != "TestB" {
		t.Errorf("unexpected test names: %v", failures)
	}
}

func TestExitCode_ExitError(t *testing.T) {
	cmd := exec.Command("false")
	_ = cmd.Run()
	if got := exitCode(cmd, nil); got != 1 {
		t.Errorf("exitCode = %d, want 1", got)
	}
}

func TestNPMTestRunner_Run(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"x"}`)
	r := &NPMTestRunner{Dir: dir}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	// npm test fails without a test script, but the runner should still report.
	if report.Framework != "npm" {
		t.Errorf("expected framework npm, got %q", report.Framework)
	}
}

func TestPytestRunner_Run(t *testing.T) {
	dir := t.TempDir()
	// Create a fake pytest binary that prints a passing test and exits 0.
	bindir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bindir, 0755); err != nil {
		t.Fatal(err)
	}
	fakePytest := filepath.Join(bindir, "pytest")
	script := `#!/bin/sh
if echo "$*" | grep -q -- "--version"; then
  echo "pytest 0.0.0"
  exit 0
fi
echo "test_x.py::test_x PASSED"
exit 0
`
	if err := os.WriteFile(fakePytest, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", bindir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	r := &PytestRunner{Dir: dir}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !report.Passed {
		t.Errorf("expected fake pytest to pass, got exit code %d: %s", report.ExitCode, report.Stdout)
	}
	if report.Framework != "pytest" {
		t.Errorf("expected framework pytest, got %q", report.Framework)
	}
}

func TestDiscoverRunner_RequirementsTxt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "requirements.txt"), "pytest")
	runner, err := DiscoverRunner(dir)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if runner.Name() != "pytest" {
		t.Errorf("expected pytest runner, got %q", runner.Name())
	}
}

func TestExitCode_NilCmd(t *testing.T) {
	if got := exitCode(nil, nil); got != 0 {
		t.Errorf("exitCode(nil, nil) = %d, want 0", got)
	}
}

func TestExitCode_ExitErrorValue(t *testing.T) {
	err := &exec.ExitError{}
	// ExitError.ExitCode is available but we can also use ProcessState if set.
	if got := exitCode(nil, err); got != 0 {
		t.Logf("exitCode for bare ExitError returned %d", got)
	}
}

func TestParseGoFailures_TailingFailure(t *testing.T) {
	output := "--- FAIL: TestTail (0.00s)\n    tail_test.go:99: tail failure"
	failures := parseGoFailures(output)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failures))
	}
	if failures[0].Test != "TestTail" {
		t.Errorf("expected TestTail, got %q", failures[0].Test)
	}
}

// TestGoTestRunner_DurationIsWallClock verifies DurationMs reflects elapsed
// wall-clock time, not CPU user time (F2).
func TestGoTestRunner_DurationIsWallClock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module durltest\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dur_test.go"), []byte("package durltest\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSleep(t *testing.T) { time.Sleep(120 * time.Millisecond) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &GoTestRunner{Dir: dir, Args: []string{"-timeout", "30s"}}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !report.Passed {
		t.Fatalf("expected pass, got report: %+v", report)
	}
	// A 120ms sleep should produce >= 100ms of wall-clock duration; CPU user
	// time for a sleeping test is near zero, which is what this guards against.
	if report.DurationMs < 100 {
		t.Errorf("DurationMs = %d, want >= 100 (wall clock)", report.DurationMs)
	}
}

func TestParseNPMFailures(t *testing.T) {
	output := "  ✓ works\n  ✕ broken thing\n  2 passing\n  1 failing\n"
	failures := parseNPMFailures(output)
	if len(failures) < 2 {
		t.Fatalf("expected at least 2 failures, got %d: %v", len(failures), failures)
	}
}

func TestParsePytestFailures(t *testing.T) {
	output := "test_a.py::test_ok PASSED\nservice/test_x.py::test_bad FAILED\n"
	failures := parsePytestFailures(output)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0].Message, "test_bad") {
		t.Errorf("failure message = %q", failures[0].Message)
	}
}
