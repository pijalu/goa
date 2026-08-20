// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tui"
)

// TestConfiguredModelItems_LocalColors pins the model-list coloring rule:
// models served by a local provider (localhost / 127.0.0.1 endpoint) render
// green (tool_success); all other models — remote providers, or models whose
// provider is missing from config — keep the default color.
func TestConfiguredModelItems_LocalColors(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: "http://localhost:1234/v1"},
			{ID: "ollama", Endpoint: "http://127.0.0.1:11434/v1"},
			{ID: "remote", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "llama", ProviderID: "local", Model: "llama"},
			{ID: "qwen", ProviderID: "ollama", Model: "qwen"},
			{ID: "gpt", ProviderID: "remote", Model: "gpt"},
			{ID: "orphan", ProviderID: "missing", Model: "orphan"},
		},
	}

	items := configuredModelItems(cfg, "")
	colors := map[string]string{}
	for _, it := range items {
		colors[it.Value] = it.Color
	}

	green := tui.TheTheme.ColorHex("tool_success")
	if colors["llama"] != green {
		t.Errorf("local model color = %q, want green %q", colors["llama"], green)
	}
	if colors["qwen"] != green {
		t.Errorf("127.0.0.1 model color = %q, want green %q", colors["qwen"], green)
	}
	if colors["gpt"] != "" {
		t.Errorf("remote model color = %q, want default (empty)", colors["gpt"])
	}
	if colors["orphan"] != "" {
		t.Errorf("model with missing provider color = %q, want default (empty)", colors["orphan"])
	}
}

func TestModelCommand_NoArgs_ShowsSelector(t *testing.T) {
	var capturedTitle string
	var capturedCurrent string

	ctx := newModeTestContext()
	ctx.Config.ActiveModel = "llama3"
	ctx.ProviderManager = newTestProviderManager()
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		capturedTitle = title
		_ = options
		capturedCurrent = current
		onSelected("", false)
	}

	cmd := &ModelCommand{}
	err := cmd.Run(ctx, []string{})
	if err != nil {
		t.Fatalf("Run with no args: %v", err)
	}

	if capturedTitle != "Select model:" {
		t.Errorf("title = %q, want %q", capturedTitle, "Select model:")
	}
	if capturedCurrent != "llama3" {
		t.Errorf("current = %q, want %q", capturedCurrent, "llama3")
	}
}

// TestModelCommand_PickerKeepsDefaultKeymap is a regression guard for the
// /goal:manage selector rework (goal manager): the /model picker
// must keep the DEFAULT selector bindings — '+' = add, '-' = delete — and
// must not request the per-instance reorder keymap.
func TestModelCommand_PickerKeepsDefaultKeymap(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "test-provider"
	ctx.Config.ActiveModel = "k3"
	ctx.Config.Providers = []config.ProviderConfig{{ID: "test-provider", Name: "Test"}}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "k3", ProviderID: "test-provider", Model: "k3"},
	}
	ctx.ProviderManager = newTestProviderManager()
	ctx.SelectOptionKeyedFunc = func(string, []tui.SelectorItem, string, tui.SelectorKeymap, func(string, bool)) {
		t.Error("/model must use the default selector bindings, not the reorder keymap")
	}
	var titles []string
	var onSel func(string, bool)
	ctx.SelectOptionFunc = func(title string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		titles = append(titles, title)
		onSel = cb
	}

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, []string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(titles) != 1 || titles[0] != "Select model:" {
		t.Fatalf("expected the model picker, got %v", titles)
	}

	// '-' on a model row emits "__delete__"+id → the remove-confirmation
	// flow must open (delete semantics unchanged).
	onSel("__delete__k3", true)
	if got := titles[len(titles)-1]; got != "Remove model k3?" {
		t.Fatalf("'-' must keep delete semantics: got selector %q", got)
	}

	// Decline the removal; '+' must still open the add-model flow (the
	// provider picker — add semantics unchanged, no network involved).
	onSel("no", true)
	onSel("__add__", true)
	if got := titles[len(titles)-1]; got != "Select provider:" {
		t.Fatalf("'+' must keep add semantics: got selector %q", got)
	}
}

