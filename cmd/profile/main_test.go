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
