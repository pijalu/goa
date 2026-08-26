// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// TestApplyConfigSet_MicroCompactionKeys verifies the micro_compaction block
// (and preserve_recent_turns) is settable via the generic config path — the
// "no hidden configuration key" requirement: these gates change
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

// TestApplyConfigSet_RejectsCrossFieldInvalidThresholds is the regression
// test for the entry "/config saves cross-field-invalid
// configuration": per-key setters only check their own range, so
// applyConfigSet must run the whole-config Validate() before committing.
// A change that breaks the compression threshold ordering
// (soft ≤ trigger ≤ hard) must be reported in-band, left out of the live
// config, and kept out of ~/.goa/config.yaml.
func TestApplyConfigSet_RejectsCrossFieldInvalidThresholds(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.ContextCompressionConfig
		key  string
		bad  string // value that violates the ordering
		good string // in-order value that must still apply and persist
		get  func(*config.Config) int
	}{
		{"soft_above_trigger",
			config.ContextCompressionConfig{Enabled: boolPtr(true), Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 80}},
			"context_compression.thresholds.soft_percent", "85", "75",
			func(c *config.Config) int { return c.ContextCompression.Thresholds.SoftPercent }},
		{"trigger_below_soft",
			config.ContextCompressionConfig{Enabled: boolPtr(true), Thresholds: config.CompressionThresholdsConfig{SoftPercent: 80}},
			"context_compression.thresholds.trigger_percent", "60", "90",
			func(c *config.Config) int { return c.ContextCompression.Thresholds.TriggerPercent }},
		{"hard_below_trigger",
			config.ContextCompressionConfig{Enabled: boolPtr(true), Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 80}},
			"context_compression.thresholds.hard_percent", "70", "95",
			func(c *config.Config) int { return c.ContextCompression.Thresholds.HardPercent }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{ContextCompression: tc.cfg}
			ctx, _, _, bus := newMenuTestContext(t, cfg)
			var buf strings.Builder
			ctx.OutputBuffer = &buf
			homePath := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")

			assertThresholdSetRejected(t, ctx, cfg, &buf, bus, homePath, tc.key, tc.bad, tc.get)
			assertThresholdSetApplied(t, ctx, cfg, &buf, homePath, tc.key, tc.good, tc.get)
		})
	}
}

// assertThresholdSetRejected sets key to an out-of-order value and verifies
// the change is reported in-band AND via the flash channel (internal-command
// output is not echoed to the viewport), left out of the live config, and
// kept out of the persisted home config.
func assertThresholdSetRejected(t *testing.T, ctx *core.Context, cfg *config.Config, buf *strings.Builder, bus *event.Bus, homePath, key, value string, get func(*config.Config) int) {
	t.Helper()
	if err := applyConfigSet(*ctx, key, value); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if got := get(cfg); got != 0 {
		t.Errorf("live config mutated by rejected %s=%s: got %d, want 0", key, value, got)
	}
	out := buf.String()
	if !strings.Contains(out, "Refusing to set "+key) || !strings.Contains(out, "must be ≤") {
		t.Errorf("rejection output should cite the violated ordering invariant, got:\n%s", out)
	}
	if strings.Contains(out, "Set "+key) {
		t.Errorf("rejected change reported as applied, got:\n%s", out)
	}
	// The rejection must also be flashed: /config:set is internal, so its
	// buffer output never reaches the TUI viewport.
	assertRejectionFlash(t, bus, key)
	// The invalid value must not have been persisted to the home config.
	if data, err := os.ReadFile(homePath); err == nil && strings.Contains(string(data), ": "+value) {
		t.Errorf("rejected %s=%s persisted to %s:\n%s", key, value, homePath, data)
	}
}

