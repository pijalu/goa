// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/usage"
)

// fakeUsageStore implements usageStore for tests.
type fakeUsageStore struct {
	stats map[string][]usage.Stat // key: dim|project
	sum   usage.Stat
}

func (f *fakeUsageStore) Query(dim usage.Dimension, project string) ([]usage.Stat, error) {
	return f.stats[dimKey(dim, project)], nil
}
func (f *fakeUsageStore) Sum(project string) (usage.Stat, error) { return f.sum, nil }
func (f *fakeUsageStore) Close() error                           { return nil }

func dimKey(d usage.Dimension, project string) string {
	return string(rune('0'+int(d))) + "|" + project
}

func newUsageCtx(buf *strings.Builder, project string) core.Context {
	return core.Context{OutputBuffer: buf, ProjectDir: project}
}

func TestUsageCommand_GlobalShowsAllSections(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 3, PromptN: 200, PredictedN: 100},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByProject, ""):  {{Key: "/a", Turns: 2, PromptN: 150, PredictedN: 70}},
			dimKey(usage.ByProvider, ""): {{Key: "kimi", Turns: 3, PromptN: 200, PredictedN: 100}},
			dimKey(usage.ByModel, ""):    {{Key: "k2", Turns: 3, PromptN: 200, PredictedN: 100}},
		},
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Global usage", "By project", "By provider", "By model", "kimi", "k2", "/a"} {
		if !strings.Contains(out, want) {
			t.Errorf("global /usage missing %q:\n%s", want, out)
		}
	}
}

func TestUsageCommand_ScopeFiltersSections(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 1, PromptN: 10, PredictedN: 5},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByModel, ""): {{Key: "k2", Turns: 1, PromptN: 10, PredictedN: 5}},
		},
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"model"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "By model") {
		t.Errorf("/usage:model missing model section:\n%s", out)
	}
	if strings.Contains(out, "By provider") || strings.Contains(out, "By project") {
		t.Errorf("/usage:model should not show other sections:\n%s", out)
	}
}

func TestUsageCommand_HereScopesToProject(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 2, PromptN: 50, PredictedN: 25},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByProvider, "/a"): {{Key: "kimi", Turns: 2, PromptN: 50, PredictedN: 25}},
		},
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }, ProjectDir: "/a"}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"here"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Usage for /a") {
		t.Errorf("/usage:here missing project label:\n%s", out)
	}
	if !strings.Contains(out, "kimi") {
		t.Errorf("/usage:here missing provider row:\n%s", out)
	}
}

func TestUsageCommand_EmptyStoreMessage(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{sum: usage.Stat{Turns: 0}}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "No usage recorded yet") {
		t.Errorf("empty store should print guidance:\n%s", buf.String())
	}
}

func TestUsageCommand_UnknownScope(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"bogus"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "Unknown /usage scope") {
		t.Errorf("unknown scope should print usage hint:\n%s", buf.String())
	}
}

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1500, "1.5K"},
		{2_500_000, "2.5M"},
	}
	for _, tc := range cases {
		if got := humanTokens(tc.in); got != tc.want {
			t.Errorf("humanTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
