// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"errors"
	"strings"
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

// withNpxLookPath stubs lookPath: npx present, everything else absent, so
// resolveCommand takes the npx branch.
func withNpxLookPath(t *testing.T) {
	t.Helper()
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if name == "npx" {
			return "/usr/bin/npx", nil
		}
		return "", errors.New("not found: " + name)
	}
	t.Cleanup(func() { lookPath = orig })
}

// TestResolveCommand_NpxForm asserts the npx argv uses --package with the
// declared Binary (not the bare package guess that broke pyright, vue, astro,
// prisma and dockerfile servers — Issue LSP).
func TestResolveCommand_NpxForm(t *testing.T) {
	withNpxLookPath(t)
	spec := &ServerSpec{
		Command: []string{"pyright-langserver", "--stdio"},
		Npx:     &NpxSpec{Package: "pyright", Binary: "pyright-langserver", Args: []string{"--stdio"}},
	}
	argv, ok := spec.resolveCommand(t.TempDir(), false)
	if !ok {
		t.Fatal("expected npx resolution to succeed")
	}
	got := strings.Join(argv, " ")
	want := "/usr/bin/npx --yes --package pyright pyright-langserver --stdio"
	if got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// TestResolveCommand_NpxExtraPackages asserts extra packages are installed
// alongside (typescript-language-server needs typescript@5).
func TestResolveCommand_NpxExtraPackages(t *testing.T) {
	withNpxLookPath(t)
	spec := &ServerSpec{
		Command: []string{"typescript-language-server", "--stdio"},
		Npx: &NpxSpec{
			Package:       "typescript-language-server",
			ExtraPackages: []string{"typescript@5"},
			Args:          []string{"--stdio"},
		},
	}
	argv, ok := spec.resolveCommand(t.TempDir(), false)
	if !ok {
		t.Fatal("expected npx resolution to succeed")
	}
	got := strings.Join(argv, " ")
	want := "/usr/bin/npx --yes --package typescript-language-server --package typescript@5 typescript-language-server --stdio"
	if got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// registryNpxBin returns the effective npx bin for a spec (Binary or Package).
func registryNpxBin(s ServerSpec) string {
	if s.Npx == nil {
		return ""
	}
	if s.Npx.Binary != "" {
		return s.Npx.Binary
	}
	return s.Npx.Package
}

// TestRegistry_NpxBinaries pins the embedded registry specs whose runnable bin
// differs from the npm package name — a bare `npx <pkg>` either fails outright
// (pyright: "Unexpected option --stdio") or guesses the wrong bin.
func TestRegistry_NpxBinaries(t *testing.T) {
	wantBin := map[string]string{
		"pyright":    "pyright-langserver",
		"typescript": "typescript-language-server",
		"svelte":     "svelteserver",
		"vue":        "vue-language-server",
		"astro":      "astro-ls",
		"prisma":     "prisma-language-server",
		"dockerfile": "docker-langserver",
		"biome":      "biome",
	}
	found := map[string]bool{}
	for _, s := range Registry() {
		want, ok := wantBin[s.ID]
		if !ok {
			continue
		}
		found[s.ID] = true
		if bin := registryNpxBin(s); bin != want {
			t.Errorf("%s: npx bin = %q, want %q", s.ID, bin, want)
		}
	}
	for id := range wantBin {
		if !found[id] {
			t.Errorf("server %q missing from registry", id)
		}
	}
}

// TestRegistry_TypescriptNeedsTypescript5 pins the extra package: typescript-
// language-server cannot run without a classic tsserver, and typescript v6+
// removed lib/tsserver.js — the fallback must pull typescript@5 alongside.
func TestRegistry_TypescriptNeedsTypescript5(t *testing.T) {
	for _, s := range Registry() {
		if s.ID != "typescript" {
			continue
		}
		if s.Npx == nil || len(s.Npx.ExtraPackages) != 1 || s.Npx.ExtraPackages[0] != "typescript@5" {
			t.Errorf("typescript extra_packages = %v, want [typescript@5]", s.Npx.ExtraPackages)
		}
		return
	}
	t.Error("typescript server missing from registry")
}