func TestModelCommand_WithArg_SwitchesModel(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveModel = "llama3"
	am := newTestAgentManager()
	ctx.AgentManager = am
	ctx.ProviderManager = newTestProviderManager()

	cmd := &ModelCommand{}
	err := cmd.Run(ctx, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if ctx.Config.ActiveModel != "gpt-4" {
		t.Errorf("ActiveModel = %q, want %q", ctx.Config.ActiveModel, "gpt-4")
	}
}

func TestModelCommand_CompleteArgs_NoProvider(t *testing.T) {
	ctx := newModeTestContext()
	cmd := &ModelCommand{}

	comps := cmd.CompleteArgs(ctx, "")
	if len(comps) != 0 {
		t.Errorf("expected no completions without provider, got %v", comps)
	}
}

func TestModelCommand_CompleteArgs_WithProvider(t *testing.T) {
	ctx := newModeTestContext()
	ctx.ProviderManager = newTestProviderManager()
	cmd := &ModelCommand{}

	// fetchModelItems hits the HTTP endpoint which won't be available in tests,
	// so we just verify it doesn't panic and returns something (likely empty).
	comps := cmd.CompleteArgs(ctx, "")
	_ = comps
}

func TestModelCommand_NoProvider(t *testing.T) {
	ctx := newModeTestContext()
	cmd := &ModelCommand{}

	var buf strings.Builder
	ctx.OutputBuffer = &buf
	err := cmd.Run(ctx, []string{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "No provider configured") {
		t.Errorf("expected 'No provider configured', got %q", buf.String())
	}
}

// newTestProviderManager returns a minimal provider manager for testing.
func newTestProviderManager() *testProviderManager {
	return &testProviderManager{}
}

type testProviderManager struct {
	model string
}

func (p *testProviderManager) Active() (*config.ProviderConfig, string) {
	return &config.ProviderConfig{ID: "local", Name: "Local", Endpoint: "http://localhost:1234/v1", DefaultModel: "llama3"}, "llama3"
}

func (p *testProviderManager) SetActive(providerID, model string) error { return nil }
func (p *testProviderManager) ListModels(providerID string) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{
		{ID: "llama3"},
		{ID: "qwen3-5-9b"},
	}, nil
}
func (p *testProviderManager) ListModelsCached(providerID string, ttl time.Duration) ([]provider.ModelInfo, error) {
	return p.ListModels(providerID)
}
func (p *testProviderManager) TestConnection(providerID string) (time.Duration, int, error) {
	return 0, 0, nil
}
func (p *testProviderManager) ResolveActiveModel() (agenticprovider.Model, error) {
	return agenticprovider.Model{ID: p.model, Name: p.model}, nil
}
func (p *testProviderManager) BuildStreamOptions() agenticprovider.StreamOptions {
	return agenticprovider.StreamOptions{}
}

// TestModelCommand_Add verifies "/model add <id> <provider-id> <model-name>"
// behaves like "/config add model": a direct upsert persisted via the saver,
// and no attempt to switch the active model.
func TestModelCommand_Add(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveModel = "llama3"
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	var buf strings.Builder
	ctx.OutputBuffer = &buf

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, []string{"add", "gpt-4o", "openai", "gpt-4o-2024"}); err != nil {
		t.Fatalf("Run add: %v", err)
	}

	idx := modelIndex(ctx.Config.Models, "gpt-4o")
	if idx < 0 {
		t.Fatalf("model not added: %+v", ctx.Config.Models)
	}
	got := ctx.Config.Models[idx]
	if got.ProviderID != "openai" || got.Model != "gpt-4o-2024" {
		t.Errorf("model = %+v, want provider=openai model=gpt-4o-2024", got)
	}
	if saver.savedCfg == nil {
		t.Error("config not persisted")
	}
	if ctx.Config.ActiveModel != "llama3" {
		t.Errorf("ActiveModel = %q, want unchanged %q", ctx.Config.ActiveModel, "llama3")
	}
	if !strings.Contains(buf.String(), "Added model gpt-4o") {
		t.Errorf("output = %q, want 'Added model gpt-4o'", buf.String())
	}
}

