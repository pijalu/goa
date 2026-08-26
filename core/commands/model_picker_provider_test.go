package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/provider"
)

// twoProviderConfig returns a config with providers openai-codex (active)
// and stealth; stealth serves configured model ox-alpha whose API name is
// "stealth/ox-alpha" — the Bug1 scenario shape.
func twoProviderConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "openai-codex", Name: "Codex"},
			{ID: "stealth", Name: "Stealth"},
		},
		Models: []config.ModelConfig{
			{ID: "ox-alpha", ProviderID: "stealth", Model: "stealth/ox-alpha"},
		},
		ActiveProvider: "openai-codex",
	}
	return cfg
}

// newPickerTestContext builds a minimal core.Context for model-switch tests:
// recording manager + isolated cascade saver (temp HOME) + fake agent
// manager so the coupled switch can propagate to the live session.
func newPickerTestContext(t *testing.T, cfg *config.Config) (*core.Context, *recordingProviderManager) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows compat
	base := newModeTestContext()
	ctx := &base
	ctx.Config = cfg
	pm := &recordingProviderManager{}
	pm.model = "llama3"
	ctx.ProviderManager = pm
	// A live agent is required: SetModel on an AgentManager without one
	// returns early, so propagateModelSwitch would never reach SetActive
	// and the switch would silently not happen (observed as ("","")).
	am := newTestAgentManager()
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "llama3"},
	}))
	ctx.AgentManager = am
	// Persist thinking level to a throwaway project dir instead of CWD.
	am.SetStateStore(core.NewStateStore(t.TempDir()))
	ctx.EventBus = event.MakeBus(10, 10, 10, 10)
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	return ctx, pm
}

// newPickerTestContextWithProject is newPickerTestContext with an explicit
// project directory wired into the cascade saver (per-project pin tests).
func newPickerTestContextWithProject(t *testing.T, cfg *config.Config, projectDir string) (*core.Context, *recordingProviderManager) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows compat
	base := newModeTestContext()
	ctx := &base
	ctx.Config = cfg
	pm := &recordingProviderManager{}
	pm.model = "llama3"
	ctx.ProviderManager = pm
	am := newTestAgentManager()
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "llama3"},
	}))
	ctx.AgentManager = am
	am.SetStateStore(core.NewStateStore(t.TempDir()))
	ctx.EventBus = event.MakeBus(10, 10, 10, 10)
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	return ctx, pm
}

// TestApplyModelSelectionForProvider_CarriesPickerProvider reproduces Bug1:
// picking "stealth/ox-alpha" from the all-providers picker while
// openai-codex is active used to keep the stale provider — the footer then
// rendered the mixed pair "(openai-codex) stealth/ox-alpha" and requests
// went to the wrong endpoint. The picker now carries the entry's true
// provider through to the coupled switch.
func TestApplyModelSelectionForProvider_CarriesPickerProvider(t *testing.T) {
	cfg := twoProviderConfig(t)
	// Production configs resolve auto_save_model ON via Load; state it
	// explicitly so the persistence assertion targets the project pin.
	cfg.Execution.AutoSaveModel = boolPtr(true)
	ctx, pm := newPickerTestContext(t, cfg)
	saver := ctx.ConfigSaver.(*fakeConfigSaver)

	// core.Context methods have value receivers: the switch helpers only
	// recognize a core.Context VALUE, not a pointer (see couple_test).
	applyModelSelectionForProvider(*ctx, cfg, saver, "stealth", "stealth/ox-alpha")

	if pm.setProvider != "stealth" {
		t.Fatalf("provider manager SetActive = %q, want %q", pm.setProvider, "stealth")
	}
	if pm.setModel != "stealth/ox-alpha" {
		t.Fatalf("model = %q, want %q", pm.setModel, "stealth/ox-alpha")
	}
	if cfg.ActiveProvider != "stealth" || cfg.ActiveModel != "stealth/ox-alpha" {
		t.Fatalf("config pair = (%s, %s), want (stealth, stealth/ox-alpha)",
			cfg.ActiveProvider, cfg.ActiveModel)
	}
	if saver.projectActiveSaved == nil {
		t.Fatalf("switch was not persisted (project pin)")
	}
	if saver.projectActiveSaved.ActiveProvider != "stealth" || saver.projectActiveSaved.ActiveModel != "stealth/ox-alpha" {
		t.Fatalf("persisted pair = (%s, %s), want switched couple",
			saver.projectActiveSaved.ActiveProvider, saver.projectActiveSaved.ActiveModel)
	}

	// Footer rendering of the resulting couple must not show a mixed pair:
	// the vendor prefix already carries the provider, so no "(stealth)"
	// prefix is added and no stale provider can appear.
	if got := displayModelForFooter(cfg.ActiveProvider, "stealth/ox-alpha"); got != "stealth/ox-alpha" {
		t.Fatalf("statusbar = %q, want vendor-prefixed name without stale prefix", got)
	}
}

