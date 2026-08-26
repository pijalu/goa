// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// Bug A (2026-08-27): adding a model whose name already exists under ANOTHER
// provider must succeed as a distinct, coexisting entry — never refused, never
// a silent in-place replace of the other provider's binding. Model identity is
// provider-scoped; bare IDs stay unique via provider-qualified derivation.

// findModel returns the model with the exact ID, or nil.
func findModel(models []config.ModelConfig, id string) *config.ModelConfig {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

// countModelsNamed returns how many entries carry the given model name.
func countModelsNamed(models []config.ModelConfig, name string) int {
	n := 0
	for _, m := range models {
		if m.Model == name {
			n++
		}
	}
	return n
}

func TestUniqueModelID_FreeBase(t *testing.T) {
	models := []config.ModelConfig{{ID: "other", ProviderID: "p1", Model: "other"}}
	if got := uniqueModelID(models, "deepseek-v4-flash", "opencode"); got != "deepseek-v4-flash" {
		t.Errorf("uniqueModelID free base = %q, want %q", got, "deepseek-v4-flash")
	}
}

func TestUniqueModelID_CrossProviderClashQualifies(t *testing.T) {
	models := []config.ModelConfig{{ID: "deepseek-v4-flash", ProviderID: "deepseek", Model: "deepseek-v4-flash"}}
	got := uniqueModelID(models, "deepseek-v4-flash", "opencode")
	if got == "deepseek-v4-flash" {
		t.Fatalf("uniqueModelID returned the colliding base ID %q", got)
	}
	if !strings.Contains(got, "opencode") {
		t.Errorf("uniqueModelID = %q, want a provider-qualified variant containing %q", got, "opencode")
	}
	if !strings.HasPrefix(got, "deepseek-v4-flash") {
		t.Errorf("uniqueModelID = %q, want it derived from the base ID %q", got, "deepseek-v4-flash")
	}
}

func TestUniqueModelID_SlugifiesProvider(t *testing.T) {
	models := []config.ModelConfig{{ID: "m", ProviderID: "p1", Model: "m"}}
	got := uniqueModelID(models, "m", "My Provider")
	if strings.ContainsAny(got, " \t") {
		t.Errorf("uniqueModelID = %q contains whitespace; provider slug must be sanitized", got)
	}
	if got == "m" {
		t.Errorf("uniqueModelID = %q, want a disambiguated ID (base taken by another provider)", got)
	}
}

func TestUniqueModelID_NumericFallbackWhenQualifiedAlsoTaken(t *testing.T) {
	models := []config.ModelConfig{
		{ID: "m", ProviderID: "p1", Model: "m"},
		{ID: "m-p2", ProviderID: "p2", Model: "m"},
	}
	got := uniqueModelID(models, "m", "p2")
	if got == "m" || got == "m-p2" {
		t.Fatalf("uniqueModelID = %q collides with an existing ID", got)
	}
	if findModel(models, got) != nil {
		t.Errorf("uniqueModelID = %q already present in models", got)
	}
}

// Picker add: same name + SAME provider is the genuine duplicate → refusal,
// no new entry.
func TestAddAndShowModel_SameProviderSameNameRefused(t *testing.T) {
	ctx := newModeTestContext()
	ctx.EventBus = event.MakeBus(4, 4, 4, 4)
	ctx.Config.Models = []config.ModelConfig{{ID: "deepseek-v4-flash", ProviderID: "deepseek", Model: "deepseek-v4-flash", Name: "deepseek-v4-flash"}}
	ctx.Config.ActiveModel = "deepseek-v4-flash"
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	// Re-showing the selector goes through SelectOption; swallow it.
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		// no-op: keep the picker closed in the test
	}

	addAndShowModel(ctx, ctx.Config, saver, "deepseek", "deepseek-v4-flash")

	if got := countModelsNamed(ctx.Config.Models, "deepseek-v4-flash"); got != 1 {
		t.Errorf("same-provider same-name add must not duplicate; got %d entries", got)
	}
}

