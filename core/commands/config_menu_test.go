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
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
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
// neutral submenu hint with per-source on-counts (bugs.md).
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
	// bugs.md Bug B: companion provider+model is ONE row (selected together
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

// TestConfigMenu_CompanionModelSetsProviderAndModel is bugs.md Bug B: the
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

	if sr.title != "Compression settings:" {
		t.Fatalf("title = %q, want Compression settings:", sr.title)
	}
	want := []string{"strategy", "soft_strategy", "hard_strategy", "soft", "threshold", "hard",
		"_derived_eff_hard", "_derived_escalation", "_derived_deferral", "_derived_elision", "_derived_reactive_savings",
		"cache_gate", "max_tokens", "preserve_recent_turns", "micro_min_context_ratio", "micro_cache_miss_threshold", "micro_keep_recent_messages", "micro_min_content_tokens", "micro_truncated_marker", "enabled", "on_context_error"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d compression items, got %d: %+v", len(want), len(sr.options), sr.options)
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
	// CM:13 design rule 5: the derived limits must be VISIBLE in /config (no
	// hidden 95%). With no hard ceiling configured, the effective hard defaults
	// to 95 and the derived levels are computed from it.
	descOf := func(v string) string {
		for _, o := range sr.options {
			if o.Value == v {
				return o.Description
			}
		}
		return ""
	}
	if got := descOf("_derived_eff_hard"); got != "95%" {
		t.Errorf("effective hard ceiling = %q, want 95%% (default when unconfigured)", got)
	}
	if got := descOf("_derived_reactive_savings"); !strings.Contains(got, "50%") {
		t.Errorf("reactive savings label = %q, want it to show 50%% savings", got)
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
// offers the SDK default plus every level from 10% to 95% in 5% increments
// (the user-settable range per the 3-layer directive), and that selecting a
// non-preset value like 30 persists it.
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
	sr.onSel("threshold", true)
	if sr.title != "Trigger threshold (% of max tokens):" {
		t.Fatalf("title = %q", sr.title)
	}
	// Expect "0" (default) then 10,15,...,95 in order.
	if len(sr.options) != 19 {
		t.Fatalf("expected 19 threshold options (default + 10..95 step 5), got %d", len(sr.options))
	}
	if sr.options[0].Value != "0" {
		t.Errorf("option[0].Value = %q, want \"0\" (default)", sr.options[0].Value)
	}
	for i, opt := range sr.options[1:] {
		want := fmt.Sprintf("%d", 10+i*5)
		if opt.Value != want {
			t.Errorf("option[%d].Value = %q, want %q", i+1, opt.Value, want)
		}
		if opt.Label != want+"%" {
			t.Errorf("option[%d].Label = %q, want %q", i+1, opt.Label, want+"%")
		}
	}
	// Selecting a non-preset value (30) must persist it.
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
	sr.onSel("soft", true)
	if sr.title != "Soft threshold — cheap zero-LLM maintenance when cache is cold:" {
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
	sr.onSel("hard", true)
	if sr.title != "Hard ceiling (emergency: bypass cache, hard-layer strategy fires):" {
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
// effect (bugs.md Issue 2): without the clear, a config still carrying
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
// for bugs.md Issue 2: with a stale legacy alias present, choosing a new trigger
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
	sr.onSel("max_tokens", true)
	sr.onSel("0", true)
	if cfg.ContextCompression.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0 (auto)", cfg.ContextCompression.MaxTokens)
	}
}

func TestCompressionLabel(t *testing.T) {
	disabledCfg := &config.Config{ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(false)}}
	if got := compressionLabel(disabledCfg); got != "off" {
		t.Errorf("compressionLabel(disabled) = %q, want off", got)
	}
	got := compressionLabel(&config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:          boolPtr(true),
			Strategy:         "micro",
			ThresholdPercent: 80,
		},
	})
	if !strings.Contains(got, "micro") || !strings.Contains(got, "80%") {
		t.Errorf("compressionLabel = %q, want substring micro and 80%%", got)
	}
	// Empty strategy falls back to tool_elision for display.
	got = compressionLabel(&config.Config{
		ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(true), ThresholdPercent: 100},
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
		"loop_warning":            "3 (default)",
		"loop_interrupt":          "5 (default)",
		"tool_repeat_total":       "off",
		"tool_repeat_consecutive": "2 (default)",
		"max_tool_calls":          "3 (default)",
		"disable_tool_budget":     "off",
		"stream_repeats":          "5 (default)",
		"stream_strikes":          "3 (default)",
		"stream_reset_after":      "10 (default)",
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
// (bugs.md: Save full-dump baked merged project/embedded values into the home
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
// not dumped (bugs.md: Save full-dump contamination).
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
