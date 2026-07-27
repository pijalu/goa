// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProcessTerminal_TermLog verifies GOA_TERM_LOG captures the terminal
// output stream to a file (the diagnostic for real-terminal rendering bugs,
// bugs.md Issue 20). termLog.once is package-global, so this is the only
// test that can exercise the capture — keep it that way.
func TestProcessTerminal_TermLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "term.log")
	t.Setenv("GOA_TERM_LOG", path)

	pt := &ProcessTerminal{}
	if _, err := pt.Write([]byte("term-log-marker-1 ")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	pt.WriteString("term-log-marker-2")

	f := termLogWriter()
	if f == nil {
		t.Fatal("term log not opened despite GOA_TERM_LOG")
	}
	_ = f.Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read term log: %v", err)
	}
	if !strings.Contains(string(data), "term-log-marker-1") || !strings.Contains(string(data), "term-log-marker-2") {
		t.Fatalf("term log missing captured output:\n%s", data)
	}
}
