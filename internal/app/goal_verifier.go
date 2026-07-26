// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// goalVerifyTimeout bounds a single verify-command execution. The context
// passed by the caller (currently context.Background from the goal tool —
// see ContextTool note in docs/GOALS.md) cannot cancel it, so the timeout
// is the only stop; keep it short enough to not stall a turn for ages.
const goalVerifyTimeout = 2 * time.Minute

// goalVerifyOutputCap caps the combined output returned to the model so a
// noisy test suite cannot flood the context.
const goalVerifyOutputCap = 4000

// execCommandVerifier implements goal.CommandVerifier by running the
// recorded verify command through the system shell in the project directory.
// Output is sanitized (raw ESC bytes must never reach the model context or
// the TUI) and capped.
type execCommandVerifier struct {
	dir string
}

// newExecCommandVerifier creates a verifier rooted at dir (project root).
func newExecCommandVerifier(dir string) *execCommandVerifier {
	return &execCommandVerifier{dir: dir}
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

// Verify runs the command with a hard timeout. ok is true only when the
// command exits 0 within the timeout.
func (v *execCommandVerifier) Verify(ctx context.Context, command string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, goalVerifyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, verifyShell(), "-c", command)
	if v.dir != "" {
		cmd.Dir = v.dir
	}
	out, err := cmd.CombinedOutput()
	output := ansi.Sanitize(string(out))
	if len(output) > goalVerifyOutputCap {
		output = output[:goalVerifyOutputCap] + "\n[... output truncated ...]"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return output + "\n[verify command timed out]", false
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return output, exitErr.ExitCode() == 0
		}
		// Failed to start at all (missing binary, permissions): report as
		// failure with the error as output so the model can fix the command.
		if output != "" {
			output += "\n"
		}
		return output + "verify command failed to run: " + err.Error(), false
	}
	return output, true
}
