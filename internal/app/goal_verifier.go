// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
)

// defaultGoalVerifyTimeout bounds a single verify-command execution when the
// user has not configured goals.verify_timeout. The context passed by the
// caller (currently context.Background from the goal tool — see ContextTool
// note in docs/GOALS.md) cannot cancel it, so the timeout is the only stop;
// keep it short enough to not stall a turn for ages.
const defaultGoalVerifyTimeout = 2 * time.Minute

// goalVerifyOutputCap caps the combined output returned to the model so a
// noisy test suite cannot flood the context.
const goalVerifyOutputCap = 4000

// execCommandVerifier implements goal.CommandVerifier by running the
// recorded verify command through the system shell in the project directory.
// Output is sanitized (raw ESC bytes must never reach the model context or
// the TUI) and capped. The timeout is explicit and configurable
// (goals.verify_timeout, Bug A) and reported in the outcome so the
// UI can display it.
type execCommandVerifier struct {
	dir     string
	timeout time.Duration
}

// newExecCommandVerifier creates a verifier rooted at dir (project root).
// A zero/negative timeout selects defaultGoalVerifyTimeout.
func newExecCommandVerifier(dir string, timeout time.Duration) *execCommandVerifier {
	if timeout <= 0 {
		timeout = defaultGoalVerifyTimeout
	}
	return &execCommandVerifier{dir: dir, timeout: timeout}
}

// verifyShell mirrors the bash tool's shell selection ($SHELL, bash fallback)
// so verify commands run in the same environment the model expects.
func verifyShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return shell
}

// Verify runs the command with a hard timeout. OK is true only when the
// command exits 0 within the timeout.
func (v *execCommandVerifier) Verify(ctx context.Context, command string) goal.VerifyOutcome {
	start := time.Now()
	outcome := goal.VerifyOutcome{TimeoutMs: v.timeout.Milliseconds()}

	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, verifyShell(), "-c", command)
	if v.dir != "" {
		cmd.Dir = v.dir
	}
	out, err := cmd.CombinedOutput()
	outcome.DurationMs = time.Since(start).Milliseconds()
	output := ansi.Sanitize(string(out))
	if len(output) > goalVerifyOutputCap {
		output = output[:goalVerifyOutputCap] + "\n[... output truncated ...]"
	}
	if ctx.Err() == context.DeadlineExceeded {
		outcome.Output = output + "\n[verify command timed out]"
		return outcome
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			outcome.Output = output
			outcome.OK = exitErr.ExitCode() == 0
			return outcome
		}
		// Failed to start at all (missing binary, permissions): report as
		// failure with the error as output so the model can fix the command.
		if output != "" {
			output += "\n"
		}
		outcome.Output = output + "verify command failed to run: " + err.Error()
		return outcome
	}
	outcome.Output = output
	outcome.OK = true
	return outcome
}
