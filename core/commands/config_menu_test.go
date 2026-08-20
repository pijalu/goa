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
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/skills"
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
	want := []string{"profile", "model", "provider", "models", "mode", "compression", "theme", "spinner", "spinner_location", "thinking_level", "thinking_blocks", "show_thinking", "multi_agent", "orchestrator", "teams", "tools", "bash", "mcp", "sandbox", "loop_detection", "skills", "goals"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d root items, got %d", len(want), len(sr.options))
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
}

// TestConfigMenu_SkillsRowIsSubmenuHint verifies the top-level Skills row
// does not read like a binary state toggle ("Skills inline"): it must show a
// neutral submenu hint with per-source on-counts.
func TestConfigMenu_SkillsRowIsSubmenuHint(t *testing.T) {
	cfg := &config.Config{
		Skills: config.SkillsConfig{ExecutionMode: "inline"},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"review":    {Meta: skills.SkillMeta{Name: "review"}, Source: "embedded"},
		"refactor":  {Meta: skills.SkillMeta{Name: "refactor"}, Source: "embedded"},
		"local-one": {Meta: skills.SkillMeta{Name: "local-one"}, Source: "file"},
	})

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()

	var skillsRow *tui.SelectorItem
	for i := range sr.options {
		if sr.options[i].Value == "skills" {
			skillsRow = &sr.options[i]
			break
		}
	}
	if skillsRow == nil {
		t.Fatal("skills row not found in root menu")
	}
	if skillsRow.Description == "inline" || skillsRow.Description == "subagent" {
		t.Errorf("skills row Description = %q (raw execution mode reads like a toggle); want a neutral submenu hint", skillsRow.Description)
	}
	if !strings.Contains(skillsRow.Description, "embedded") || !strings.Contains(skillsRow.Description, "local") {
		t.Errorf("skills row Description = %q, want per-source hint mentioning embedded and local", skillsRow.Description)
	}
}

func TestConfigMenu_ToolsSubmenuAndToolCallFixing(t *testing.T) {
	cfg := &config.Config{Execution: config.ExecutionConfig{AutoHealToolCalls: false}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("tools", true)
	if sr.title != "Tools settings:" {
		t.Fatalf("title = %q, want Tools settings:", sr.title)
	}
	if len(sr.options) != 2 || sr.options[0].Value != "enabled_tools" || sr.options[1].Value != "tool_call_fixing" {
		t.Fatalf("unexpected tools submenu options: %+v", sr.options)
	}
	if sr.options[1].Description != "off" {
		t.Fatalf("tool fixing description = %q, want off", sr.options[1].Description)
	}
	sr.onSel("tool_call_fixing", true)
	if !cfg.Execution.AutoHealToolCalls {
		t.Fatal("tool call fixing was not enabled")
	}
	if sr.title != "Tools settings:" || sr.options[1].Description != "on" {
		t.Fatalf("submenu did not refresh after toggle: title=%q options=%+v", sr.title, sr.options)
	}

}

func TestConfigMenu_ToolSelectorIsNested(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("tools", true)
	sr.onSel("enabled_tools", true)
	if sr.title != "Toggle optional tools:" {
		t.Fatalf("title = %q, want Toggle optional tools:", sr.title)
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
	// Bug B: companion provider+model is ONE row (selected together
	// like /model); the separate companion_provider row is gone.
	if len(sr.options) != 2 {
		t.Fatalf("expected 2 multi-agent options, got %d", len(sr.options))
	}
	if sr.options[0].Value != "companion_model" || sr.options[1].Value != "enabled" {
		t.Errorf("unexpected multi-agent rows: %q, %q", sr.options[0].Value, sr.options[1].Value)
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

// TestConfigMenu_CompanionModelSetsProviderAndModel is Bug B: the
// companion model picker must bind provider+model atomically (like /model)
// instead of exposing a separate provider row that can contradict the model.
func TestConfigMenu_CompanionModelSetsProviderAndModel(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "opencode-go",
		Providers: []config.ProviderConfig{
			{ID: "opencode-go", Endpoint: "https://example.com/go"},
			{ID: "zai", Endpoint: "https://example.com/zai"},
		},
		Models: []config.ModelConfig{
			{ID: "glm", ProviderID: "zai", Model: "glm-5.2"},
			{ID: "ds", ProviderID: "opencode-go", Model: "deepseek-v4-flash"},
		},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	_ = menu.showRoot()
	sr.onSel("multi_agent", true)

	// One combined row: no separate companion_provider row.
	for _, it := range sr.options {
		if it.Value == "companion_provider" {
			t.Fatalf("multi-agent menu still exposes a separate provider row")
		}
	}

	// The model picker shows each model's provider binding.
	sr.onSel("companion_model", true)
	if sr.title != "Select companion model:" {
		t.Fatalf("expected companion model selector, got %q", sr.title)
	}
	providerShown := false
	for _, it := range sr.options {
		if it.Value == "glm" && strings.Contains(it.Description, "zai") {
			providerShown = true
		}
	}
	if !providerShown {
		t.Errorf("companion picker does not show provider bindings, options: %+v", sr.options)
	}

	// Selecting a configured model sets BOTH companion keys.
	sr.onSel("glm", true)
	if cfg.MultiAgent.CompanionModel != "glm" {
		t.Errorf("companion_model = %q, want glm", cfg.MultiAgent.CompanionModel)
	}
	if cfg.MultiAgent.CompanionProvider != "zai" {
		t.Errorf("companion_provider = %q, want zai (bound from model selection)", cfg.MultiAgent.CompanionProvider)
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
	want := []string{"think_loop", "tool_loop", "stream_loop", "thinking_stall", "thresholds"}
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

	// Selecting think_loop offers temp/persistent actions.
	sr.onSel("think_loop", true)
	if sr.title != "Change thinking-loop detection:" {
		t.Fatalf("expected action chooser, got %q", sr.title)
	}
	wantActions := []string{"temp_off", "persist_off"}
	if len(sr.options) != len(wantActions) {
		t.Fatalf("expected %d action items, got %d", len(wantActions), len(sr.options))
	}
	for i, w := range wantActions {
		if sr.options[i].Value != w {
			t.Errorf("action[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}

	// Choose session-only disable.
	sr.onSel("temp_off", true)

	if !ctx.LoopDetector.TempOverride("think") {
		t.Error("TempOverride(think) should be true (disabled) after temp_off")
	}
	if sr.options[0].Description != "off (session)" {
		t.Errorf("thinking-loop after temp_off label = %q, want off (session)", sr.options[0].Description)
	}
}

// TestConfigMenu_LoopDetectionPersistToggle verifies the persistent (saved)
// disable path writes config and flips the live detector.
func TestConfigMenu_LoopDetectionPersistToggle(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("loop_detection", true)
	sr.onSel("think_loop", true)

	// Choose persistent disable.
	sr.onSel("persist_off", true)

	if !ctx.LoopDetector.Disabled("think") {
		t.Error("Disabled(think) should be true after persist_off")
	}
	if ctx.LoopDetector.TempOverride("think") {
		t.Error("persist_off must not set the session temp override")
	}
	if cfg.Execution.DisableThinkingLoopDetection == nil || !*cfg.Execution.DisableThinkingLoopDetection {
		t.Error("config Execution.DisableThinkingLoopDetection not persisted to true")
	}
	if sr.options[0].Description != "off (saved)" {
		t.Errorf("thinking-loop after persist_off label = %q, want off (saved)", sr.options[0].Description)
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
