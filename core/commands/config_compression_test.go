// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
)

// TestApplyConfigSet_MicroCompactionKeys verifies the micro_compaction block
// (and preserve_recent_turns) is settable via the generic config path — the
// "no hidden configuration key" requirement (bugs.md): these gates change
// runtime behavior (the 2026-08-02 session compaction fired on
// min_context_ratio) so they must not be YAML-only.
func TestApplyConfigSet_MicroCompactionKeys(t *testing.T) {
	cases := []struct {
		key   string
		value string
		get   func(*config.Config) any
		want  any
	}{
		{"context_compression.preserve_recent_turns", "8",
			func(c *config.Config) any { return c.ContextCompression.PreserveRecentTurns }, 8},
		{"context_compression.micro_compaction.keep_recent_messages", "30",
			func(c *config.Config) any { return c.ContextCompression.MicroCompaction.KeepRecentMessages }, 30},
		{"context_compression.micro_compaction.min_content_tokens", "500",
			func(c *config.Config) any { return c.ContextCompression.MicroCompaction.MinContentTokens }, 500},
		{"context_compression.micro_compaction.min_context_ratio", "0.7",
			func(c *config.Config) any { return c.ContextCompression.MicroCompaction.MinContextRatio }, 0.7},
		{"context_compression.micro_compaction.cache_miss_threshold", "30m",
			func(c *config.Config) any { return c.ContextCompression.MicroCompaction.CacheMissThreshold }, "30m"},
		{"context_compression.micro_compaction.cache_miss_threshold", "0",
			func(c *config.Config) any { return c.ContextCompression.MicroCompaction.CacheMissThreshold }, "0"},
		{"context_compression.micro_compaction.truncated_marker", "[[elided]]",
			func(c *config.Config) any { return c.ContextCompression.MicroCompaction.TruncatedMarker }, "[[elided]]"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			cfg := &config.Config{}
			ctx, _, _, _ := newMenuTestContext(t, cfg)
			if err := applyConfigSet(*ctx, tc.key, tc.value); err != nil {
				t.Fatalf("applyConfigSet: %v", err)
			}
			if got := tc.get(cfg); got != tc.want {
				t.Errorf("%s = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// TestApplyConfigSet_MicroCompactionRejectsInvalid pins validation: out-of-range
// ratios, unparseable durations, and negative counts must leave the config
// untouched (applyConfigSet reports in-band and returns nil).
func TestApplyConfigSet_MicroCompactionRejectsInvalid(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"context_compression.micro_compaction.min_context_ratio", "1.5"},
		{"context_compression.micro_compaction.min_context_ratio", "-0.5"},
		{"context_compression.micro_compaction.min_context_ratio", "abc"},
		{"context_compression.micro_compaction.cache_miss_threshold", "soon"},
		{"context_compression.micro_compaction.keep_recent_messages", "-5"},
		{"context_compression.micro_compaction.min_content_tokens", "-1"},
		{"context_compression.preserve_recent_turns", "-2"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			cfg := &config.Config{}
			ctx, _, _, _ := newMenuTestContext(t, cfg)
			if err := applyConfigSet(*ctx, tc.key, tc.value); err != nil {
				t.Fatalf("applyConfigSet: %v", err)
			}
			if cfg.ContextCompression.MicroCompaction != (config.MicroCompactionSettings{}) {
				t.Errorf("MicroCompaction mutated to %+v on invalid %s=%s", cfg.ContextCompression.MicroCompaction, tc.key, tc.value)
			}
			if cfg.ContextCompression.PreserveRecentTurns != 0 {
				t.Errorf("PreserveRecentTurns mutated to %d on invalid %s=%s", cfg.ContextCompression.PreserveRecentTurns, tc.key, tc.value)
			}
		})
	}
}

// TestConfigMenu_CompressionShowsMicroGateKeys is the regression test for the
// hidden-gate complaint: the /config → Compression menu must list the micro
// compaction's own gates (min_context_ratio, cache_miss_threshold) and the
// remaining micro knobs, each with its effective value visible.
func TestConfigMenu_CompressionShowsMicroGateKeys(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			PreserveRecentTurns: 4,
			MicroCompaction: config.MicroCompactionSettings{
				MinContextRatio: 0.7,
			},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompression()

	values := make(map[string]string, len(sr.options))
	for _, it := range sr.options {
		values[it.Value] = it.Description
	}

	for _, key := range []string{
		"micro_min_context_ratio",
		"micro_cache_miss_threshold",
		"micro_keep_recent_messages",
		"micro_min_content_tokens",
		"micro_truncated_marker",
		"preserve_recent_turns",
	} {
		if _, ok := values[key]; !ok {
			t.Errorf("compression menu missing %q (got %v)", key, values)
		}
	}
	// Effective values must be visible: configured 0.7 shows as 70%, an unset
	// gate shows its default.
	if got := values["micro_min_context_ratio"]; got != "70%" {
		t.Errorf("micro_min_context_ratio description = %q, want %q", got, "70%")
	}
	if got := values["micro_cache_miss_threshold"]; got != "1h (default)" {
		t.Errorf("micro_cache_miss_threshold description = %q, want %q", got, "1h (default)")
	}
}

// TestConfigMenu_MicroRatioSelectionApplies drives the menu end-to-end: open
// the ratio selector, pick 0.6, and the config updates.
func TestConfigMenu_MicroRatioSelectionApplies(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompression()

	sr.onSel("micro_min_context_ratio", true)
	// The recorder now holds the ratio selector's state.
	found := false
	for _, it := range sr.options {
		if it.Value == "0.6" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ratio selector did not open with expected options, got %v", sr.options)
	}
	sr.onSel("0.6", true)
	if got := cfg.ContextCompression.MicroCompaction.MinContextRatio; got != 0.6 {
		t.Errorf("MinContextRatio = %v, want 0.6 after menu selection", got)
	}
}
