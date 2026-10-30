// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// TestNewFooter_AppliesConfiguredModeLine reproduces B26 through the production
// config-to-footer boundary: configuring segments must change the rendered
// footer, not merely survive YAML loading and cascade merging.
func TestNewFooter_AppliesConfiguredModeLine(t *testing.T) {
	a := &App{subs: &subsystems{
		projectDir: t.TempDir(),
		cfg: &config.Config{TUI: config.TUIConfig{
			ModeLine: config.ModeLineSegmentConfig{
				Left:  []string{"app-badge"},
				Right: []string{"help-hint"},
			},
		}},
	}}

	rendered := strings.Join(a.newFooter().Render(160), "\n")
	for _, want := range []string{"⬡ goa", "/help"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("configured footer does not render %q:\n%s", want, rendered)
		}
	}
}

// TestNewFooter_DefaultModeLinePreservesLegacyLayout pins the shipped default,
// not a hand-built segment list. The examples are the required B26 contract:
// row one keeps workdir/git and mode state; row two keeps every live statistic
// beside the model and plugin metadata.
func TestNewFooter_DefaultModeLinePreservesLegacyLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	projectDir := t.TempDir()
	cfg, err := config.NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("load embedded defaults: %v", err)
	}
	assertDefaultFooterLayout(t, home, projectDir, cfg)
}

// The segment lists shipped before the renderer was implemented were inert and
// were copied into generated home configs. An unchanged copy must therefore
// retain the legacy presentation instead of becoming an opt-in by accident.
func TestNewFooter_CopiedPreRendererDefaultsPreserveLegacyLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create home config: %v", err)
	}
	layer := "tui:\n  modeline:\n    left: [app-badge, mode-badge, current-file]\n    right: [token-bar, activity, help-hint]\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(layer), 0o600); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	projectDir := t.TempDir()
	cfg, err := config.NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("load copied defaults: %v", err)
	}
	assertDefaultFooterLayout(t, home, projectDir, cfg)
}

func assertDefaultFooterLayout(t *testing.T, home, projectDir string, cfg *config.Config) {
	t.Helper()
	a := &App{subs: &subsystems{projectDir: projectDir, cfg: cfg}}
	footer := a.newFooter()
	footer.SetData(tui.FooterData{
		Workdir:       home + "/dev/goa",
		GitBranch:     "feature/zai",
		Profile:       "coding-posture",
		Mode:          "yolo",
		Model:         "(kimi-code) k3.1-pro",
		ThinkingLevel: "xhigh",
		Stats:         "↑636.7K ↓101.8K 10.1 tok/s CH:97.4%▸99.4% TC:360 8.4%/1.0M c:0m-1",
		PluginSegments: []tui.PluginSegment{
			{ID: "quota", Text: "[8%|3%]"},
		},
	})

	lines := footer.Render(240)
	want := []string{
		"~/dev/goa (⎇ feature/zai) coding-posture │ YOLO",
		"↑636.7K ↓101.8K 10.1 tok/s CH:97.4%▸99.4% TC:360 8.4%/1.0M c:0m-1 (kimi-code) k3.1-pro • xhigh • [8%|3%]",
	}
	if len(lines) != len(want) {
		t.Fatalf("default footer row count = %d, want %d: %q", len(lines), len(want), lines)
	}
	for row := range want {
		got := strings.Join(strings.Fields(ansi.Strip(lines[row])), " ")
		if got != want[row] {
			t.Errorf("default footer row %d:\n got: %q\nwant: %q", row+1, got, want[row])
		}
	}
}
