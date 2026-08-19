// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

func TestConfigMenu_CompressionSubmenu(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          boolPtr(true),
			Strategy:         "micro",
			ThresholdPercent: 80,
			MaxTokens:        8192,
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)

	if sr.title != "Compression:" {
		t.Fatalf("title = %q, want Compression:", sr.title)
	}
	// 5 main rows + Advanced… — no derived/read-only rows.
	want := []string{"soft_percent", "soft_method", "hard_percent", "hard_method", "on_error", "advanced"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d compression items, got %d: %+v", len(want), len(sr.options), sr.options)
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
	// Effective values must be visible on the main rows.
	descOf := func(v string) string {
		for _, o := range sr.options {
			if o.Value == v {
				return o.Description
			}
		}
		return ""
	}
	if got := descOf("soft_percent"); got != "0% (disabled)" {
		t.Errorf("soft_percent description = %q, want 0%% (disabled)", got)
	}
	if got := descOf("soft_method"); got != "micro (default)" {
		t.Errorf("soft_method description = %q, want micro (default)", got)
	}
	if got := descOf("hard_method"); got != "summarize (default)" {
		t.Errorf("hard_method description = %q, want summarize (default)", got)
	}
}

func TestConfigMenu_CompressionStrategyChange(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          boolPtr(true),
			Strategy:         "micro",
			ThresholdPercent: 80,
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("advanced", true)
	sr.onSel("strategy", true)
	if sr.title != "Compression strategy:" {
		t.Fatalf("title = %q, want Compression strategy:", sr.title)
	}
	sr.onSel("summarize", true)
	if cfg.ContextCompression.Strategy != "summarize" {
		t.Errorf("Strategy = %q, want summarize", cfg.ContextCompression.Strategy)
	}
}

func TestConfigMenu_CompressionThresholdChange(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled: boolPtr(true),
			// Seed the tiered field (not the legacy ThresholdPercent alias,
			// which would win over Thresholds.TriggerPercent on read-back).
			Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 80},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("advanced", true)
	sr.onSel("threshold", true)
	if sr.title != "Trigger threshold (% of max tokens):" {
		t.Fatalf("title = %q", sr.title)
	}
	sr.onSel("50", true)
	if cfg.ContextCompression.Thresholds.TriggerPercent != 50 {
		t.Errorf("Thresholds.TriggerPercent = %d, want 50", cfg.ContextCompression.Thresholds.TriggerPercent)
	}
}

// TestConfigMenu_CompressionThresholdOptions verifies the trigger threshold menu
// offers 0 (disabled) plus every level from 5% to 100% in 5% increments (the
// opt-in range), and that selecting a value like 30 persists it.
func TestConfigMenu_CompressionThresholdOptions(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:    boolPtr(true),
			Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 80},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("advanced", true)
	sr.onSel("threshold", true)
	if sr.title != "Trigger threshold (% of max tokens):" {
		t.Fatalf("title = %q", sr.title)
	}
	// Expect "0" (disabled) then 5,10,...,100 in order.
	if len(sr.options) != 21 {
		t.Fatalf("expected 21 threshold options (0 + 5..100 step 5), got %d", len(sr.options))
	}
	if sr.options[0].Value != "0" {
		t.Errorf("option[0].Value = %q, want \"0\" (disabled)", sr.options[0].Value)
	}
	for i, opt := range sr.options[1:] {
		want := fmt.Sprintf("%d", 5+i*5)
		if opt.Value != want {
			t.Errorf("option[%d].Value = %q, want %q", i+1, opt.Value, want)
		}
		if opt.Label != want+"%" {
			t.Errorf("option[%d].Label = %q, want %q", i+1, opt.Label, want+"%")
		}
	}
	// Selecting a value (30) must persist it.
	sr.onSel("30", true)
	if cfg.ContextCompression.Thresholds.TriggerPercent != 30 {
		t.Errorf("Thresholds.TriggerPercent = %d, want 30", cfg.ContextCompression.Thresholds.TriggerPercent)
	}
}

func TestConfigMenu_CompressionSoftChange(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(true)},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("soft_percent", true)
	if sr.title != "Soft ceiling (% of max tokens, 0 = disabled):" {
		t.Fatalf("title = %q", sr.title)
	}
	sr.onSel("40", true)
	if cfg.ContextCompression.Thresholds.SoftPercent != 40 {
		t.Errorf("Thresholds.SoftPercent = %d, want 40", cfg.ContextCompression.Thresholds.SoftPercent)
	}
}

