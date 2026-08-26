// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"errors"
	"os"
	"path/filepath"
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
func TestSpecForFile_BasenameAndCase(t *testing.T) {
	specs := []ServerSpec{{ID: "docker", Filenames: []string{"Dockerfile", "Containerfile"}}}
	for _, path := range []string{"Dockerfile", "dockerfile", "sub/Containerfile"} {
		if got := specForFile(specs, path); got == nil || got.ID != "docker" {
			t.Fatalf("%s not matched: %+v", path, got)
		}
	}
}

func TestSpecForFile_DenoBeatsTypeScript(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.ts")
	specs := []ServerSpec{
		{ID: "typescript", Extensions: []string{".ts"}, Markers: []string{"package.json"}, ExcludeMarkers: []string{"deno.json"}},
		{ID: "deno", Extensions: []string{".ts"}, Markers: []string{"deno.json"}},
	}
	if got := specForFile(specs, path); got == nil || got.ID != "deno" {
		t.Fatalf("selected %v, want deno", got)
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

// TestRegistryParity_OpenCodeIDs keeps the embedded registry aligned with the
// 38-server OpenCode baseline. Executable availability is tested separately;
// this assertion only covers declarative identity.
func TestRegistryParity_OpenCodeIDs(t *testing.T) {
	want := map[string]bool{
		"deno": true, "typescript": true, "vue": true, "eslint": true,
		"oxlint": true, "biome": true, "gopls": true, "ruby-lsp": true,
		"ty": true, "pyright": true, "elixir-ls": true, "zls": true,
		"csharp": true, "razor": true, "fsharp": true, "sourcekit-lsp": true,
		"rust": true, "clangd": true, "svelte": true, "astro": true,
		"jdtls": true, "kotlin-ls": true, "yaml-ls": true, "lua-ls": true,
		"php intelephense": true, "prisma": true, "dart": true, "ocaml-lsp": true,
		"bash": true, "terraform": true, "texlab": true, "dockerfile": true,
		"gleam": true, "clojure-lsp": true, "nixd": true, "tinymist": true,
		"haskell-language-server": true, "julials": true,
	}
	got := make(map[string]bool)
	for _, spec := range Registry() {
		got[spec.ID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("registry has %d IDs, want %d", len(got), len(want))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("OpenCode server %q missing from registry", id)
		}
	}
}

func TestRegistryParityEntries(t *testing.T) {
	parity := map[string][]string{
		"eslint": {".js", ".ts", ".vue"}, "oxlint": {".js", ".svelte", ".astro"},
		"razor": {".razor", ".cshtml"}, "dockerfile": {}, "sourcekit-lsp": {".swift", ".m", ".mm"},
	}
	served := make(map[string]bool)
	for _, spec := range Registry() {
		served[spec.ID] = true
		checkParityEntry(t, spec, parity)
	}
	for id := range parity {
		if !served[id] {
			t.Errorf("registry missing %s", id)
		}
	}
}

// checkParityEntry compares one registered spec against its OpenCode parity
// expectation for the same ID (no-op when there is none). Dockerfile has an
// empty extension expectation but must still match by filename.
func checkParityEntry(t *testing.T, spec ServerSpec, parity map[string][]string) {
	t.Helper()
	wantExts, ok := parity[spec.ID]
	if !ok {
		return
	}
	haveExts := make(map[string]bool, len(spec.Extensions))
	for _, ext := range spec.Extensions {
		haveExts[ext] = true
	}
	for _, ext := range wantExts {
		if !haveExts[ext] {
			t.Errorf("%s missing extension %s", spec.ID, ext)
		}
	}
	if spec.ID == "dockerfile" && len(spec.Filenames) == 0 {
		t.Error("dockerfile missing filenames")
	}
}

func TestLanguageIDFor(t *testing.T) {
	cases := map[string]string{
		"a.ts":    "typescript",
		"a.tsx":   "typescriptreact",
		"a.js":    "javascript",
		"a.jsx":   "javascriptreact",
		"a.c":     "c",
		"a.cpp":   "cpp",
		"a.swift": "swift",
		"a.m":     "objective-c",
		"a.mm":    "objective-cpp",
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

func TestValidateRegistryRejectsMalformedEntries(t *testing.T) {
	cases := []ServerSpec{
		{ID: "", Command: []string{"x"}},
		{ID: "x", Command: nil},
		{ID: "x", Command: []string{"x"}, Extensions: []string{"go"}},
		{ID: "x", Command: []string{"x"}, Install: &InstallSpec{Kind: "download", Binary: "x", URL: "http://example.test/x"}},
	}
	for _, spec := range cases {
		if err := ValidateRegistry([]ServerSpec{spec}); err == nil {
			t.Errorf("ValidateRegistry(%+v) accepted malformed entry", spec)
		}
	}
}

func TestValidateRegistryRejectsDuplicateIDs(t *testing.T) {
	spec := ServerSpec{ID: "x", Command: []string{"x"}}
	if err := ValidateRegistry([]ServerSpec{spec, spec}); err == nil {
		t.Fatal("duplicate IDs accepted")
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
func TestResolveCommand_UsesWorkspaceLocalBinary(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, ".goa", "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "gopls")
	if err := os.WriteFile(path, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	t.Cleanup(func() { lookPath = old })
	spec := &ServerSpec{Command: []string{"gopls", "-mode=stdio"}}
	argv, ok := spec.resolveCommandWithWorkspace(root, filepath.Join(root, "cache"), false)
	if !ok || argv[0] != path {
		t.Fatalf("argv=%v ok=%v, want local %s", argv, ok, path)
	}
}

func TestResolveCommand_UsesPlatformVariant(t *testing.T) {
	old := lookPath
	lookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	t.Cleanup(func() { lookPath = old })
	spec := &ServerSpec{Command: []string{"default"}, Platforms: map[string]PlatformVariant{"default": {Command: []string{"variant"}}}}
	argv, ok := spec.resolveCommandWithWorkspace(t.TempDir(), t.TempDir(), false)
	if !ok || argv[0] != "/bin/variant" {
		t.Fatalf("argv=%v ok=%v", argv, ok)
	}
}

func TestResolutionHintIsActionable(t *testing.T) {
	spec := &ServerSpec{Npx: &NpxSpec{Package: "pyright"}, Install: &InstallSpec{Kind: "go"}}
	got := spec.resolutionHint(false)
	for _, want := range []string{"PATH binary", "npx package pyright", "automatic go installation", "automatic installation disabled"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint %q missing %q", got, want)
		}
	}
}

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
