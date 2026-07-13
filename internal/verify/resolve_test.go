// SPDX-License-Identifier: GPL-3.0-or-later

package verify

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// TestResolveFramework_NoCommandAppliesExtraArgs is the regression test for
// the silent arg-drop bug: when no command is supplied (auto-discover), the
// user's extra args MUST be forwarded to the discovered runner instead of
// being discarded. Previously verify ran the whole suite (`go test ./...`)
// even when the caller asked for a single package.
func TestResolveFramework_NoCommandAppliesExtraArgs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module test\n\ngo 1.21\n")

	framework, extra, err := ResolveFramework(dir, "", []string{"-race", "./internal/app/..."})
	if err != nil {
		t.Fatalf("ResolveFramework: %v", err)
	}
	if framework != "go" {
		t.Fatalf("framework = %q, want go", framework)
	}
	if !reflect.DeepEqual(extra, []string{"-race", "./internal/app/..."}) {
		t.Fatalf("extra args dropped = %v, want [-race ./internal/app/...]", extra)
	}
}

// TestResolveFramework_ExplicitCommandStripsBaseSubcommand verifies that an
// explicit command like "go test -race ./pkg" resolves to framework "go" with
// extra args ["-race", "./pkg"] (the "go test" base is stripped so the runner
// does not double the "test" subcommand).
func TestResolveFramework_ExplicitCommandStripsBaseSubcommand(t *testing.T) {
	framework, extra, err := ResolveFramework(t.TempDir(), "go test -race ./pkg", []string{"-count=1"})
	if err != nil {
		t.Fatalf("ResolveFramework: %v", err)
	}
	if framework != "go" {
		t.Fatalf("framework = %q, want go", framework)
	}
	want := []string{"-race", "./pkg", "-count=1"}
	if !reflect.DeepEqual(extra, want) {
		t.Fatalf("extra = %v, want %v", extra, want)
	}
}

// TestResolveFramework_UnknownCommandReturnsEmpty verifies an arbitrary
// command (not a known framework) signals raw execution.
func TestResolveFramework_UnknownCommandReturnsEmpty(t *testing.T) {
	framework, extra, err := ResolveFramework(t.TempDir(), "make test", nil)
	if err != nil {
		t.Fatalf("ResolveFramework: %v", err)
	}
	if framework != "" {
		t.Fatalf("framework = %q, want \"\" for unknown command", framework)
	}
	if extra != nil {
		t.Fatalf("extra = %v, want nil", extra)
	}
}

// TestDisplayCommandLine_MatchesRunner verifies the display string the TUI
// shows matches what the framework runner executes.
func TestDisplayCommandLine(t *testing.T) {
	cases := []struct {
		framework string
		extra     []string
		want      string
	}{
		{"go", []string{"-race", "./pkg"}, "go test -race ./pkg"},
		{"go", nil, "go test"},
		{"npm", []string{"--", "foo"}, "npm test -- foo"},
		{"pytest", []string{"-x"}, "pytest -x"},
		{"make", []string{"test"}, ""},
	}
	for _, tc := range cases {
		if got := DisplayCommandLine(tc.framework, tc.extra); got != tc.want {
			t.Errorf("DisplayCommandLine(%q,%v) = %q, want %q", tc.framework, tc.extra, got, tc.want)
		}
	}
}

// TestGoTestRunner_SetArgsReplacesDefaultTarget verifies the runner honors
// SetArgs: with args it runs exactly `go test <args>` (no `./...` prepended).
func TestGoTestRunner_SetArgsReplacesDefaultTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module test\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "foo_test.go"), "package test\nimport \"testing\"\nfunc TestFoo(t *testing.T) {}\n")

	r := &GoTestRunner{Dir: dir}
	r.SetArgs([]string{"-race", "."})
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !report.Passed {
		t.Fatalf("expected pass, exit=%d: %s", report.ExitCode, report.Stdout)
	}
}
