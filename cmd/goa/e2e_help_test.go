//go:build (darwin || linux) && e2e

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runGoa runs the built binary non-interactively, with a throwaway HOME so the
// run cannot see or touch the developer's configuration.
func runGoa(t *testing.T, binary string, timeout time.Duration, args ...string) (string, int) {
	t.Helper()
	home := setupTestHome(t)
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "HOME="+home, "GOA_HOME="+home)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("goa %v did not exit within %s — help must never start a session", args, timeout)
	}
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("goa %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return string(out), code
}

// TestGoaE2E_HelpIsCompleteAndDeterministic is the regression test for the bug
// that started this: `goa --help` wrote its usage through the asynchronous
// stderr tee and let the flag package call os.Exit(0), so the output was
// truncated mid-drain — a different prefix on every run (6 of 63 options in one
// observation, all of them in another) and nothing at all on a slow terminal.
//
// The test asserts all three properties at once: complete, byte-stable across
// runs, and free of any terminal control sequence (i.e. no TUI was started).
func TestGoaE2E_HelpIsCompleteAndDeterministic(t *testing.T) {
	binary := buildTestBinary(t)
	defer os.Remove(binary)

	var first string
	for i := 0; i < 5; i++ {
		out, code := runGoa(t, binary, 15*time.Second, "--help")
		if code != 0 {
			t.Fatalf("run %d: exit = %d, want 0\n%s", i, code, out)
		}
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("run %d differs from run 0 (len %d vs %d) — help output is still being truncated",
				i, len(out), len(first))
		}
	}

	for _, want := range []string{
		"Command-Line Manual", // the manual itself, not a bare flag dump
		"## Command-line options",
		"## Documentation",
		"goa server",    // the web UI is part of the documentation
		"goa mcp",       // so is the MCP CLI
		"--server-addr", // server options
		"--insecure-no-auth",
		"--prompt", // headless mode
		"--orchestrate",
		"GOA_HOME", // configuration layers
		"WEBUI",    // documentation index
	} {
		if !strings.Contains(first, want) {
			t.Errorf("`goa --help` does not document %q", want)
		}
	}

	// Every registered option must be present: count the rendered option lines.
	if n := strings.Count(first, "\n  --"); n < 60 {
		t.Errorf("`goa --help` rendered %d option lines, want >= 60 (all registered flags)", n)
	}

	for _, banned := range []string{"\x1b[", "\x1b]"} {
		if strings.Contains(first, banned) {
			t.Errorf("help output contains the terminal control sequence %q — a TUI was started", banned)
		}
	}
}

// helpSurfaceCase is one non-interactive invocation and its expectations.
type helpSurfaceCase struct {
	name     string
	args     []string
	wantCode int
	want     []string
}

// helpSurfaceCases enumerates the help surface beyond the `--help` flag: the
// `help` verb, topic lookup, verb-scoped help, and the diagnoses that used to be
// silence — `goa serve` (a plausible typo for `goa server`) started a TUI that
// did nothing visible, and a positional prompt was dropped entirely.
func helpSurfaceCases() []helpSurfaceCase {
	return []helpSurfaceCase{
		{"help verb", []string{"help"}, 0, []string{"Command-Line Manual", "--server-addr"}},
		{"doc topic", []string{"help", "webui"}, 0, []string{"goa server", "127.0.0.1:8080"}},
		{"unit topic", []string{"help", "server"}, 0, []string{"goa server", "--server-read-only"}},
		{"options topic", []string{"help", "options"}, 0, []string{"--model", "--server-auth-token"}},
		{"verb help", []string{"server", "--help"}, 0, []string{"goa server", "--server-auth"}},
		{"mcp help", []string{"mcp", "help"}, 0, []string{"goa mcp add", "goa mcp list"}},
		{"unknown topic", []string{"help", "nosuchtopic"}, 2, []string{"unknown help topic", "available topics"}},
		{"unknown verb", []string{"serve"}, 2, []string{"unknown command", "did you mean: goa server"}},
		{"positional prompt", []string{"fix the bug"}, 2, []string{"unknown command", "--prompt"}},
	}
}

// TestGoaE2E_HelpVerbAndTopics drives each help invocation end to end.
func TestGoaE2E_HelpVerbAndTopics(t *testing.T) {
	binary := buildTestBinary(t)
	defer os.Remove(binary)

	for _, tt := range helpSurfaceCases() {
		t.Run(tt.name, func(t *testing.T) {
			out, code := runGoa(t, binary, 15*time.Second, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d\n%s", code, tt.wantCode, out)
			}
			assertMentions(t, out, tt.want)
			assertNoTUIArtifacts(t, out)
		})
	}
}

// assertMentions reports every wanted fragment missing from output.
func assertMentions(t *testing.T, output string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(output, w) {
			t.Errorf("output does not mention %q\n%s", w, output)
		}
	}
}

// assertNoTUIArtifacts fails when output carries terminal control sequences —
// the signature of a session (TUI or web UI) having been started.
func assertNoTUIArtifacts(t *testing.T, output string) {
	t.Helper()
	for _, banned := range []string{"\x1b[", "\x1b]"} {
		if strings.Contains(output, banned) {
			t.Errorf("output contains %q — a session was started\n%.200q", banned, output)
		}
	}
}

// TestGoaE2E_UsageErrorIsFlushed checks the other print-then-exit paths a user
// hits by typo: the message must arrive in full (not raced away by the tee) and
// the program must not fall through into a session.
func TestGoaE2E_UsageErrorIsFlushed(t *testing.T) {
	binary := buildTestBinary(t)
	defer os.Remove(binary)

	for i := 0; i < 3; i++ {
		out, code := runGoa(t, binary, 15*time.Second, "--nosuchflag")
		if code != 2 {
			t.Fatalf("exit = %d, want 2\n%s", code, out)
		}
		if !strings.Contains(out, "flag provided but not defined") {
			t.Errorf("missing the parser error: %q", out)
		}
		if !strings.Contains(out, "goa --help") {
			t.Errorf("usage error does not point at the manual: %q", out)
		}
	}
}