// assertRejectionFlash verifies the rejection was reported on the chat event
// channel's flash, citing the violated ordering invariant and the key.
func assertRejectionFlash(t *testing.T, bus *event.Bus, key string) {
	t.Helper()
	select {
	case ev := <-bus.Chat:
		if ev.Flash == nil || !strings.Contains(ev.Flash.Text, "must be ≤") || !strings.Contains(ev.Flash.Text, key) {
			t.Errorf("rejection flash missing or wrong: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Error("expected a rejection flash on the chat event channel")
	}
}

// assertThresholdSetApplied is the control: an in-order value still applies
// to the live config and persists to the home config.
func assertThresholdSetApplied(t *testing.T, ctx *core.Context, cfg *config.Config, buf *strings.Builder, homePath, key, value string, get func(*config.Config) int) {
	t.Helper()
	buf.Reset()
	if err := applyConfigSet(*ctx, key, value); err != nil {
		t.Fatalf("applyConfigSet control: %v", err)
	}
	want, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("atoi %q: %v", value, err)
	}
	if got := get(cfg); got != want {
		t.Errorf("valid %s=%s not applied: got %d", key, value, got)
	}
	data, err := os.ReadFile(homePath)
	if err != nil {
		t.Fatalf("valid change not persisted: %v", err)
	}
	if !strings.Contains(string(data), ": "+value) {
		t.Errorf("valid %s=%s missing from %s:\n%s", key, value, homePath, data)
	}
}

// TestApplyConfigSet_RejectsEnableWithInvalidThresholds covers the toggle
// path: a config file carrying out-of-order thresholds while compression is
// disabled loads fine (validation skips disabled compression), but enabling
// compression via /config would activate the invalid thresholds. The toggle
// must be rejected and nothing persisted. This requires the validation
// candidate to be a faithful copy of the live config (DeepCopy must not
// drop the disabled ContextCompression block).
func TestApplyConfigSet_RejectsEnableWithInvalidThresholds(t *testing.T) {
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		Enabled:    boolPtr(false),
		Thresholds: config.CompressionThresholdsConfig{SoftPercent: 85, TriggerPercent: 80},
	}}
	ctx, _, _, bus := newMenuTestContext(t, cfg)
	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := applyConfigSet(*ctx, "context_compression.enabled", "true"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if cfg.ContextCompression.EnabledValue() {
		t.Error("compression enabled despite invalid thresholds")
	}
	out := buf.String()
	if !strings.Contains(out, "soft_percent") {
		t.Errorf("rejection output should cite the violated invariant, got:\n%s", out)
	}
	select {
	case ev := <-bus.Chat:
		if ev.Flash == nil || !strings.Contains(ev.Flash.Text, "soft_percent") {
			t.Errorf("rejection flash missing or wrong: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Error("expected a rejection flash on the chat event channel")
	}
	homePath := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	if data, err := os.ReadFile(homePath); err == nil && strings.Contains(string(data), "enabled: true") {
		t.Errorf("rejected enable persisted to %s:\n%s", homePath, data)
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
	// The micro gates live under Advanced… now.
	sr.onSel("advanced", true)

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
			t.Errorf("advanced compression menu missing %q (got %v)", key, values)
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

	sr.onSel("advanced", true)
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

// --- per-model overrides ------------------------------------------------------

// cfgWithModel returns a config with one configured model so per-model override
// validation (which requires the model ID to exist) passes.
func cfgWithModel(id string) *config.Config {
	return &config.Config{
		Models: []config.ModelConfig{{ID: id, ProviderID: "local", Model: id}},
	}
}

// TestApplyConfigSet_PerModelKeys verifies the dynamic
// context_compression.per_model.<id>.<field> keys set the right override fields.
func TestApplyConfigSet_PerModelKeys(t *testing.T) {
	cases := []struct {
		field string
		value string
		get   func(config.ModelCompressionOverride) any
		want  any
	}{
		{"strategy", "hybrid", func(o config.ModelCompressionOverride) any { return o.Strategy }, "hybrid"},
		{"strategies.soft", "micro", func(o config.ModelCompressionOverride) any { return o.Strategies.Soft }, "micro"},
		{"strategies.hard", "summarize", func(o config.ModelCompressionOverride) any { return o.Strategies.Hard }, "summarize"},
		{"thresholds.soft_percent", "30", func(o config.ModelCompressionOverride) any { return o.Thresholds.SoftPercent }, 30},
		{"thresholds.trigger_percent", "60", func(o config.ModelCompressionOverride) any { return o.Thresholds.TriggerPercent }, 60},
		{"thresholds.hard_percent", "85", func(o config.ModelCompressionOverride) any { return o.Thresholds.HardPercent }, 85},
		{"max_tokens", "8192", func(o config.ModelCompressionOverride) any { return o.MaxTokens }, 8192},
		{"cache_gate", "off", func(o config.ModelCompressionOverride) any { return o.CacheGate }, "off"},
		{"preserve_recent_turns", "6", func(o config.ModelCompressionOverride) any { return o.PreserveRecentTurns }, 6},
	}
	for _, tc := range cases {
		t.Run(tc.field+"="+tc.value, func(t *testing.T) {
			cfg := cfgWithModel("qwen")
			ctx, _, _, _ := newMenuTestContext(t, cfg)
			key := "context_compression.per_model.qwen." + tc.field
			if err := applyConfigSet(*ctx, key, tc.value); err != nil {
				t.Fatalf("applyConfigSet: %v", err)
			}
			ov, ok := cfg.ContextCompression.PerModel["qwen"]
			if !ok {
				t.Fatalf("PerModel[qwen] not created after set %s", key)
			}
			if got := tc.get(ov); got != tc.want {
				t.Errorf("%s => %v, want %v", key, got, tc.want)
			}
		})
	}
}

// TestApplyConfigSet_PerModelRejectsInvalid pins validation: bad strategy,
// out-of-range/bad-step thresholds, soft-layer LLM strategy, and unknown model
// IDs must all be rejected without mutating the live config.
func TestApplyConfigSet_PerModelRejectsInvalid(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"bad strategy", "context_compression.per_model.qwen.strategy", "bogus"},
		{"trigger not 5-step", "context_compression.per_model.qwen.thresholds.trigger_percent", "63"},
		{"hard too high", "context_compression.per_model.qwen.thresholds.hard_percent", "99"},
		{"unknown model", "context_compression.per_model.ghost.strategy", "hybrid"},
		{"unknown field", "context_compression.per_model.qwen.bogus_field", "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cfgWithModel("qwen")
			ctx, _, _, _ := newMenuTestContext(t, cfg)
			if err := applyConfigSet(*ctx, tc.key, tc.value); err != nil {
				t.Fatalf("applyConfigSet: %v", err)
			}
			// Nothing should have been written for the model.
			if _, ok := cfg.ContextCompression.PerModel["qwen"]; ok && tc.name != "unknown field" {
				t.Errorf("%s: PerModel[qwen] was written despite invalid value %+v", tc.key, cfg.ContextCompression.PerModel["qwen"])
			}
			if tc.name == "unknown model" {
				if _, ok := cfg.ContextCompression.PerModel["ghost"]; ok {
					t.Errorf("PerModel[ghost] written for unknown model")
				}
			}
		})
	}
}

// TestApplyConfigSet_PerModelClear verifies an empty value clears a field back
// to inherit (and persists the removal).
func TestApplyConfigSet_PerModelClear(t *testing.T) {
	cfg := cfgWithModel("qwen")
	cfg.ContextCompression.PerModel = map[string]config.ModelCompressionOverride{
		"qwen": {Strategy: "hybrid"},
	}
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	if err := applyConfigSet(*ctx, "context_compression.per_model.qwen.strategy", ""); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if got := cfg.ContextCompression.PerModel["qwen"].Strategy; got != "" {
		t.Errorf("Strategy after clear = %q, want empty (inherit)", got)
	}
}

// TestConfigMenu_CompressionShowsPerModelEntry verifies the top-level
// compression menu exposes the per-model override entry point.
func TestConfigMenu_CompressionShowsPerModelEntry(t *testing.T) {
	cfg := cfgWithModel("qwen")
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompression()
	sr.onSel("advanced", true)
	found := false
	for _, it := range sr.options {
		if it.Value == "per_model" {
			found = true
		}
	}
	if !found {
		t.Fatalf("advanced compression menu missing per_model entry, got %v", sr.options)
	}
}

// TestApplyConfigSet_PerModelEnabledKeys verifies the per-model `enabled`
// tri-state setter (bugs.md 2026-08-26): true/false materialize an explicit
// pointer, empty clears back to inherit.
func TestApplyConfigSet_PerModelEnabledKeys(t *testing.T) {
	cfg := cfgWithModel("qwen")
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	key := "context_compression.per_model.qwen.enabled"

	if err := applyConfigSet(*ctx, key, "true"); err != nil {
		t.Fatalf("applyConfigSet true: %v", err)
	}
	ov := cfg.ContextCompression.PerModel["qwen"]
	if ov.Enabled == nil || *ov.Enabled != true {
		t.Fatalf("Enabled = %v, want explicit true", ov.Enabled)
	}

	if err := applyConfigSet(*ctx, key, "false"); err != nil {
		t.Fatalf("applyConfigSet false: %v", err)
	}
	ov = cfg.ContextCompression.PerModel["qwen"]
	if ov.Enabled == nil || *ov.Enabled != false {
		t.Fatalf("Enabled = %v, want explicit false", ov.Enabled)
	}

	// Empty clears back to inherit (nil).
	if err := applyConfigSet(*ctx, key, ""); err != nil {
		t.Fatalf("applyConfigSet clear: %v", err)
	}
	if ov := cfg.ContextCompression.PerModel["qwen"]; ov.Enabled != nil {
		t.Errorf("Enabled after clear = %v, want nil (inherit)", *ov.Enabled)
	}

	// A non-boolean value is rejected: the override entry (left over from the
	// earlier set/clear steps) keeps its cleared nil Enabled — never
	// materialized by the rejected write.
	if err := applyConfigSet(*ctx, key, "maybe"); err != nil {
		t.Fatalf("applyConfigSet maybe: %v", err)
	}
	if ov := cfg.ContextCompression.PerModel["qwen"]; ov.Enabled != nil {
		t.Errorf("invalid enabled value mutated the override: %+v", ov)
	}
}

// TestConfigMenu_CompressionShowsEnabledTopLevel verifies the `enabled`
// master switch is a first-class row of the compression menu — the user
// must be able to SEE and flip it without entering Advanced (bugs.md
// 2026-08-26: the session's project layer had compression disabled and the
// setting was effectively invisible).
func TestConfigMenu_CompressionShowsEnabledTopLevel(t *testing.T) {
	cfg := cfgWithModel("qwen")
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompression()
	if findOption(sr.options, "enabled") == nil {
		t.Fatalf("top-level compression menu missing enabled row, got %v", sr.options)
	}
}

// TestConfigMenu_PerModelEditorShowsEnabled verifies the per-model override
// editor exposes the enabled tri-state.
func TestConfigMenu_PerModelEditorShowsEnabled(t *testing.T) {
	cfg := cfgWithModel("qwen")
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompressionPerModelEdit("qwen")
	if findOption(sr.options, "enabled") == nil {
		t.Fatalf("per-model editor missing enabled row, got %v", sr.options)
	}
}

// findOption returns the selector item with the given value, or nil.
func findOption(options []tui.SelectorItem, value string) *tui.SelectorItem {
	for i := range options {
		if options[i].Value == value {
			return &options[i]
		}
	}
	return nil
}

// TestConfigMenu_PerModelFlow drives the full per-model flow: open the picker,
// choose a configured model, set the strategy override, then clear it.
func TestConfigMenu_PerModelFlow(t *testing.T) {
	cfg := cfgWithModel("qwen")
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	// Open the per-model model picker: qwen is offered, inheriting global.
	menu.settingCompressionPerModel()
	modelRow := findOption(sr.options, "qwen")
	if modelRow == nil {
		t.Fatalf("model picker missing configured model qwen, got %v", sr.options)
	}
	if modelRow.Description != "inherits global" {
		t.Errorf("model row description = %q, want 'inherits global' before any override", modelRow.Description)
	}

	// Pick the model -> the override editor opens with an inherit strategy row.
	sr.onSel("qwen", true)
	strategyRow := findOption(sr.options, "strategy")
	if strategyRow == nil {
		t.Fatalf("override editor missing strategy row, got %v", sr.options)
	}
	if strategyRow.Description != "inherit" {
		t.Errorf("strategy row = %q, want inherit", strategyRow.Description)
	}

	// Open the strategy selector and pick hybrid.
	sr.onSel("strategy", true)
	if findOption(sr.options, "hybrid") == nil {
		t.Fatalf("strategy selector missing hybrid, got %v", sr.options)
	}
	sr.onSel("hybrid", true)
	if got := cfg.ContextCompression.PerModel["qwen"].Strategy; got != "hybrid" {
		t.Fatalf("Strategy = %q, want hybrid after selection", got)
	}

	// Reopen the editor: the strategy row now shows the override, and clear works.
	menu.settingCompressionPerModelEdit("qwen")
	if got := findOption(sr.options, "strategy").Description; got != "hybrid" {
		t.Errorf("strategy row after set = %q, want hybrid", got)
	}
	if findOption(sr.options, "__clear__") == nil {
		t.Fatalf("override editor missing __clear__ row, got %v", sr.options)
	}
	sr.onSel("__clear__", true)
	if _, ok := cfg.ContextCompression.PerModel["qwen"]; ok {
		t.Errorf("PerModel[qwen] still present after __clear__")
	}
}

// TestPerModelCompression_PersistRoundTrip verifies a per-model override set
// via applyConfigSet survives a save+reload through the real CascadeLoader.
func TestPerModelCompression_PersistRoundTrip(t *testing.T) {
	cfg := cfgWithModel("qwen")
	ctx, _, _, _ := newMenuTestContext(t, cfg) // provides a real CascadeLoader + event bus
	// Persist the model itself first (a per-model override requires the model to
	// be configured); then the override set must survive a reload alongside it.
	if err := ctx.ConfigSaver.SaveHomeProvidersAndModels(cfg); err != nil {
		t.Fatalf("SaveHomeProvidersAndModels: %v", err)
	}
	if err := applyConfigSet(*ctx, "context_compression.per_model.qwen.thresholds.trigger_percent", "55"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	fresh, err := ctx.ConfigSaver.Reload()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	ov, ok := fresh.ContextCompression.PerModel["qwen"]
	if !ok {
		t.Fatalf("PerModel[qwen] missing after reload; home config not persisted")
	}
	if ov.Thresholds.TriggerPercent != 55 {
		t.Errorf("TriggerPercent after reload = %d, want 55", ov.Thresholds.TriggerPercent)
	}
}
