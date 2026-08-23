package app

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/usage"
)

// usageDefaultConfig returns a provider catalog with two configured models
// and no active model — the Bug6 "no default model" gap.
func usageDefaultConfig() *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "openai-codex"},
			{ID: "stealth"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt-x", ProviderID: "openai-codex", Model: "gpt-x"},
			{ID: "ox-alpha", ProviderID: "stealth", Model: "stealth/ox-alpha"},
		},
	}
}

// seedUsage opens a temp store and records the given events.
func seedUsage(t *testing.T, dir string, events []usage.Record) *usage.Store {
	t.Helper()
	st, err := usage.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatalf("open usage store: %v", err)
	}
	for _, ev := range events {
		if err := st.Add(ev); err != nil {
			t.Fatalf("add event: %v", err)
		}
	}
	return st
}

// TestApplyUsageBasedDefaultModel_PrefersProjectScope verifies the resolver
// picks the project's most-used model over the global one.
func TestApplyUsageBasedDefaultModel_PrefersProjectScope(t *testing.T) {
	dir := t.TempDir()
	store := seedUsage(t, dir, []usage.Record{
		{Project: "/work/a", Provider: "openai-codex", Model: "gpt-x", PromptN: 5000},
		{Project: "/work/b", Provider: "stealth", Model: "stealth/ox-alpha", PromptN: 900},
	})
	stub := func() (*usage.Store, error) { return store, nil }

	cfg := usageDefaultConfig()
	applyUsageBasedDefaultModelWith(cfg, "/work/a", stub)

	if cfg.ActiveProvider != "openai-codex" || cfg.ActiveModel != "gpt-x" {
		t.Fatalf("pair = (%s, %s), want project A's most-used (openai-codex, gpt-x)",
			cfg.ActiveProvider, cfg.ActiveModel)
	}
}

// TestApplyUsageBasedDefaultModel_FallsBackToGlobal covers a project with no
// history: the global most-used model fills in.
func TestApplyUsageBasedDefaultModel_FallsBackToGlobal(t *testing.T) {
	dir := t.TempDir()
	store := seedUsage(t, dir, []usage.Record{
		{Project: "/other", Provider: "stealth", Model: "stealth/ox-alpha", PromptN: 700},
	})
	stub := func() (*usage.Store, error) { return store, nil }

	cfg := usageDefaultConfig()
	applyUsageBasedDefaultModelWith(cfg, "/fresh-project", stub)

	if cfg.ActiveProvider != "stealth" || cfg.ActiveModel != "ox-alpha" {
		t.Fatalf("pair = (%s, %s), want global most-used mapped to (stealth, ox-alpha)",
			cfg.ActiveProvider, cfg.ActiveModel)
	}
}

// TestApplyUsageBasedDefaultModel_RespectsExplicitModel pins that an existing
// active_model is never overridden.
func TestApplyUsageBasedDefaultModel_RespectsExplicitModel(t *testing.T) {
	store := seedUsage(t, t.TempDir(), nil)
	stub := func() (*usage.Store, error) { return store, nil }

	cfg := usageDefaultConfig()
	cfg.ActiveModel = "gpt-x"
	applyUsageBasedDefaultModelWith(cfg, "/anywhere", stub)

	if cfg.ActiveModel != "gpt-x" {
		t.Fatalf("active model overwritten to %q, want untouched gpt-x", cfg.ActiveModel)
	}
}

// TestPickMostUsedModel skips stale entries whose model is no longer
// configured instead of resurrecting them.
func TestPickMostUsedModel(t *testing.T) {
	cfg := usageDefaultConfig()
	stats := []usage.Stat{
		{Key: "deleted-model", PromptN: 9999}, // most tokens, but unconfigured
		{Key: "stealth/ox-alpha", PromptN: 10},
	}
	pid, mid, ok := pickMostUsedModel(stats, cfg)
	if !ok || pid != "stealth" || mid != "ox-alpha" {
		t.Fatalf("pick = (%q, %q, %v), want (stealth, ox-alpha, true)", pid, mid, ok)
	}

	if _, _, ok := pickMostUsedModel(nil, cfg); ok {
		t.Fatal("empty stats must not select anything")
	}
}
