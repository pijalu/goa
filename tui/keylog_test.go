// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKeyLogger_DisabledByDefault(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	ui := NewTUI(term)
	if ui.keyLog != nil {
		t.Fatalf("key logger should be nil by default")
	}
}

func TestKeyLogger_LogsWhenEnabled(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	ui := NewTUI(term)
	path := filepath.Join(t.TempDir(), "keys.log")

	if err := ui.SetKeyLog(path); err != nil {
		t.Fatalf("SetKeyLog: %v", err)
	}

	ui.handleKey("a")
	if err := ui.keyLog.close(); err != nil {
		t.Fatalf("close key log: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key log: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `raw="a"`) {
		t.Fatalf("expected log to contain raw=\"a\", got:\n%s", got)
	}
}

func TestKeyLogger_CreatesDirectories(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	ui := NewTUI(term)
	path := filepath.Join(t.TempDir(), "nested", "keys.log")

	if err := ui.SetKeyLog(path); err != nil {
		t.Fatalf("SetKeyLog: %v", err)
	}
	if err := ui.keyLog.close(); err != nil {
		t.Fatalf("close key log: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("key log file was not created: %v", err)
	}
}

func TestKeyLogger_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission checks are unix-specific")
	}

	term := &fakeTerminal{w: 80, h: 24}
	ui := NewTUI(term)
	path := filepath.Join(t.TempDir(), "keys.log")

	if err := ui.SetKeyLog(path); err != nil {
		t.Fatalf("SetKeyLog: %v", err)
	}
	if err := ui.keyLog.close(); err != nil {
		t.Fatalf("close key log: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key log: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected file mode 0600, got 0%o", perm)
	}
}

func TestKeyLogger_InvalidPath(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	ui := NewTUI(term)
	// Use an invalid directory path: create a file then try to mkdir under it.
	blocking := filepath.Join(t.TempDir(), "blocking")
	if err := os.WriteFile(blocking, []byte("x"), 0600); err != nil {
		t.Fatalf("setup blocking file: %v", err)
	}
	path := filepath.Join(blocking, "keys.log")

	if err := ui.SetKeyLog(path); err == nil {
		t.Fatalf("expected SetKeyLog to fail on invalid path")
	}
}
