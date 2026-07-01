// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"
)

// TestNewAgent_AppliesDefaultMicroCompactionConfig verifies I2: when
// Strategy==CompressionMicro and MicroCompaction is left at zero, NewAgent
// applies DefaultMicroCompactionConfig so the documented keep/recent/threshold
// defaults actually take effect (previously they were silently never applied).
func TestNewAgent_AppliesDefaultMicroCompactionConfig(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 8000,
			Strategy:  CompressionMicro,
			// MicroCompaction intentionally zero — defaults should apply.
		},
	})

	got := agent.cfg.ContextCompression.MicroCompaction
	if got.KeepRecentMessages != DefaultMicroCompactionConfig.KeepRecentMessages {
		t.Errorf("KeepRecentMessages = %d, want default %d",
			got.KeepRecentMessages, DefaultMicroCompactionConfig.KeepRecentMessages)
	}
	if got.MinContextRatio != DefaultMicroCompactionConfig.MinContextRatio {
		t.Errorf("MinContextRatio = %v, want default %v",
			got.MinContextRatio, DefaultMicroCompactionConfig.MinContextRatio)
	}
	if got.TruncatedMarker != DefaultMicroCompactionConfig.TruncatedMarker {
		t.Errorf("TruncatedMarker = %q, want default %q",
			got.TruncatedMarker, DefaultMicroCompactionConfig.TruncatedMarker)
	}
}

// TestNewAgent_PreservesExplicitMicroCompactionConfig verifies that an
// explicitly-provided MicroCompaction is NOT overwritten by the default.
func TestNewAgent_PreservesExplicitMicroCompactionConfig(t *testing.T) {
	explicit := MicroCompactionConfig{
		KeepRecentMessages: 7,
		MinContentTokens:   42,
		MinContextRatio:    0.25,
		TruncatedMarker:    "[custom]",
	}
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:       8000,
			Strategy:        CompressionMicro,
			MicroCompaction: explicit,
		},
	})

	got := agent.cfg.ContextCompression.MicroCompaction
	if got != explicit {
		t.Errorf("explicit MicroCompaction overwritten: got %+v, want %+v", got, explicit)
	}
}

// TestNewAgent_NoDefaultForNonMicroStrategy verifies the default is only
// applied for the micro strategy.
func TestNewAgent_NoDefaultForNonMicroStrategy(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 8000,
			Strategy:  CompressionToolElision,
		},
	})

	got := agent.cfg.ContextCompression.MicroCompaction
	if got != (MicroCompactionConfig{}) {
		t.Errorf("MicroCompaction unexpectedly populated for non-micro strategy: %+v", got)
	}
}
