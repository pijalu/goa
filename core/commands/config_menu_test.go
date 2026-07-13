// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
)

type selectRecorder struct {
	title   string
	options []tui.SelectorItem
	current string
	onSel   func(string, bool)
}

type inputRecorder struct {
	prompt  string
	current string
	onSub   func(string, bool)
}

func newMenuTestContext(t *testing.T, cfg *config.Config) (*core.Context, *selectRecorder, *inputRecorder, *event.Bus) {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	// Isolate from real home config: CascadeLoader uses os.UserHomeDir() at construction
	// time, so we must override HOME before creating the loader.
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir) // Windows compat
	sr := &selectRecorder{}
	ir := &inputRecorder{}
	tuiEvents := event.MakeBus(32, 32, 32, 32)
	ctx := &core.Context{
		Config:          cfg,
		ConfigSaver:     config.NewCascadeLoader(t.TempDir(), "", nil),
		ProviderManager: newTestProviderManager(),
		EventBus:        tuiEvents,
		LoopDetector:    core.NewLoopDetector(core.DefaultLoopDetectorConfig()),
	}
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		sr.title = title
		sr.options = options
		sr.current = current
		sr.onSel = onSelected
	}
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		ir.prompt = prompt
		ir.current = current
		ir.onSub = onSubmit
	}
	return ctx, sr, ir, tuiEvents
}

