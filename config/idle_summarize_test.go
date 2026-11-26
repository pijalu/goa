// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"
)

func idleSummarizeInt(v int) *int { return &v }

func TestDefaultConfig_IdleSummarize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := cfg.ContextCompression.IdleSummarize
	if got.TriggerPercentValue() != 10 {
		t.Errorf("TriggerPercentValue() = %d, want 10", got.TriggerPercentValue())
	}
	if got.DelaySecondsValue() != 240 {
		t.Errorf("DelaySecondsValue() = %d, want 240 (4*60 seconds)", got.DelaySecondsValue())
	}
}

func TestDeepMergeContextCompressionIdleSummarizePreservesExplicitZero(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		IdleSummarize: IdleSummarizeSettings{
			TriggerPercent: idleSummarizeInt(10),
			DelaySeconds:   idleSummarizeInt(240),
		},
	}}
	base.DeepMerge(&Config{ContextCompression: ContextCompressionConfig{
		IdleSummarize: IdleSummarizeSettings{
			TriggerPercent: idleSummarizeInt(0),
			DelaySeconds:   idleSummarizeInt(0),
		},
	}})

	got := base.ContextCompression.IdleSummarize
	if got.TriggerPercentValue() != 0 || got.DelaySecondsValue() != 0 {
		t.Fatalf("explicit zero override = trigger %d, delay %d; want 0, 0", got.TriggerPercentValue(), got.DelaySecondsValue())
	}

	base.DeepMerge(&Config{ContextCompression: ContextCompressionConfig{
		IdleSummarize: IdleSummarizeSettings{TriggerPercent: idleSummarizeInt(25)},
	}})
	got = base.ContextCompression.IdleSummarize
	if got.TriggerPercentValue() != 25 {
		t.Errorf("TriggerPercentValue() = %d after partial override, want 25", got.TriggerPercentValue())
	}
	if got.DelaySecondsValue() != 0 {
		t.Errorf("DelaySecondsValue() = %d after partial override, want preserved explicit 0", got.DelaySecondsValue())
	}
}

func TestIdleSummarizeSettingsDeepCopyDoesNotAliasPointers(t *testing.T) {
	original := &Config{ContextCompression: ContextCompressionConfig{
		IdleSummarize: IdleSummarizeSettings{
			TriggerPercent: idleSummarizeInt(10),
			DelaySeconds:   idleSummarizeInt(240),
		},
	}}
	copied := original.DeepCopy()
	*copied.ContextCompression.IdleSummarize.TriggerPercent = 70
	*copied.ContextCompression.IdleSummarize.DelaySeconds = 0

	if got := original.ContextCompression.IdleSummarize.TriggerPercentValue(); got != 10 {
		t.Errorf("original trigger = %d after editing copy, want 10", got)
	}
	if got := original.ContextCompression.IdleSummarize.DelaySecondsValue(); got != 240 {
		t.Errorf("original delay = %d after editing copy, want 240", got)
	}
}

func TestConfigValidateIdleSummarize(t *testing.T) {
	tests := []struct {
		name    string
		trigger *int
		delay   *int
		wantErr string
	}{
		{name: "defaults inherited"},
		{name: "explicit zeroes valid", trigger: idleSummarizeInt(0), delay: idleSummarizeInt(0)},
		{name: "custom values valid", trigger: idleSummarizeInt(37), delay: idleSummarizeInt(1)},
		{name: "negative trigger rejected", trigger: idleSummarizeInt(-1), wantErr: "idle_summarize.trigger_percent"},
		{name: "trigger over 100 rejected", trigger: idleSummarizeInt(101), wantErr: "idle_summarize.trigger_percent"},
		{name: "negative delay rejected", delay: idleSummarizeInt(-1), wantErr: "idle_summarize.delay_seconds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ContextCompression: ContextCompressionConfig{
				Enabled: boolPtr(true),
				IdleSummarize: IdleSummarizeSettings{
					TriggerPercent: tt.trigger,
					DelaySeconds:   tt.delay,
				},
			}}
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
