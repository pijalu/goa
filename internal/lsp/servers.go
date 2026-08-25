// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed servers.yaml
var embeddedServersYAML []byte

// NpxSpec describes how to run a Node-based language server via npx when its
// PATH binary is absent. npx downloads the package on first run, so no
// separate install step is needed (per the user's direction to use npx).
type NpxSpec struct {
	// Package is the npm package installed into the npx cache.
	Package string `yaml:"package"`
	// Binary is the executable to run from Package. Defaults to Package —
	// required when they differ, e.g. package "pyright" ships the
	// "pyright-langserver" bin, package "svelte-language-server" ships
	// "svelteserver" (Issue LSP: bare `npx <pkg>` guessed wrong bins).
	Binary string `yaml:"binary,omitempty"`
	// ExtraPackages are additional npm packages the server needs alongside it
	// (e.g. typescript-language-server requires typescript@5 — v6+ removed the
	// classic lib/tsserver.js it launches).
	ExtraPackages []string `yaml:"extra_packages,omitempty"`
	Args          []string `yaml:"args"`
}

// InstallSpec describes how to fetch a server that npx cannot run (gopls via
// `go install`, jdtls via download). Installation is opt-out via config.
type InstallSpec struct {
	Kind     string   `yaml:"kind"` // "go" | "npm" | "download"
	Package  string   `yaml:"package,omitempty"`
	URL      string   `yaml:"url,omitempty"`
	Binary   string   `yaml:"binary"`
	Checksum string   `yaml:"checksum,omitempty"` // optional SHA-256 digest of the archive
	Args     []string `yaml:"args,omitempty"`     // installer-specific flags
}

// PlatformVariant overrides launch/install metadata for one GOOS/GOARCH pair.
// The key in ServerSpec.Platforms is either a full "os/arch" pair or an OS
// name. Full pairs take precedence, allowing architecture-specific downloads.
type PlatformVariant struct {
	Command []string     `yaml:"command,omitempty"`
	Install *InstallSpec `yaml:"install,omitempty"`
}

// DynamicSpec describes workspace-derived initialization values.
// Keys use a small declarative vocabulary; unknown keys are ignored safely.
type DynamicSpec struct {
	PythonVenv string `yaml:"python_venv,omitempty"`
	// TypeScriptServer resolves the workspace-local TypeScript tsserver path.
	TypeScriptServer bool `yaml:"typescript_server,omitempty"`
}

// ServerSpec declaratively describes a language server, loaded from the
// embedded registry (servers.yaml). Modeled on OpenCode's LSP server registry
// (packages/opencode/src/lsp/server.ts).
type ServerSpec struct {
	ID         string   `yaml:"id"`
	Extensions []string `yaml:"extensions"`
	// Filenames matches basenames case-insensitively (for extensionless files such as Dockerfile).
	Filenames      []string                   `yaml:"filenames,omitempty"`
	Markers        []string                   `yaml:"markers"`
	ExcludeMarkers []string                   `yaml:"exclude_markers,omitempty"`
	Command        []string                   `yaml:"command"`
	LanguageID     string                     `yaml:"language_id"`
	Npx            *NpxSpec                   `yaml:"npx,omitempty"`
	Install        *InstallSpec               `yaml:"install,omitempty"`
	Prerequisites  []string                   `yaml:"prerequisites,omitempty"`
	Platforms      map[string]PlatformVariant `yaml:"platforms,omitempty"`
	// Dynamic declares generic initialization values computed from workspace
	// metadata. Keeping this in the registry avoids per-server code branches.
	Dynamic *DynamicSpec `yaml:"dynamic,omitempty"`
	// Env is merged over the process environment for the server process (from
	// user config overrides; not part of the embedded registry).
	Env map[string]string `yaml:"-"`
	// Initialization is sent as initializationOptions at initialize (from user
	// config overrides; builtin dynamic options are computed separately).
	Initialization map[string]any `yaml:"-"`
}

// registry is the parsed embedded server list, in declaration order. Order
// matters only for display; per-file selection is by extension match.
var registry []ServerSpec

func init() {
	if err := yaml.Unmarshal(embeddedServersYAML, &registry); err != nil {
		panic(fmt.Sprintf("lsp: parse embedded servers.yaml: %v", err))
	}
	if err := ValidateRegistry(registry); err != nil {
		panic(fmt.Sprintf("lsp: invalid embedded registry: %v", err))
	}
}