func TestConfigMenu_RootShowsItems(t *testing.T) {
	cfg := &config.Config{
		Mode:           config.ModeConfig{Default: internal.ModeState{Major: internal.MajorCoder}},
		ActiveModel:    "llama3",
		ActiveProvider: "local",
		Execution:      config.ExecutionConfig{Mode: "yolo"},
		TUI:            config.TUIConfig{Theme: "dark"},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()

	if sr.title != "Settings:" {
		t.Errorf("title = %q, want Settings:", sr.title)
	}
	want := []string{"profile", "model", "provider", "models", "mode", "compression", "theme", "spinner", "thinking_level", "thinking_blocks", "show_thinking", "multi_agent", "orchestrator", "tools", "sandbox", "loop_detection", "skills", "goals"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d root items, got %d", len(want), len(sr.options))
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
}

func TestConfigMenu_ModeChangeSyncsMode(t *testing.T) {
	cfg := &config.Config{Mode: config.ModeConfig{Default: internal.ModeState{Major: internal.MajorCoder}}}
	ctx, sr, _, events := newMenuTestContext(t, cfg)
	ctx.AgentManager = core.NewAgentManager(cfg, nil, nil, core.NewSessionState(internal.ModeState{Major: internal.MajorCoder}), events, "")

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("profile", true)

	if sr.title != "Select mode:" {
		t.Fatalf("expected mode selector, got %q", sr.title)
	}
	sr.onSel("planner", true)

	if cfg.Mode.Default.Major != internal.MajorPlanner {
		t.Errorf("Mode.Default.Major = %q, want planner", cfg.Mode.Default.Major)
	}
	select {
	case msg := <-events.Footer:
		if msg.ModeChange == nil {
			t.Errorf("expected mode change event, got %+v", msg)
		}
	default:
		t.Errorf("expected footer event, but channel empty")
	}
}

func TestConfigMenu_ModelSelectionListsConfiguredModelsAndOther(t *testing.T) {
	cfg := &config.Config{
		ActiveModel: "m2",
		Models: []config.ModelConfig{
			{ID: "m1", Model: "model-one"},
			{ID: "m2", Model: "model-two"},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("model", true)

	if sr.title != "Select active model:" {
		t.Fatalf("expected model selector, got %q", sr.title)
	}
	if len(sr.options) != 3 {
		t.Fatalf("expected 3 options (2 models + other), got %d: %+v", len(sr.options), sr.options)
	}
	if sr.options[2].Value != "__other__" {
		t.Errorf("last option = %q, want __other__", sr.options[2].Value)
	}
}

func TestConfigMenu_ModelOtherPicksProviderThenModel(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "local",
		ActiveModel:    "old",
		Providers: []config.ProviderConfig{
			{ID: "local", Name: "Local"},
			{ID: "openai", Name: "OpenAI"},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("model", true)
	sr.onSel("__other__", true)

	if sr.title != "Select provider:" {
		t.Fatalf("expected provider selector, got %q", sr.title)
	}
	sr.onSel("local", true)

	if sr.title != "Select model:" {
		t.Fatalf("expected model list selector, got %q", sr.title)
	}
	sr.onSel("qwen3-5-9b", true)

	if cfg.ActiveModel != "qwen3-5-9b" {
		t.Errorf("ActiveModel = %q, want qwen3-5-9b", cfg.ActiveModel)
	}
}

func TestConfigMenu_ModelOtherSkipsProviderWhenOnlyOne(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "local",
		ActiveModel:    "old",
		Providers:      []config.ProviderConfig{{ID: "local", Name: "Local"}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("model", true)
	sr.onSel("__other__", true)

	if sr.title != "Select model:" {
		t.Fatalf("expected model list selector, got %q", sr.title)
	}
	sr.onSel("llama3", true)

	if cfg.ActiveModel != "llama3" {
		t.Errorf("ActiveModel = %q, want llama3", cfg.ActiveModel)
	}
}

func TestConfigMenu_ProviderChangeRequiresModelSelection(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "local",
		Providers: []config.ProviderConfig{
			{ID: "local", Name: "Local"},
			{ID: "openai", Name: "OpenAI"},
		},
		Models: []config.ModelConfig{
			{ID: "llama3", Model: "llama3", ProviderID: "local"},
			{ID: "gpt4", Model: "gpt-4", ProviderID: "openai"},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("provider", true)

	if sr.title != "Select provider:" {
		t.Fatalf("expected provider selector, got %q", sr.title)
	}
	sr.onSel("openai", true)

	if cfg.ActiveProvider != "openai" {
		t.Errorf("ActiveProvider = %q, want openai", cfg.ActiveProvider)
	}
	if sr.title != "Select model for provider:" {
		t.Fatalf("expected model-for-provider selector, got %q", sr.title)
	}
	sr.onSel("gpt4", true)

	if cfg.ActiveModel != "gpt4" {
		t.Errorf("ActiveModel = %q, want gpt4", cfg.ActiveModel)
	}
}

func TestConfigMenu_ProviderAddShowsWizard(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "local", Name: "Local"}},
	}
	ctx, sr, _, events := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("provider", true)
	sr.onSel("__add__", true)

	// After selecting __add__, the wizard should show a selector with provider presets
	select {
	case msg := <-events.Chat:
		// No flash about the old command — we now show an interactive wizard
		if msg.Flash != nil && strings.Contains(msg.Flash.Text, "/config:add provider") {
			t.Fatalf("expected wizard, not flash hint, got %+v", msg)
		}
	default:
		// The wizard opens a SelectOption internally; no flash expected
	}
}

func TestConfigMenu_ModelsAddFlow(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "local", Name: "Local"}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("models", true)

	if sr.title != "Model manager:" {
		t.Fatalf("expected model manager, got %q", sr.title)
	}
	sr.onSel("__add__", true)

	if sr.title != "Select provider for new model:" {
		t.Fatalf("expected provider selector, got %q", sr.title)
	}
	sr.onSel("local", true)

	if sr.title != "Select model:" {
		t.Fatalf("expected model list selector, got %q", sr.title)
	}
	// The model list contains the mocked provider models plus a custom option.
	sr.onSel("qwen3-5-9b", true)

	if len(cfg.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(cfg.Models))
	}
	if cfg.Models[0].ID != "qwen3-5-9b" {
		t.Errorf("model ID = %q, want qwen3-5-9b", cfg.Models[0].ID)
	}
}

func TestConfigMenu_ThinkingLevelPersists(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("thinking_level", true)

	if sr.title != "Select thinking level:" {
		t.Fatalf("expected thinking level selector, got %q", sr.title)
	}
	sr.onSel("high", true)

	if cfg.ThinkingLevels.MainAgent != "high" {
		t.Errorf("ThinkingLevels.MainAgent = %q, want high", cfg.ThinkingLevels.MainAgent)
	}
}

func TestConfigMenu_ShowThinkingToggle(t *testing.T) {
	cfg := &config.Config{TUI: config.TUIConfig{Transparency: config.TransparencyConfig{ShowThinking: true}}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("show_thinking", true)

	if cfg.TUI.Transparency.ShowThinking {
		t.Error("ShowThinking should be toggled off")
	}
}

func TestConfigMenu_SandboxSubMenu(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("sandbox", true)

	if sr.title != "Sandbox settings:" {
		t.Fatalf("expected sandbox menu, got %q", sr.title)
	}
	want := []string{"bash_complexity", "bash_jail", "bash_max_score", "terminal_sandbox", "bash_blocked", "bash_allowed"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d sandbox options, got %d", len(want), len(sr.options))
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
}

func TestConfigMenu_SandboxToggleComplexity(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("sandbox", true)
	sr.onSel("bash_complexity", true)

	if !cfg.Tools.Bash.EnableComplexityAnalysis {
		t.Error("Tools.Bash.EnableComplexityAnalysis should be toggled on")
	}
}

func TestConfigMenu_MultiAgentSubMenu(t *testing.T) {
	cfg := &config.Config{
		MultiAgent: config.MultiAgentConfig{Enabled: true},
		Providers:  []config.ProviderConfig{{ID: "local", Name: "Local"}},
		Models:     []config.ModelConfig{{ID: "llama3", Model: "llama3"}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("multi_agent", true)

	if sr.title != "Multi-agent settings:" {
		t.Fatalf("expected multi-agent menu, got %q", sr.title)
	}
	if len(sr.options) != 3 {
		t.Fatalf("expected 3 multi-agent options, got %d", len(sr.options))
	}
}

func TestConfigMenu_MultiAgentToggle(t *testing.T) {
	cfg := &config.Config{MultiAgent: config.MultiAgentConfig{Enabled: true}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("multi_agent", true)
	sr.onSel("enabled", true)

	if cfg.MultiAgent.Enabled {
		t.Error("MultiAgent.Enabled should be toggled off")
	}
}

func TestConfigMenu_FooterRefreshOnApply(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, events := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("mode", true)
	sr.onSel("confirm", true)

	select {
	case msg := <-events.Footer:
		if !msg.FooterRefresh {
			t.Errorf("expected footer refresh event, got %+v", msg)
		}
	default:
		t.Fatal("expected footer refresh message after applyConfigSet")
	}
}

func TestConfigMenu_LeafReturnsToRoot(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("thinking_blocks", true)

	if sr.title != "Settings:" {
		t.Errorf("after leaf action title = %q, want Settings:", sr.title)
	}
}

func TestConfigMenu_CancelStaysInMenuHistory(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelConfig{{ID: "m1", Model: "m1"}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("model", true)
	sr.onSel("", false)

	if sr.title != "Settings:" {
		t.Errorf("after cancel title = %q, want Settings:", sr.title)
	}
}

func TestConfiguredProviderItems_OnlyConfigured(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "local", Name: "Local"},
			{ID: "local", Name: "Duplicate"},
			{ID: "", Name: "Empty"},
		},
	}
	items := configuredProviderItems(cfg)
	if len(items) != 1 {
		t.Fatalf("expected 1 provider item, got %d", len(items))
	}
	if items[0].Value != "local" {
		t.Errorf("provider item = %q, want local", items[0].Value)
	}
}

func TestConfigMenu_EscReturnsToPreviousPage(t *testing.T) {
	cfg := &config.Config{
		MultiAgent: config.MultiAgentConfig{Enabled: true},
		Providers:  []config.ProviderConfig{{ID: "local", Name: "Local"}},
		Models:     []config.ModelConfig{{ID: "llama3", Model: "llama3"}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("multi_agent", true)
	sr.onSel("companion_model", true)

	if sr.title != "Select companion model:" {
		t.Fatalf("expected companion model selector, got %q", sr.title)
	}

	// Cancel should return to the multi-agent settings page, not the root.
	sr.onSel("", false)

	if sr.title != "Multi-agent settings:" {
		t.Errorf("after cancel title = %q, want Multi-agent settings:", sr.title)
	}
}

func TestConfigMenu_LoopDetectionToggle(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("loop_detection", true)

	if sr.title != "Loop detection settings:" {
		t.Fatalf("expected loop detection menu, got %q", sr.title)
	}
	want := []string{"think_loop", "tool_loop", "thresholds"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d loop detection items, got %d", len(want), len(sr.options))
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}

	// Thinking-loop detection starts enabled.
	if sr.options[0].Description != "on" {
		t.Errorf("thinking-loop initial label = %q, want on", sr.options[0].Description)
	}
	if ctx.LoopDetector.TempOverride("think") {
		t.Fatal("thinking-loop should start enabled")
	}

	// Toggle thinking-loop off.
	sr.onSel("think_loop", true)

	if !ctx.LoopDetector.TempOverride("think") {
		t.Error("TempOverride(think) should be true (disabled) after toggle")
	}
	if sr.options[0].Description != "off" {
		t.Errorf("thinking-loop after toggle label = %q, want off", sr.options[0].Description)
	}
}

func TestConfigMenu_ModelOtherCancelReturnsToModelSelector(t *testing.T) {
	cfg := &config.Config{
		ActiveModel: "old",
		Providers: []config.ProviderConfig{
			{ID: "local", Name: "Local"},
			{ID: "openai", Name: "OpenAI"},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("model", true)
	sr.onSel("__other__", true)

	if sr.title != "Select provider:" {
		t.Fatalf("expected provider selector, got %q", sr.title)
	}

	// Cancel provider selection should return to the active-model selector.
	sr.onSel("", false)

	if sr.title != "Select active model:" {
		t.Errorf("after cancel title = %q, want Select active model:", sr.title)
	}
}

func TestConfigMenu_ModelsAddReturnsToManager(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "local", Name: "Local"}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("models", true)
	sr.onSel("__add__", true)
	sr.onSel("local", true)
	sr.onSel("llama3", true)

	if sr.title != "Model manager:" {
		t.Errorf("after adding model title = %q, want Model manager:", sr.title)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(cfg.Models))
	}
}

func TestModelManagerItems(t *testing.T) {
	cfg := &config.Config{
		ActiveModel: "m1",
		Models: []config.ModelConfig{
			{ID: "m1", Model: "model-one"},
			{ID: "m2", Model: "model-two"},
		},
	}
	items := modelManagerItems(cfg)
	if len(items) != 6 {
		t.Fatalf("expected 6 items (add, set active, edit/remove per model), got %d", len(items))
	}
	if items[0].Value != "__add__" {
		t.Errorf("first item = %q, want __add__", items[0].Value)
	}
}

func TestConfigMenu_CompressionSubmenu(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          true,
			Strategy:         "micro",
			ThresholdPercent: 80,
			MaxTokens:        8192,
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)

	if sr.title != "Compression settings:" {
		t.Fatalf("title = %q, want Compression settings:", sr.title)
	}
	want := []string{"strategy", "threshold", "max_tokens", "enabled", "on_context_error"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d compression items, got %d", len(want), len(sr.options))
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
}

func TestConfigMenu_CompressionStrategyChange(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          true,
			Strategy:         "micro",
			ThresholdPercent: 80,
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
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
			Enabled:          true,
			ThresholdPercent: 80,
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("compression", true)
	sr.onSel("threshold", true)
	if sr.title != "Trigger threshold (% of max tokens):" {
		t.Fatalf("title = %q", sr.title)
	}
	sr.onSel("50", true)
	if cfg.ContextCompression.ThresholdPercent != 50 {
		t.Errorf("ThresholdPercent = %d, want 50", cfg.ContextCompression.ThresholdPercent)
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
	sr.onSel("max_tokens", true)
	sr.onSel("0", true)
	if cfg.ContextCompression.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0 (auto)", cfg.ContextCompression.MaxTokens)
	}
}

func TestCompressionLabel(t *testing.T) {
	if got := compressionLabel(&config.Config{}); got != "off" {
		t.Errorf("compressionLabel(disabled) = %q, want off", got)
	}
	got := compressionLabel(&config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          true,
			Strategy:         "micro",
			ThresholdPercent: 80,
		},
	})
	if !strings.Contains(got, "micro") || !strings.Contains(got, "80%") {
		t.Errorf("compressionLabel = %q, want substring micro and 80%%", got)
	}
	// Empty strategy falls back to tool_elision for display.
	got = compressionLabel(&config.Config{
		ContextCompression: config.ContextCompressionConfig{Enabled: true, ThresholdPercent: 100},
	})
	if !strings.Contains(got, "tool_elision") {
		t.Errorf("compressionLabel with empty strategy = %q, want tool_elision fallback", got)
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