// displayModelForFooter mirrors internal/app's statusbar formatting
// (modelDisplay is unexported there; keep this in lockstep).
func displayModelForFooter(providerID, modelName string) string {
	switch {
	case modelName == "":
		return providerID
	case strings.HasPrefix(modelName, providerID+"/"):
		return modelName
	default:
		return "(" + providerID + ") " + modelName
	}
}

// TestCustomModelSelectionHandler_UsesProviderMap pins the picker wiring:
// the handler must look the provider up in the map built from the fetch
// list instead of re-deriving it from cfg.Models.
func TestCustomModelSelectionHandler_UsesProviderMap(t *testing.T) {
	cfg := twoProviderConfig(t)
	ctx, pm := newPickerTestContext(t, cfg)

	handler := customModelSelectionHandler(*ctx, cfg, ctx.ConfigSaver,
		map[string]string{"stealth/ox-alpha": "stealth"})
	handler("stealth/ox-alpha", true)

	if pm.setProvider != "stealth" {
		t.Fatalf("handler lost the picker provider: SetActive = %q", pm.setProvider)
	}
}

// TestApplyModelSelection_CustomKeepsCurrentProvider documents the legacy
// free-text path: a model whose provider is unknown stays on the current one.
func TestApplyModelSelection_CustomKeepsCurrentProvider(t *testing.T) {
	cfg := twoProviderConfig(t)
	ctx, pm := newPickerTestContext(t, cfg)

	applyModelSelection(*ctx, cfg, ctx.ConfigSaver, "totally-custom-model")

	if pm.setProvider != "" && pm.setProvider != "openai-codex" {
		t.Fatalf("custom model switched provider to %q, want kept openai-codex", pm.setProvider)
	}
	if pm.setModel != "totally-custom-model" {
		t.Fatalf("model = %q, want totally-custom-model", pm.setModel)
	}
}

// TestApplyModelSelectionForProvider_ConfiguredModelStillSwitches guards the
// configured-model case: picker provider agrees with cfg.Models derivation.
func TestApplyModelSelectionForProvider_ConfiguredModelStillSwitches(t *testing.T) {
	cfg := twoProviderConfig(t)
	ctx, pm := newPickerTestContext(t, cfg)

	applyModelSelectionForProvider(*ctx, cfg, ctx.ConfigSaver, "stealth", "ox-alpha")

	if pm.setProvider != "stealth" || pm.setModel != "ox-alpha" {
		t.Fatalf("pair = (%q, %q), want (stealth, ox-alpha)", pm.setProvider, pm.setModel)
	}
}

// TestProviderByModelID_FirstEntryWins covers dedup order parity with
// fetchAllProviderModels: a duplicate ID keeps its first listing provider.
func TestProviderByModelID_FirstEntryWins(t *testing.T) {
	got := providerByModelID([]providerModelEntry{
		{ProviderID: "openai-codex", Model: provider.ModelInfo{ID: "shared"}},
		{ProviderID: "stealth", Model: provider.ModelInfo{ID: "shared"}},
	})
	if got["shared"] != "openai-codex" {
		t.Fatalf("map[shared] = %q, want first entry openai-codex", got["shared"])
	}
}
