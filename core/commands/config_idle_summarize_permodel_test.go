// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

func TestApplyConfigSet_PerModelIdleSummarizeZeroAndInherit(t *testing.T) {
	tests := []struct {
		field string
		value func(config.ModelCompressionOverride) *int
	}{
		{field: "idle_summarize.trigger_percent", value: func(ov config.ModelCompressionOverride) *int {
			return ov.IdleSummarize.TriggerPercent
		}},
		{field: "idle_summarize.delay_seconds", value: func(ov config.ModelCompressionOverride) *int {
			return ov.IdleSummarize.DelaySeconds
		}},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			cfg := cfgWithModel("qwen")
			ctx, _, _, _ := newMenuTestContext(t, cfg)
			key := "context_compression.per_model.qwen." + tt.field
			if err := applyConfigSet(*ctx, key, "0"); err != nil {
				t.Fatalf("applyConfigSet zero: %v", err)
			}
			value := tt.value(cfg.ContextCompression.PerModel["qwen"])
			if value == nil || *value != 0 {
				t.Fatalf("%s = %v, want materialized explicit zero", tt.field, value)
			}

			if err := applyConfigSet(*ctx, key, ""); err != nil {
				t.Fatalf("applyConfigSet clear: %v", err)
			}
			value = tt.value(cfg.ContextCompression.PerModel["qwen"])
			if value != nil {
				t.Fatalf("%s after clear = %d, want nil (inherit)", tt.field, *value)
			}
		})
	}
}

func TestConfigMenu_PerModelIdleSummarizeFields(t *testing.T) {
	trigger, delay := 25, 45
	cfg := cfgWithModel("qwen")
	cfg.ContextCompression.PerModel = map[string]config.ModelCompressionOverride{
		"qwen": {IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &trigger, DelaySeconds: &delay}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.settingCompressionPerModelEdit("qwen")
	requireOptionDescription(t, sr.options, "idle_summarize.trigger_percent", "25%")
	requireOptionDescription(t, sr.options, "idle_summarize.delay_seconds", "45s")

	sr.onSel("idle_summarize.trigger_percent", true)
	requireOptions(t, sr.options, perModelInheritValue, "0", "100")
	sr.onSel("0", true)
	ov := cfg.ContextCompression.PerModel["qwen"]
	zero := 0
	requireOptionalInt(t, "trigger picker", ov.IdleSummarize.TriggerPercent, &zero)

	menu.settingCompressionPerModelEdit("qwen")
	sr.onSel("idle_summarize.delay_seconds", true)
	requireOptions(t, sr.options, perModelInheritValue, "0", "240")
	sr.onSel("0", true)
	ov = cfg.ContextCompression.PerModel["qwen"]
	requireOptionalInt(t, "delay picker", ov.IdleSummarize.DelaySeconds, &zero)
}

func TestPerModelIdleSummarizePersistRoundTrip(t *testing.T) {
	cfg := cfgWithModel("qwen")
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	if err := ctx.ConfigSaver.SaveHomeProvidersAndModels(cfg); err != nil {
		t.Fatalf("SaveHomeProvidersAndModels: %v", err)
	}
	for key, value := range map[string]string{
		"context_compression.per_model.qwen.idle_summarize.trigger_percent": "0",
		"context_compression.per_model.qwen.idle_summarize.delay_seconds":   "0",
	} {
		if err := applyConfigSet(*ctx, key, value); err != nil {
			t.Fatalf("applyConfigSet(%s): %v", key, err)
		}
	}
	fresh, err := ctx.ConfigSaver.Reload()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	ov := fresh.ContextCompression.PerModel["qwen"].IdleSummarize
	zero := 0
	requireOptionalInt(t, "reloaded trigger", ov.TriggerPercent, &zero)
	requireOptionalInt(t, "reloaded delay", ov.DelaySeconds, &zero)

	if err := applyConfigSet(*ctx, "context_compression.per_model.qwen.idle_summarize.trigger_percent", ""); err != nil {
		t.Fatalf("clear trigger: %v", err)
	}
	fresh, err = ctx.ConfigSaver.Reload()
	if err != nil {
		t.Fatalf("Reload after clear: %v", err)
	}
	ov = fresh.ContextCompression.PerModel["qwen"].IdleSummarize
	requireOptionalInt(t, "cleared trigger", ov.TriggerPercent, nil)
	requireOptionalInt(t, "independent delay", ov.DelaySeconds, &zero)
}

func requireOptionDescription(t *testing.T, options []tui.SelectorItem, value, description string) {
	t.Helper()
	row := findOption(options, value)
	if row == nil || row.Description != description {
		t.Fatalf("option %s = %+v, want description %q", value, row, description)
	}
}

func requireOptions(t *testing.T, options []tui.SelectorItem, values ...string) {
	t.Helper()
	for _, value := range values {
		if findOption(options, value) == nil {
			t.Errorf("picker missing %q: %v", value, options)
		}
	}
}

func requireOptionalInt(t *testing.T, name string, got, want *int) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s = %d, want nil (inherit)", name, *got)
		}
		return
	}
	if got == nil || *got != *want {
		t.Fatalf("%s = %v, want %d", name, got, *want)
	}
}
