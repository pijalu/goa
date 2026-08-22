// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyntheticRunProducesProfiles(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		cpuFile:      filepath.Join(dir, "cpu.prof"),
		memFile:      filepath.Join(dir, "mem.prof"),
		traceFile:    filepath.Join(dir, "trace.out"),
		duration:     500 * time.Millisecond,
		messageCount: 50,
		termW:        80,
		termH:        24,
		updateRate:   60,
	}

	if err := runSynthetic(cfg); err != nil {
		t.Fatalf("runSynthetic: %v", err)
	}

	for _, p := range []string{cfg.cpuFile, cfg.memFile, cfg.traceFile} {
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("expected profile %s to exist: %v", p, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("expected profile %s to be non-empty", p)
		}
	}
}

func TestRusageHelpersDoNotPanic(t *testing.T) {
	// Regression for vet failure: syscall.Getrusage is undefined on Windows.
	// rusage helpers are now split by build tag (rusage_unix.go / rusage_windows.go)
	// and must compile + run on every GOOS. This test would have caught the
	// vet break by exercising the same abstraction.
	r := rusageNow()
	if r == nil {
		t.Fatal("rusageNow returned nil")
	}
	if got := rusageSeconds(nil); got != 0 {
		t.Errorf("rusageSeconds(nil) = %v, want 0", got)
	}
	if got := rusageSeconds(r); got < 0 {
		t.Errorf("rusageSeconds(rusageNow()) = %v, want >= 0", got)
	}
	if got := processRusage(nil); got == nil {
		t.Error("processRusage(nil) should return non-nil stub")
	}
	if got := rusageSeconds(processRusage(nil)); got != 0 {
		t.Errorf("rusageSeconds(processRusage(nil)) = %v, want 0", got)
	}
}

func TestReportPTYResultsHandlesStubRusage(t *testing.T) {
	// Ensures report path tolerates the Windows stub (zero CPU time) without panic.
	cfg := config{
		cpuFile:    "cpu.prof",
		memFile:    "mem.prof",
		traceFile:  "trace.out",
		ptyLogFile: "pty.log",
	}
	// Should not panic even when childUsage is the Windows stub.
	reportPTYResults(2*time.Second, cfg, &cpuRusage{})
	reportPTYResults(2*time.Second, cfg, processRusage(nil))
}
