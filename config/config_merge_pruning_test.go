// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import "testing"

// TestMergeToolResultPruningEnabled pins the tri-state merge of the
// pre-compaction pruning toggle: only an explicitly set pointer overrides the
// lower layer, and the default (all layers unset) is OFF (bugs.md: no
// pre-pruning at the hard ceiling unless opted in).
func TestMergeToolResultPruningEnabled(t *testing.T) {
	trueP, falseP := true, false

	t.Run("default off when unset everywhere", func(t *testing.T) {
		var s ToolResultPruningSettings
		if s.PruningEnabled() {
			t.Error("PruningEnabled() = true for zero settings, want default off")
		}
	})

	t.Run("higher layer true overrides", func(t *testing.T) {
		var dst ToolResultPruningSettings
		mergeToolResultPruning(&dst, ToolResultPruningSettings{Enabled: &trueP})
		if !dst.PruningEnabled() {
			t.Error("Enabled=true did not merge")
		}
	})

	t.Run("higher layer false overrides lower true", func(t *testing.T) {
		dst := ToolResultPruningSettings{Enabled: &trueP}
		mergeToolResultPruning(&dst, ToolResultPruningSettings{Enabled: &falseP})
		if dst.PruningEnabled() {
			t.Error("Enabled=false did not override lower-layer true")
		}
	})

	t.Run("unset higher layer preserves lower", func(t *testing.T) {
		dst := ToolResultPruningSettings{Enabled: &trueP}
		mergeToolResultPruning(&dst, ToolResultPruningSettings{ThresholdChars: 4096})
		if !dst.PruningEnabled() {
			t.Error("lower-layer Enabled was reset by a layer that set only budgets")
		}
		if dst.ThresholdChars != 4096 {
			t.Errorf("ThresholdChars = %d, want 4096", dst.ThresholdChars)
		}
	})
}
