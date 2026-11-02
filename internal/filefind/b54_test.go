// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package filefind

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestComplete_FDSlowButWithinBudget pins the B54 contract: a rescue that
// succeeds — but slowly, as a real fd does when the machine is loaded —
// still yields candidates when the search extends the budget, and the same
// slow rescue legitimately returns nothing under the default interactive
// budget. The assertion therefore depends on the budget the search asks
// for, never on ambient machine load.
func TestComplete_FDSlowButWithinBudget(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh available for fd stub")
	}
	root := seedIssue8Tree(t)

	// A fake fd that needs 300 ms — slower than the 250 ms interactive
	// budget a keystroke may never be held hostage by (B49), faster than
	// the load-proof budget a test may ask for (B54).
	slowFD(t, 300*time.Millisecond, "hosts")

	t.Run("default interactive budget caps the slow rescue", func(t *testing.T) {
		got := New(root).WithFD(true).Complete("/etc/zzz")
		if containsPath(got, "/etc/hosts") {
			t.Errorf("default 250ms budget let a 300ms rescue through: %v", paths(got))
		}
	})

	t.Run("extended budget rescues the slow hit absolute", func(t *testing.T) {
		got := New(root).WithFD(true).WithTimeout(5 * time.Second).Complete("/etc/zzz")
		if !containsPath(got, "/etc/hosts") {
			t.Errorf("fd path = %v, want /etc/hosts kept absolute", paths(got))
		}
	})
}

// slowFD installs an fd stub that sleeps before printing its hit: the
// timing behaviour of a loaded machine's rescue path, deterministically.
func slowFD(t *testing.T, delay time.Duration, output string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fd")
	body := "#!/bin/sh\nsleep " + strconv.FormatFloat(delay.Seconds(), 'f', 2, 64) + "\ncat <<'EOF'\n" + output + "\nEOF\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	oldAvail, oldPath := fdAvailable, fdPath
	fdAvailable, fdPath = true, script
	t.Cleanup(func() { fdAvailable, fdPath = oldAvail, oldPath })
}
