// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunEcho(t *testing.T) {
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = "cmd"
	}
	opts := RunOpts{
		Cmd:       []string{shell, "-c", "echo hello"},
		MaxOutput: 1000,
	}
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Fatalf("output %q does not contain hello", res.Output)
	}
}

func TestRunTimeout(t *testing.T) {
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = "cmd"
	}
	opts := RunOpts{
		Cmd:       []string{shell, "-c", "sleep 10"},
		Timeout:   50 * time.Millisecond,
		MaxOutput: 1000,
	}
	start := time.Now()
	res, _ := Run(opts)
	elapsed := time.Since(start)
	if !res.TimedOut {
		t.Fatal("TimedOut should be true")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestRunCancel(t *testing.T) {
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = "cmd"
	}
	ctx, cancel := context.WithCancel(context.Background())
	opts := RunOpts{
		Cmd:       []string{shell, "-c", "sleep 10"},
		Cancel:    ctx,
		MaxOutput: 1000,
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	res, _ := Run(opts)
	if res.TimedOut {
		t.Fatal("TimedOut should be false for cancellation")
	}
}

func TestTruncateOutput(t *testing.T) {
	in := strings.Repeat("a", 100)
	got := truncateOutput(in, 50)
	if !strings.HasSuffix(got, "... (truncated, 100 chars total)") {
		t.Fatalf("unexpected truncation suffix: %q", got)
	}
}
