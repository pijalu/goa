// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"testing"
)

func TestMergeRegistry_DisablesBuiltin(t *testing.T) {
	merged := MergeRegistry(map[string]ServerOverride{
		"gopls": {Disabled: true},
	})
	for _, s := range merged {
		if s.ID == "gopls" {
			t.Errorf("gopls should be disabled, still present in merged registry")
		}
	}
	// Other builtins remain.
	var found bool
	for _, s := range merged {
		if s.ID == "pyright" {
			found = true
		}
	}
	if !found {
		t.Errorf("pyright should remain enabled")
	}
}

func TestMergeRegistry_OverrideCommand(t *testing.T) {
	merged := MergeRegistry(map[string]ServerOverride{
		"gopls": {Command: []string{"/custom/gopls", "-mode=stdio"}},
	})
	for _, s := range merged {
		if s.ID == "gopls" {
			if len(s.Command) == 0 || s.Command[0] != "/custom/gopls" {
				t.Errorf("gopls command = %v, want /custom/gopls", s.Command)
			}
			if s.Npx != nil || s.Install != nil {
				t.Errorf("custom command must clear npx/install fallbacks")
			}
			return
		}
	}
	t.Error("gopls not found in merged registry")
}

func TestMergeRegistry_CustomServer(t *testing.T) {
	merged := MergeRegistry(map[string]ServerOverride{
		"myls": {
			Command:    []string{"myls", "--stdio"},
			Extensions: []string{".my"},
			Markers:    []string{"my.toml"},
			LanguageID: "mylang",
		},
	})
	var found *ServerSpec
	for i := range merged {
		if merged[i].ID == "myls" {
			found = &merged[i]
		}
	}
	if found == nil {
		t.Fatal("custom server myls not added")
	}
	if found.Command[0] != "myls" || found.LanguageID != "mylang" {
		t.Errorf("custom server = %+v", found)
	}
	if !found.handlesExt(".my") {
		t.Errorf("custom server should handle .my")
	}
}

func TestSpecForFile(t *testing.T) {
	specs := []ServerSpec{
		{ID: "gopls", Extensions: []string{".go"}},
		{ID: "pyright", Extensions: []string{".py"}},
	}
	if s := specForFile(specs, "main.go"); s == nil || s.ID != "gopls" {
		t.Errorf("main.go → %v, want gopls", s)
	}
	if s := specForFile(specs, "app.py"); s == nil || s.ID != "pyright" {
		t.Errorf("app.py → %v, want pyright", s)
	}
	if s := specForFile(specs, "notes.txt"); s != nil {
		t.Errorf("notes.txt → %v, want nil", s)
	}
}

func TestFindRoot(t *testing.T) {
	dir := t.TempDir()
	spec := ServerSpec{ID: "gopls", Markers: []string{"go.mod"}}
	// No go.mod present: falls back to dir.
	if got := spec.FindRoot(dir+"/sub/file.go", dir); got != dir {
		t.Errorf("FindRoot without marker = %q, want %q", got, dir)
	}
}

func TestLanguageIDFor(t *testing.T) {
	cases := map[string]string{
		"a.ts":  "typescript",
		"a.tsx": "typescriptreact",
		"a.js":  "javascript",
		"a.jsx": "javascriptreact",
		"a.c":   "c",
		"a.cpp": "cpp",
	}
	for path, want := range cases {
		if got := LanguageIDFor(path); got != want {
			t.Errorf("LanguageIDFor(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRegistryLoads(t *testing.T) {
	reg := Registry()
	if len(reg) == 0 {
		t.Fatal("embedded registry is empty")
	}
	// Spot-check a few expected servers from the OpenCode port.
	want := map[string]bool{"gopls": false, "pyright": false, "typescript": false, "jdtls": false, "rust": false}
	for _, s := range reg {
		if _, ok := want[s.ID]; ok {
			want[s.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("expected builtin server %q in registry", id)
		}
	}
}