// TestModelCommand_AddUpsert verifies adding an existing model ID updates it
// in place instead of duplicating it, matching doAddModel semantics.
func TestModelCommand_AddUpsert(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.Models = []config.ModelConfig{{ID: "gpt-4o", ProviderID: "old", Model: "old-name"}}
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	var buf strings.Builder
	ctx.OutputBuffer = &buf

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, []string{"add", "gpt-4o", "openai", "gpt-4o-2024"}); err != nil {
		t.Fatalf("Run add: %v", err)
	}

	if len(ctx.Config.Models) != 1 {
		t.Fatalf("expected upsert, got %d models", len(ctx.Config.Models))
	}
	got := ctx.Config.Models[0]
	if got.ProviderID != "openai" || got.Model != "gpt-4o-2024" {
		t.Errorf("model = %+v, want provider=openai model=gpt-4o-2024", got)
	}
}

// TestModelCommand_AddUsage verifies "/model add" with incomplete arguments
// returns a usage error instead of switching to a model literally named "add".
func TestModelCommand_AddUsage(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveModel = "llama3"

	cmd := &ModelCommand{}
	err := cmd.Run(ctx, []string{"add", "gpt-4o"})
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("err = %v, want usage error", err)
	}
	if ctx.Config.ActiveModel != "llama3" {
		t.Errorf("ActiveModel = %q, want unchanged %q", ctx.Config.ActiveModel, "llama3")
	}
}

// TestModelCommand_AddNoProvider verifies "/model add" works without any
// active provider, mirroring "/config add model".
func TestModelCommand_AddNoProvider(t *testing.T) {
	ctx := newModeTestContext()
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	var buf strings.Builder
	ctx.OutputBuffer = &buf

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, []string{"add", "gpt-4o", "openai", "gpt-4o-2024"}); err != nil {
		t.Fatalf("Run add: %v", err)
	}
	if modelIndex(ctx.Config.Models, "gpt-4o") < 0 {
		t.Errorf("model not added without provider: %+v", ctx.Config.Models)
	}
}

// TestModelCommand_AddAlwaysPromptsProvider verifies the interactive add-model
// flow ALWAYS asks for the provider first (Issue 11), even when an
// active provider is set — and pre-selects that active provider as the default.
// The subsequent model list is scoped to the chosen provider only.
func TestModelCommand_AddAlwaysPromptsProvider(t *testing.T) {
	ctx := newModeTestContext()
	ctx.ConfigSaver = &fakeConfigSaver{}
	ctx.Config.ActiveProvider = "openai" // active provider must NOT bypass the picker
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		{ID: "poolside", Endpoint: "https://inference.poolside.ai/v1"},
	}

	var gotTitle, gotCurrent string
	var gotOptions []tui.SelectorItem
	prompted := false
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		prompted = true
		gotTitle, gotOptions, gotCurrent = title, options, current
		onSelected("", false) // cancel: we only assert the provider prompt itself
	}

	if err := runModelCommand(ctx, nil, ctx.Config, ctx.ConfigSaver, []string{"add"}); err != nil {
		t.Fatalf("runModelCommand add: %v", err)
	}

	if !prompted {
		t.Fatal("add-model did not prompt for a provider; it must always ask")
	}
	if gotTitle != "Select provider:" {
		t.Errorf("prompt title = %q, want %q", gotTitle, "Select provider:")
	}
	if gotCurrent != "openai" {
		t.Errorf("pre-selected provider = %q, want active provider %q", gotCurrent, "openai")
	}
	var sawOpenAI, sawPoolside bool
	for _, it := range gotOptions {
		if it.Value == "openai" {
			sawOpenAI = true
		}
		if it.Value == "poolside" {
			sawPoolside = true
		}
	}
	if !sawOpenAI || !sawPoolside {
		t.Errorf("provider picker should list all configured providers; got %v", gotOptions)
	}
}

