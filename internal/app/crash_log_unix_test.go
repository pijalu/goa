// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTeeStderr verifies that writes to os.Stderr — the path the Go runtime
// uses for fatal errors — are captured into the crash log file, and that the
// cleanup restores fd 2 so later writes are no longer captured. Serial: this
// mutates process-wide fd 2.
func TestTeeStderr(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "tee.log"))
	if err != nil {
		t.Fatal(err)
	}

	cleanup := teeStderr(f)
	fmt.Fprint(os.Stderr, "tee-marker-line-67890\n")
	cleanup() // restores fd 2, drains the pipe, closes f

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tee-marker-line-67890") {
		t.Fatalf("stderr write not captured in crash log:\n%s", data)
	}

	// After cleanup, stderr writes must bypass the (closed) log entirely.
	fmt.Fprint(os.Stderr, "post-cleanup-marker-11111\n")
	data, err = os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "post-cleanup-marker-11111") {
		t.Fatal("stderr write after cleanup still reached the crash log")
	}
}