func TestConfigMenu_CompressionHardChange(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(true)},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("hard_percent", true)
	if sr.title != "Hard ceiling (% of max tokens, 0 = disabled):" {
		t.Fatalf("title = %q", sr.title)
	}
	sr.onSel("90", true)
	if cfg.ContextCompression.Thresholds.HardPercent != 90 {
		t.Errorf("Thresholds.HardPercent = %d, want 90", cfg.ContextCompression.Thresholds.HardPercent)
	}
}

// TestCompressionTriggerValue_LegacyAliasWins locks the documented backwards-
// compatibility rule: when the deprecated ThresholdPercent alias is set, it
// takes precedence over Thresholds.TriggerPercent (config/config.go).
func TestCompressionTriggerValue_LegacyAliasWins(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			ThresholdPercent: 80,
			Thresholds:       config.CompressionThresholdsConfig{TriggerPercent: 50},
		},
	}
	if got := compressionTriggerValue(cfg); got != 80 {
		t.Errorf("compressionTriggerValue = %d, want 80 (legacy alias wins)", got)
	}
	// Without the legacy alias, the tiered field is used.
	cfg.ContextCompression.ThresholdPercent = 0
	if got := compressionTriggerValue(cfg); got != 50 {
		t.Errorf("compressionTriggerValue = %d, want 50 (tiered field)", got)
	}
}

// TestSetTriggerPercentClearLegacy verifies that explicitly setting the tiered
// trigger_percent clears the deprecated legacy alias so the edit actually takes
// effect (Issue 2): without the clear, a config still carrying
// threshold_percent would keep shadowing the new tiered value on display
// (compressionTriggerValue) and at runtime (resolveAgenticThresholds).
func TestSetTriggerPercentClearLegacy(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          boolPtr(true),
			ThresholdPercent: 80, // stale legacy alias
		},
	}
	if err := setTriggerPercentClearLegacy(cfg, "50"); err != nil {
		t.Fatalf("setTriggerPercentClearLegacy: %v", err)
	}
	if cfg.ContextCompression.Thresholds.TriggerPercent != 50 {
		t.Errorf("Thresholds.TriggerPercent = %d, want 50", cfg.ContextCompression.Thresholds.TriggerPercent)
	}
	if cfg.ContextCompression.ThresholdPercent != 0 {
		t.Errorf("legacy ThresholdPercent = %d, want 0 (cleared so edit takes effect)", cfg.ContextCompression.ThresholdPercent)
	}
	// Display must now reflect the new value, not the stale legacy alias.
	if got := compressionTriggerValue(cfg); got != 50 {
		t.Errorf("compressionTriggerValue = %d, want 50 after edit", got)
	}
	// Range validation still applies.
	if err := setTriggerPercentClearLegacy(cfg, "150"); err == nil {
		t.Errorf("expected range error for 150, got nil")
	}
}

// TestConfigMenu_TriggerEditReflectsWithLegacyAlias is the menu-level regression
// for Issue 2: with a stale legacy alias present, choosing a new trigger
// threshold in the menu must update both the tiered field and the displayed value.
func TestConfigMenu_TriggerEditReflectsWithLegacyAlias(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          boolPtr(true),
			ThresholdPercent: 80, // stale legacy alias that would shadow the edit
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("advanced", true)
	sr.onSel("threshold", true)
	sr.onSel("50", true)

	if cfg.ContextCompression.Thresholds.TriggerPercent != 50 {
		t.Errorf("Thresholds.TriggerPercent = %d, want 50", cfg.ContextCompression.Thresholds.TriggerPercent)
	}
	if cfg.ContextCompression.ThresholdPercent != 0 {
		t.Errorf("legacy ThresholdPercent = %d, want 0 (cleared)", cfg.ContextCompression.ThresholdPercent)
	}
	if got := compressionTriggerValue(cfg); got != 50 {
		t.Errorf("compressionTriggerValue = %d, want 50 (edit reflected, not shadowed)", got)
	}
}

func TestConfigMenu_CompressionThresholdRejectsOutOfRange(t *testing.T) {
	cfg := &config.Config{}
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	prev := cfg.ContextCompression.ThresholdPercent
	if err := applyConfigSet(*ctx, "context_compression.threshold_percent", "150"); err != nil {
		t.Fatalf("applyConfigSet returned err: %v", err)
	}
	if cfg.ContextCompression.ThresholdPercent != prev {
		t.Errorf("ThresholdPercent changed from %d to %d (should be unchanged on invalid)", prev, cfg.ContextCompression.ThresholdPercent)
	}
}

func TestConfigMenu_CompressionMaxTokensAuto(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{MaxTokens: 8192},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("advanced", true)
	sr.onSel("max_tokens", true)
	sr.onSel("0", true)
	if cfg.ContextCompression.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0 (auto)", cfg.ContextCompression.MaxTokens)
	}
}