// Picker add: same name + DIFFERENT provider appends a distinct entry with a
// unique provider-qualified ID; the original binding survives untouched.
func TestAddAndShowModel_CrossProviderCoexists(t *testing.T) {
	ctx := newModeTestContext()
	ctx.EventBus = event.MakeBus(4, 4, 4, 4)
	ctx.Config.Models = []config.ModelConfig{{ID: "deepseek-v4-flash", ProviderID: "deepseek", Model: "deepseek-v4-flash", Name: "deepseek-v4-flash"}}
	ctx.Config.ActiveProvider = "opencode"
	ctx.Config.ActiveModel = "deepseek-v4-flash"
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		// no-op: keep the picker closed in the test
	}

	addAndShowModel(ctx, ctx.Config, saver, "opencode", "deepseek-v4-flash")

	if got := countModelsNamed(ctx.Config.Models, "deepseek-v4-flash"); got != 2 {
		t.Fatalf("cross-provider same-name add must append a 2nd entry; got %d entries: %+v", got, ctx.Config.Models)
	}
	// Original deepseek binding untouched.
	orig := findModel(ctx.Config.Models, "deepseek-v4-flash")
	if orig == nil || orig.ProviderID != "deepseek" {
		t.Errorf("original deepseek binding clobbered: %+v", orig)
	}
	// New entry under opencode with a distinct ID.
	var added *config.ModelConfig
	for i := range ctx.Config.Models {
		if ctx.Config.Models[i].ProviderID == "opencode" {
			added = &ctx.Config.Models[i]
		}
	}
	if added == nil {
		t.Fatal("no opencode model entry added")
	}
	if added.ID == "deepseek-v4-flash" {
		t.Errorf("new entry reuses the colliding bare ID %q", added.ID)
	}
	if saver.savedCfg == nil {
		t.Error("cross-provider add not persisted")
	}
}

// CLI add: same ID + SAME provider updates in place (upsert preserved).
func TestDoAddModel_SameProviderUpsert(t *testing.T) {
	cfg := &config.Config{Models: []config.ModelConfig{{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o-old"}}}
	saver := &fakeConfigSaver{}

	if err := doAddModel(cfg, saver, newWriter(), "gpt-4o", "openai", "gpt-4o-2024"); err != nil {
		t.Fatalf("doAddModel: %v", err)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("same-provider same-ID add must upsert in place; got %d models", len(cfg.Models))
	}
	if cfg.Models[0].Model != "gpt-4o-2024" {
		t.Errorf("upsert did not update model name: %+v", cfg.Models[0])
	}
}

// CLI add: same ID + DIFFERENT provider must NOT clobber the existing binding;
// it appends a distinct entry under a unique ID.
func TestDoAddModel_CrossProviderNoClobber(t *testing.T) {
	cfg := &config.Config{Models: []config.ModelConfig{{ID: "deepseek-v4-flash", ProviderID: "deepseek", Model: "deepseek-v4-flash"}}}
	saver := &fakeConfigSaver{}

	if err := doAddModel(cfg, saver, newWriter(), "deepseek-v4-flash", "opencode", "deepseek-v4-flash"); err != nil {
		t.Fatalf("doAddModel: %v", err)
	}

	if len(cfg.Models) != 2 {
		t.Fatalf("cross-provider same-ID add must append a new entry, not upsert; got %d models: %+v", len(cfg.Models), cfg.Models)
	}
	orig := findModel(cfg.Models, "deepseek-v4-flash")
	if orig == nil || orig.ProviderID != "deepseek" {
		t.Errorf("existing deepseek binding was clobbered: %+v", orig)
	}
	var added *config.ModelConfig
	for i := range cfg.Models {
		if cfg.Models[i].ProviderID == "opencode" {
			added = &cfg.Models[i]
		}
	}
	if added == nil {
		t.Fatal("no opencode entry appended")
	}
	if added.ID == "deepseek-v4-flash" {
		t.Errorf("new entry reuses the colliding ID %q", added.ID)
	}
}