// Registry returns the builtin server specs (embedded). A copy is returned so
// callers cannot mutate the shared slice.
func Registry() []ServerSpec {
	out := make([]ServerSpec, len(registry))
	copy(out, registry)
	return out
}

// ServerOverride mirrors config.LSPServerConfig without the lsp package
// importing config (avoids a dependency cycle). It carries the user-supplied
// per-server overrides.
type ServerOverride struct {
	Command        []string
	Extensions     []string
	Disabled       bool
	Env            map[string]string
	Initialization map[string]any
	Markers        []string
	LanguageID     string
}

// MergeRegistry applies user overrides to the builtin registry, following
// OpenCode's config merge (lsp.ts): a `disabled` entry removes the builtin; an
// entry with a Command replaces the launch; missing fields inherit the builtin.
// Entries that match no builtin define a new custom server (root markers fall
// back to the session dir).
func MergeRegistry(overrides map[string]ServerOverride) []ServerSpec {
	merged := make([]ServerSpec, 0, len(registry)+len(overrides))
	seen := make(map[string]bool, len(registry))
	for _, base := range registry {
		seen[base.ID] = true
		if s, ok := applyOverride(base, overrides[base.ID]); ok {
			merged = append(merged, s)
		}
	}
	return append(merged, customServers(overrides, seen)...)
}

// applyOverride folds one override into its builtin base. ok=false means the
// server is disabled (removed from the active set).
func applyOverride(base ServerSpec, ov ServerOverride) (ServerSpec, bool) {
	if ov.Disabled {
		return ServerSpec{}, false
	}
	s := base
	if len(ov.Command) > 0 {
		s.Command = ov.Command
		s.Npx = nil     // custom command supersedes the npx fallback
		s.Install = nil // and any installer
	}
	if len(ov.Extensions) > 0 {
		s.Extensions = ov.Extensions
	}
	if len(ov.Markers) > 0 {
		s.Markers = ov.Markers
	}
	if ov.LanguageID != "" {
		s.LanguageID = ov.LanguageID
	}
	s.Env = ov.Env
	s.Initialization = ov.Initialization
	return s, true
}

// customServers returns specs for override entries that match no builtin
// (brand-new user-defined servers). Entries with no Command are skipped.
func customServers(overrides map[string]ServerOverride, seen map[string]bool) []ServerSpec {
	var out []ServerSpec
	for id, ov := range overrides {
		if seen[id] || ov.Disabled || len(ov.Command) == 0 {
			continue
		}
		out = append(out, ServerSpec{
			ID:             id,
			Extensions:     ov.Extensions,
			Markers:        ov.Markers,
			Command:        ov.Command,
			LanguageID:     ov.LanguageID,
			Env:            ov.Env,
			Initialization: ov.Initialization,
		})
	}
	return out
}

