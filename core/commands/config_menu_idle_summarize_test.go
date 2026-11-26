// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
)

func TestConfigMenuIdleSummarizeSettings(t *testing.T) {
	trigger, delay := 10, 240
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &trigger, DelaySeconds: &delay},
	}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.settingCompressionIdleTrigger()
	sr.onSel("25", true)
	if got := cfg.ContextCompression.IdleSummarize.TriggerPercentValue(); got != 25 {
		t.Fatalf("trigger after picker = %d, want 25", got)
	}

	menu.settingCompressionIdleDelay()
	sr.onSel("0", true)
	if got := cfg.ContextCompression.IdleSummarize.DelaySecondsValue(); got != 0 {
		t.Fatalf("delay after picker = %d, want explicit zero", got)
	}

	items := menu.compressionAdvancedItems()
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Value] = true
	}
	if !seen["idle_trigger"] || !seen["idle_delay"] {
		t.Fatalf("advanced compression items missing idle settings: %+v", seen)
	}
}