// TestModelCommand_StatusShowsCurrent verifies /model? prints the live state.
func TestModelCommand_StatusShowsCurrent(t *testing.T) {
	cmd := &ModelCommand{}
	ctx := newModeTestContext()
	ctx.Config.ActiveModel = "llama3"
	ctx.Config.ActiveProvider = "local"
	got := cmd.Status(ctx)
	if !strings.Contains(got, "llama3") || !strings.Contains(got, "local") {
		t.Errorf("Status() = %q, want llama3 and local", got)
	}
}

// TestModelCommand_PickerListsAllProvidersModels verifies the picker surfaces
// models from every configured provider, not just the active one (Bug 3).
func TestModelCommand_PickerListsAllProvidersModels(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "openai"
	ctx.Config.ActiveModel = "gpt-4o"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		{ID: "anthropic", Endpoint: "https://api.anthropic.com/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		{ID: "claude-3-5", ProviderID: "anthropic", Model: "claude-3-5-sonnet"},
	}
	ctx.ProviderManager = newTestProviderManager()

	var got []tui.SelectorItem
	ctx.SelectOptionFunc = func(_ string, options []tui.SelectorItem, _ string, onSelected func(string, bool)) {
		got = options
		onSelected("", false)
	}

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawClaude, sawGPT bool
	for _, item := range got {
		if item.Value == "claude-3-5" {
			sawClaude = true
		}
		if item.Value == "gpt-4o" {
			sawGPT = true
		}
	}
	if !sawClaude {
		t.Errorf("picker did not list model from non-active provider (anthropic). items=%v", got)
	}
	if !sawGPT {
		t.Errorf("picker did not list active model. items=%v", got)
	}
}

// TestModelCommand_SelectForeignModelSwitchesProvider verifies that picking
// a model configured under a different provider also updates ActiveProvider.
func TestModelCommand_SelectForeignModelSwitchesProvider(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "openai"
	ctx.Config.ActiveModel = "gpt-4o"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		{ID: "anthropic", Endpoint: "https://api.anthropic.com/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		{ID: "claude-3-5", ProviderID: "anthropic", Model: "claude-3-5-sonnet"},
	}
	ctx.ProviderManager = newTestProviderManager()

	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, onSelected func(string, bool)) {
		onSelected("claude-3-5", true)
	}

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if ctx.Config.ActiveModel != "claude-3-5" {
		t.Errorf("ActiveModel = %q, want claude-3-5", ctx.Config.ActiveModel)
	}
	if ctx.Config.ActiveProvider != "anthropic" {
		t.Errorf("ActiveProvider = %q, want anthropic (should follow model)", ctx.Config.ActiveProvider)
	}
}

// TestModelCommand_MissingProviderDoesNotSwitch verifies that selecting a
// model whose provider is not configured is rejected instead of routing
// requests (and API keys) to the wrong endpoint.
func TestModelCommand_MissingProviderDoesNotSwitch(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "openai"
	ctx.Config.ActiveModel = "gpt-4o"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		{ID: "claude-3-5", ProviderID: "anthropic", Model: "claude-3-5-sonnet"},
	}
	ctx.ProviderManager = newTestProviderManager()

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, []string{"claude-3-5"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if ctx.Config.ActiveProvider != "openai" {
		t.Errorf("ActiveProvider = %q, want openai (should not switch to missing provider)", ctx.Config.ActiveProvider)
	}
	if ctx.Config.ActiveModel != "gpt-4o" {
		t.Errorf("ActiveModel = %q, want gpt-4o (should not switch)", ctx.Config.ActiveModel)
	}
	if !strings.Contains(buf.String(), "not configured") {
		t.Errorf("expected output about missing provider, got %q", buf.String())
	}
}

