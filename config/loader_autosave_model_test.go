// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// autosaveCascadeCase describes one cascade-resolution scenario for
// execution.auto_save_model: file contents per layer ("" = no file) and the
// expected RESOLVED value. The embedded default is true, so any layer that
// leaves the key unset must keep the lower layer's value — a plain bool
// cannot express that, which is exactly the bug this guards against.
type autosaveCascadeCase struct {
	name        string
	homeYAML    string // content of ~/.goa/config.yaml ("" = absent)
	projectYAML string // content of <project>/.goa/config.yaml ("" = absent)
	want        bool
}

func TestAutoSaveModel_TriStateAcrossCascade(t *testing.T) {
	cases := []autosaveCascadeCase{
		{
			name: "legacy home execution section without the key keeps default",
			homeYAML: `execution:
  thinking_default: high
`,
			want: true,
		},
		{
			name: "home without any execution section keeps default",
			homeYAML: `providers:
  - id: p1
    name: P1
    endpoint: http://localhost:1
    api_key: k
`,
			want: true,
		},
		{
			name: "no files at all keeps embedded default",
			want: true,
		},
		{
			name:        "explicit false at home, project silent stays opted out",
			homeYAML:    "execution:\n  auto_save_model: false\n",
			projectYAML: "",
			want:        false,
		},
		{
			name:        "explicit true at project overrides home false",
			homeYAML:    "execution:\n  auto_save_model: false\n",
			projectYAML: "execution:\n  auto_save_model: true\n",
			want:        true,
		},
		{
			name:        "explicit false at project overrides home true",
			homeYAML:    "execution:\n  auto_save_model: true\n",
			projectYAML: "execution:\n  auto_save_model: false\n",
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			project := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("GOA_HOME", home)
			if tc.homeYAML != "" {
				writeTestFile(t, filepath.Join(home, ".goa", "config.yaml"), tc.homeYAML)
			}
			if tc.projectYAML != "" {
				writeTestFile(t, filepath.Join(project, ".goa", "config.yaml"), tc.projectYAML)
			}
			cfg, err := NewCascadeLoader(project, "", nil).Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Execution.AutoSaveModelEnabled(); got != tc.want {
				t.Fatalf("resolved auto_save_model = %v, want %v", got, tc.want)
			}
		})
	}
}

// writeTestFile creates parent directories and writes content.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