func ValidateRegistry(specs []ServerSpec) error {
	seen := make(map[string]bool, len(specs))
	for i, s := range specs {
		if err := validateServerSpec(i, s, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateServerSpec(index int, s ServerSpec, seen map[string]bool) error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("entry %d: id is required", index)
	}
	if seen[s.ID] {
		return fmt.Errorf("duplicate server id %q", s.ID)
	}
	seen[s.ID] = true
	if len(s.Command) == 0 || strings.TrimSpace(s.Command[0]) == "" {
		return fmt.Errorf("%s: command is required", s.ID)
	}
	if err := validateFilePatterns(s); err != nil {
		return err
	}
	if err := validateInstallSpec(s.ID, s.Install); err != nil {
		return err
	}
	for key, variant := range s.Platforms {
		if strings.TrimSpace(key) == "" || (len(variant.Command) == 0 && variant.Install == nil) {
			return fmt.Errorf("%s: invalid platform %q", s.ID, key)
		}
		if err := validateInstallSpec(s.ID+"/"+key, variant.Install); err != nil {
			return err
		}
	}
	return nil
}

func validateFilePatterns(s ServerSpec) error {
	for _, ext := range s.Extensions {
		if ext == "" || !strings.HasPrefix(ext, ".") {
			return fmt.Errorf("%s: invalid extension %q", s.ID, ext)
		}
	}
	for _, name := range s.Filenames {
		if strings.TrimSpace(name) == "" || filepath.Base(name) != name {
			return fmt.Errorf("%s: invalid filename %q", s.ID, name)
		}
	}
	return nil
}
func validateInstallSpec(id string, in *InstallSpec) error {
	if in == nil {
		return nil
	}
	if strings.TrimSpace(in.Binary) == "" {
		return fmt.Errorf("%s: installer binary is required", id)
	}
	switch in.Kind {
	case "go", "npm", "dotnet":
		if strings.TrimSpace(in.Package) == "" {
			return fmt.Errorf("%s: installer package is required", id)
		}
	case "download":
		if !strings.HasPrefix(strings.ToLower(in.URL), "https://") {
			return fmt.Errorf("%s: download URL must use HTTPS", id)
		}
	default:
		return fmt.Errorf("%s: unknown install kind %q", id, in.Kind)
	}
	return nil
}

// handlesExt reports whether the spec declares the given extension. An empty
// Extensions list matches everything.
func (s *ServerSpec) handlesExt(ext string) bool {
	if len(s.Extensions) == 0 {
		return true
	}
	for _, e := range s.Extensions {
		if strings.EqualFold(e, ext) {
			return true
		}
	}
	return false
}

func (s *ServerSpec) handlesFile(path string) bool {
	base := filepath.Base(path)
	for _, name := range s.Filenames {
		if strings.EqualFold(name, base) {
			return true
		}
	}
	return s.handlesExt(strings.ToLower(filepath.Ext(path)))
}

func (s *ServerSpec) excludedByMarker(file, dir string) bool {
	cur := dir
	if abs, err := filepath.Abs(file); err == nil {
		cur = filepath.Dir(abs)
	}
	for {
		for _, marker := range s.ExcludeMarkers {
			if fileExists(filepath.Join(cur, marker)) {
				return true
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur || cur == dir {
			return false
		}
		cur = parent
	}
}

// specForFile returns the best matching spec. A candidate with a project marker
// outranks extension-only candidates, allowing Deno to beat TypeScript.
func specForFile(specs []ServerSpec, path string) *ServerSpec {
	var fallback *ServerSpec
	for i := range specs {
		if !specs[i].handlesFile(path) || specs[i].excludedByMarker(path, filepath.Dir(path)) {
			continue
		}
		if fallback == nil {
			fallback = &specs[i]
		}
		if root := specs[i].FindRoot(path, filepath.Dir(path)); root != filepath.Dir(path) {
			return &specs[i]
		}
	}
	return fallback
}

// FindRoot walks upward from the file's directory looking for the nearest
// ancestor containing any of the spec's Markers. It returns dir when none is
// found so the server always has a workspace root (OpenCode NearestRoot
// semantics: fall back to the session directory, never undefined).
func (s *ServerSpec) FindRoot(file, dir string) string {
	start := dir
	if abs, err := filepath.Abs(file); err == nil {
		start = filepath.Dir(abs)
	}
	cur := start
	for {
		for _, m := range s.Markers {
			if fileExists(filepath.Join(cur, m)) {
				return cur
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir
		}
		cur = parent
	}
}

// languageID returns the LSP language ID for a file: the spec's static
// LanguageID when set, otherwise derived per-extension (JS/TS family, c/cpp).
func (s *ServerSpec) languageID(path string) string {
	if s.LanguageID != "" {
		return s.LanguageID
	}
	return LanguageIDFor(path)
}

// LanguageIDFor maps a file extension to an LSP language identifier for the
// families whose specs leave LanguageID empty (JS/TS, c/cpp, latex/bibtex).
func LanguageIDFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx", ".mtsx", ".ctsx":
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".swift":
		return "swift"
	case ".m":
		return "objective-c"
	case ".mm":
		return "objective-cpp"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hh", ".hxx", ".h++":
		return "cpp"
	case ".bib":
		return "bibtex"
	case ".tex":
		return "latex"
	default:
		return "plaintext"
	}
}

// fileExists reports whether path exists (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// lookPath is exec.LookPath, swappable in tests.
var lookPath = exec.LookPath

// resolveCommand determines the argv to launch this server, following the
// resolution order: PATH binary → npx → install. It returns ok=false when the
// server cannot be launched (no binary, no npx, install disabled or failed).
//
// binDir is the directory into which installers place binaries (e.g.
// ~/.goa/bin); installAllowed gates the install step (lsp.disable_download).
func (s *ServerSpec) resolveCommand(binDir string, installAllowed bool) (argv []string, ok bool) {
	return s.resolveCommandWithWorkspace(filepath.Dir(binDir), binDir, installAllowed)
}

// resolveCommandWithWorkspace applies generic resolution to a registry entry.
// Workspace-local .goa/bin and node_modules/.bin binaries win over downloads;
// this keeps projects hermetic without requiring per-server conditionals.
func (s *ServerSpec) resolveCommandWithWorkspace(workspace, binDir string, installAllowed bool) (argv []string, ok bool) {
	variant := s.platformVariant()
	command, installer := s.Command, s.Install
	if variant != nil {
		if len(variant.Command) > 0 {
			command = variant.Command
		}
		if variant.Install != nil {
			installer = variant.Install
		}
	}
	if len(command) == 0 {
		return nil, false
	}
	if bin := localBinary(workspace, binDir, command[0]); bin != "" {
		return append([]string{bin}, command[1:]...), true
	}
	if bin, err := lookPath(command[0]); err == nil {
		return append([]string{bin}, command[1:]...), true
	}
	if argv, ok := s.npxArgv(); ok {
		return argv, true
	}
	if installAllowed && installer != nil {
		if bin, err := installer.run(binDir); err == nil && bin != "" {
			return append([]string{bin}, command[1:]...), true
		}
	}
	return nil, false
}

func (s *ServerSpec) platformVariant() *PlatformVariant {
	if len(s.Platforms) == 0 {
		return nil
	}
	for _, key := range []string{runtime.GOOS + "/" + runtime.GOARCH, runtime.GOOS, "default"} {
		if v, ok := s.Platforms[key]; ok {
			return &v
		}
	}
	return nil
}

// no launch strategy succeeds, without inspecting a server ID.
func (s *ServerSpec) resolutionHint(installAllowed bool) string {
	strategies := []string{"PATH binary"}
	if s.Npx != nil {
		strategies = append(strategies, "npx package "+s.Npx.Package)
	}
	if s.Install != nil {
		strategies = append(strategies, "automatic "+s.Install.Kind+" installation")
		if !installAllowed {
			strategies = append(strategies, "automatic installation disabled")
		}
	}
	return strings.Join(strategies, ", ")
}

func localBinary(workspace, binDir, command string) string {
	if filepath.IsAbs(command) {
		if fileExists(command) {
			return command
		}
		return ""
	}
	for _, dir := range localBinaryDirs(workspace, binDir) {
		candidate := filepath.Join(dir, binName(command))
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func localBinaryDirs(workspace, binDir string) []string {
	return []string{filepath.Join(workspace, ".goa", "bin"), binDir, filepath.Join(workspace, "node_modules", ".bin")}
}

// npxArgv builds the npx fallback argv for Node-based servers. The --package
// form is used because the runnable bin often differs from the package name
// (pyright→pyright-langserver, @vue/language-server→vue-language-server, …)
// and extra packages may be required (typescript-language-server needs
// typescript@5 — v6+ dropped lib/tsserver.js).
func (s *ServerSpec) npxArgv() ([]string, bool) {
	if s.Npx == nil || s.Npx.Package == "" {
		return nil, false
	}
	npxBin, err := lookPath("npx")
	if err != nil {
		return nil, false
	}
	bin := s.Npx.Binary
	if bin == "" {
		bin = s.Npx.Package
	}
	argv := []string{npxBin, "--yes", "--package", s.Npx.Package}
	for _, extra := range s.Npx.ExtraPackages {
		argv = append(argv, "--package", extra)
	}
	argv = append(argv, bin)
	argv = append(argv, s.Npx.Args...)
	return argv, true
}

// run executes the installer, returning the path to the installed binary.
// Installations are idempotent: an existing binary in binDir is reused.
func (in *InstallSpec) run(binDir string) (string, error) {
	switch in.Kind {
	case "go":
		return installGo(in.Package, in.Binary, binDir)
	case "dotnet":
		return installDotnet(in.Package, in.Binary, binDir, in.Args...)
	case "npm":
		return installNpm(in.Package, in.Binary, binDir)
	case "download":
		return installDownload(in.URL, in.Binary, binDir, in.Checksum)
	default:
		return "", fmt.Errorf("lsp: unknown install kind %q", in.Kind)
	}
}