// TestModelCommand_SentinelArgRejected is the regression for the leaked
// selector sentinel bug: a "__delete__X" value (emitted by the picker's
// backspace/delete hotkey) must never become the active model — the picker
// previously left the active model named "__delete__deepseek-v4-flash".
func TestModelCommand_SentinelArgRejected(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "local"
	ctx.Config.ActiveModel = "llama3"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "local", Endpoint: "http://localhost:1234/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "llama3", ProviderID: "local", Model: "llama3"},
	}
	ctx.ProviderManager = newTestProviderManager()

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, []string{"__delete__llama3"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if ctx.Config.ActiveModel != "llama3" {
		t.Errorf("ActiveModel = %q, want llama3 (sentinel must not be applied)", ctx.Config.ActiveModel)
	}
	if !strings.Contains(buf.String(), "Invalid model name") {
		t.Errorf("expected rejection message, got %q", buf.String())
	}
}

// TestApplyModelSelection_SentinelIgnored verifies the picker callback path:
// applyModelSelection must drop sentinel values instead of persisting them.
func TestApplyModelSelection_SentinelIgnored(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "local"
	ctx.Config.ActiveModel = "llama3"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "local", Endpoint: "http://localhost:1234/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "llama3", ProviderID: "local", Model: "llama3"},
	}

	applyModelSelection(ctx, ctx.Config, nil, "__delete__llama3")

	if ctx.Config.ActiveModel != "llama3" {
		t.Errorf("ActiveModel = %q, want llama3 (sentinel must be dropped)", ctx.Config.ActiveModel)
	}
}

// TestModelPickerItems_HaveSearchLabel verifies model selector items carry a
// SearchLabel (model id + provider + model name) so the picker filter does
// not match the "model="/"provider=" description noise.
func TestModelPickerItems_HaveSearchLabel(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "opencode-go"
	ctx.Config.ActiveModel = "deepseek-v4-flash"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "opencode-go", Endpoint: "https://opencode.ai/zen/go/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "deepseek-v4-flash", ProviderID: "opencode-go", Model: "deepseek-v4-flash"},
	}
	ctx.ProviderManager = newTestProviderManager()

	var got []tui.SelectorItem
	ctx.SelectOptionFunc = func(_ string, options []tui.SelectorItem, _ string, onSelected func(string, bool)) {
		got = options
		onSelected("", false)
	}

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, item := range got {
		if item.Value == "deepseek-v4-flash" {
			if item.SearchLabel == "" {
				t.Fatalf("model item missing SearchLabel: %+v", item)
			}
			for _, term := range []string{"deepseek-v4-flash", "opencode-go"} {
				if !strings.Contains(item.SearchLabel, term) {
					t.Errorf("SearchLabel %q missing term %q", item.SearchLabel, term)
				}
			}
			return
		}
	}
	t.Fatalf("model item not found in picker: %v", got)
}

// TestModelCommand_PropagatesToAgent verifies that switching a model updates
// the active agent's model via AgentManager.SetModel.
func TestModelCommand_PropagatesToAgent(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "local"
	ctx.Config.ActiveModel = "llama3"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "local", Endpoint: "http://localhost:1234/v1"},
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "llama3", ProviderID: "local", Model: "llama3"},
		{ID: "gpt-4", ProviderID: "openai", Model: "gpt-4"},
	}
	pm := newTestProviderManager()
	pm.model = "gpt-4"
	ctx.ProviderManager = pm
	am := newTestAgentManager()
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "llama3"},
	}))
	ctx.AgentManager = am

	cmd := &ModelCommand{}
	if err := cmd.Run(ctx, []string{"gpt-4"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mdl := am.ActiveModel()
	if mdl.ID != "gpt-4" {
		t.Errorf("active model ID = %q, want gpt-4", mdl.ID)
	}
}