func TestCompressionLabel(t *testing.T) {
	// The root-menu row is a simple COUNT of enabled mechanisms — no rich
	// preview (too wide for the row) and never a misleading "off" when some
	// compression is actually enabled.
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "explicitly disabled",
			cfg:  &config.Config{ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(false)}},
			want: "disabled",
		},
		{
			name: "nothing enabled",
			cfg:  &config.Config{ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(true)}},
			want: "none active",
		},
		{
			// Micro compaction alone previously rendered "off" although
			// compression was enabled (the bug behind this fix).
			name: "micro only",
			cfg: &config.Config{ContextCompression: config.ContextCompressionConfig{
				Enabled:         boolPtr(true),
				MicroCompaction: config.MicroCompactionSettings{Enabled: boolPtr(true)},
			}},
			want: "1 active",
		},
		{
			name: "hard plus on-error (tuned default)",
			cfg: &config.Config{ContextCompression: config.ContextCompressionConfig{
				Enabled:        boolPtr(true),
				Thresholds:     config.CompressionThresholdsConfig{HardPercent: 95},
				OnContextError: true,
			}},
			want: "2 active",
		},
		{
			name: "all layers plus net plus micro",
			cfg: &config.Config{ContextCompression: config.ContextCompressionConfig{
				Enabled:         boolPtr(true),
				Thresholds:      config.CompressionThresholdsConfig{SoftPercent: 40, TriggerPercent: 80, HardPercent: 95},
				OnContextError:  true,
				MicroCompaction: config.MicroCompactionSettings{Enabled: boolPtr(true)},
			}},
			want: "5 active",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compressionLabel(tt.cfg)
			if got != tt.want {
				t.Errorf("compressionLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMultiAgentLabel_CompanionModeShowsOn(t *testing.T) {
	cfg := &config.Config{}
	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)

	if got := multiAgentLabel(cfg, orch); got != "off" {
		t.Errorf("multiAgentLabel = %q, want off", got)
	}

	orch.SetMode(multiagent.WorkflowAgentDriven)
	if got := multiAgentLabel(cfg, orch); got != "on" {
		t.Errorf("multiAgentLabel(agent-driven) = %q, want on", got)
	}

	orch.SetMode(multiagent.WorkflowCompanionMinor)
	if got := multiAgentLabel(cfg, orch); got != "on" {
		t.Errorf("multiAgentLabel(framework) = %q, want on", got)
	}
}

// TestConfigMenu_LoopThresholdsShowEffectiveValues verifies the Loop detection
// → Thresholds menu shows the effective value used (with a "(default)"
// annotation) instead of the bare word "default" when the config value is 0.
func TestConfigMenu_LoopThresholdsShowEffectiveValues(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("loop_detection", true)
	sr.onSel("thresholds", true)

	if sr.title != "Loop threshold settings:" {
		t.Fatalf("title = %q, want Loop threshold settings:", sr.title)
	}
	want := map[string]string{
		"loop_warning":                "3 (default)",
		"loop_interrupt":              "5 (default)",
		"tool_repeat_total":           "off",
		"tool_repeat_consecutive":     "2 (default)",
		"max_tool_calls":              "3 (default)",
		"max_consecutive_tool_rounds": "15 (default)",
		"disable_tool_budget":         "off",
		"stream_repeats":              "5 (default)",
		"stream_min_period":           "50 (default)",
		"stream_strikes":              "3 (default)",
		"stream_reset_after":          "10 (default)",
	}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d threshold options, got %d", len(want), len(sr.options))
	}
	for _, opt := range sr.options {
		got, ok := want[opt.Value]
		if !ok {
			t.Errorf("unexpected option %q", opt.Value)
			continue
		}
		if opt.Description != got {
			t.Errorf("option %q description = %q, want %q", opt.Value, opt.Description, got)
		}
	}
}

// TestConfigMenu_LoopThresholdsShowConfiguredValues verifies explicitly
// configured thresholds are shown verbatim.
func TestConfigMenu_LoopThresholdsShowConfiguredValues(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{
			LoopWarning:              7,
			LoopInterrupt:            9,
			MaxToolRepeatTotal:       12,
			MaxToolRepeatConsecutive: 4,
			MaxToolCalls:             15,
			StreamLoopMaxRepeats:     8,
			StreamLoopMinPeriod:      72,
			StreamLoopMaxStrikes:     6,
			StreamLoopResetAfter:     20,
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("loop_detection", true)
	sr.onSel("thresholds", true)

	want := map[string]string{
		"loop_warning":            "7",
		"loop_interrupt":          "9",
		"tool_repeat_total":       "12",
		"tool_repeat_consecutive": "4",
		"max_tool_calls":          "15",
		"stream_repeats":          "8",
		"stream_min_period":       "72",
		"stream_strikes":          "6",
		"stream_reset_after":      "20",
	}
	for _, opt := range sr.options {
		if got, ok := want[opt.Value]; ok && opt.Description != got {
			t.Errorf("option %q description = %q, want %q", opt.Value, opt.Description, got)
		}
	}
}

func TestThresholdLabel(t *testing.T) {
	tests := []struct {
		name string
		v    int
		def  int
		want string
	}{
		{name: "unset uses default", v: 0, def: 5, want: "5 (default)"},
		{name: "negative uses default", v: -1, def: 3, want: "3 (default)"},
		{name: "configured value", v: 8, def: 5, want: "8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := thresholdLabel(tt.v, tt.def); got != tt.want {
				t.Errorf("thresholdLabel(%d, %d) = %q, want %q", tt.v, tt.def, got, tt.want)
			}
		})
	}
}

func TestDisabledThresholdLabel(t *testing.T) {
	tests := []struct {
		name string
		v    int
		want string
	}{
		{name: "zero means off", v: 0, want: "off"},
		{name: "negative means off", v: -3, want: "off"},
		{name: "configured value", v: 12, want: "12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := disabledThresholdLabel(tt.v); got != tt.want {
				t.Errorf("disabledThresholdLabel(%d) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}

// TestConfigMenu_OrchestratorSaveIsSectionScoped verifies the orchestrator
// settings flow persists only the orchestrator section to ~/.goa/config.yaml
// (Save full-dump baked merged project/embedded values into the home
// file). A pre-existing unrelated home key must survive untouched, and
// unrelated merged state (e.g. skills from the embedded layer) must not leak
// into the home file.
func TestConfigMenu_OrchestratorSaveIsSectionScoped(t *testing.T) {
	cfg := &config.Config{
		Orchestrator: config.OrchestratorConfig{
			Roles: map[string]config.OrchestratorRole{"worker": {Model: "qwen"}},
		},
		// Merged state that must NOT leak into the home file on an
		// orchestrator-section save.
		Skills: config.SkillsConfig{ExecutionMode: "subagent"},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	// newMenuTestContext points HOME at an isolated temp dir; seed the home
	// config there with a pre-existing unrelated key.
	homePath := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	writeTestConfig(t, homePath, "skills:\n  execution_mode: inline\n")

	menu := newConfigMenu(*ctx)
	menu.openOrchestratorDefaults()
	sr.onSel(config.OrchestratorTopologyFanout, true)

	data := readTestFile(t, homePath)
	if !strings.Contains(data, "topology: fanout") {
		t.Errorf("orchestrator defaults not persisted, got:\n%s", data)
	}
	if !strings.Contains(data, "execution_mode: inline") {
		t.Errorf("pre-existing home skills key was clobbered, got:\n%s", data)
	}
	if strings.Contains(data, "subagent") {
		t.Errorf("merged in-memory skills state leaked into the home file, got:\n%s", data)
	}
	if strings.Contains(data, "active_provider") || strings.Contains(data, "providers:") {
		t.Errorf("unrelated merged sections leaked into the home file, got:\n%s", data)
	}
}

// TestConfigMenu_ModelSaveIsFieldScoped verifies the model-manager flow
// (add/edit/remove model) persists via SaveHomeProvidersAndModels — provider
// and model fields update in the home file, but unrelated merged state is
// not dumped (Save full-dump contamination).
func TestConfigMenu_ModelSaveIsFieldScoped(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "p1",
		Providers:      []config.ProviderConfig{{ID: "p1", Endpoint: "http://p1.example.com/v1"}},
		Models:         []config.ModelConfig{{ID: "m1", ProviderID: "p1", Model: "m1"}},
		// Merged state that must not leak.
		Skills: config.SkillsConfig{ExecutionMode: "subagent"},
	}
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	homePath := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	writeTestConfig(t, homePath, `active_provider: p1
providers:
  - id: p1
    endpoint: http://p1.example.com/v1
models:
  - id: m1
    provider: p1
    model: m1
skills:
  execution_mode: inline
`)

	menu := newConfigMenu(*ctx)
	menu.addModel("p1", "m2", "m2")

	data := readTestFile(t, homePath)
	if !strings.Contains(data, "id: m2") {
		t.Errorf("added model not persisted, got:\n%s", data)
	}
	if !strings.Contains(data, "execution_mode: inline") {
		t.Errorf("pre-existing home skills key was clobbered, got:\n%s", data)
	}
	if strings.Contains(data, "subagent") {
		t.Errorf("merged in-memory skills state leaked into the home file, got:\n%s", data)
	}
}
