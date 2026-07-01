// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
)

func TestBuildBundle_IncludesAllArtifacts(t *testing.T) {
	dir := t.TempDir()
	setupTestProject(t, dir)

	ctx := core.Context{
		Config: &config.Config{
			ConfigDir:      filepath.Join(dir, ".goa"),
			ActiveProvider: "openai",
			ActiveModel:    "gpt-4o",
			Logging: config.LoggingConfig{
				File: filepath.Join(dir, "goa.log"),
			},
		},
		ProjectDir: dir,
		SessionStore: &fakeSessionStore{
			sessionID:   "test_session",
			sessionPath: filepath.Join(dir, ".goa", "sessions", "test_session.jsonl"),
		},
		RenderChat: func(width int) string { return "chat line" },
	}

	result, err := BuildBundle(ctx, BuildOptions{IssueDescription: "bug"})
	if err != nil {
		t.Fatalf("BuildBundle failed: %v", err)
	}

	entries := readZipEntries(t, result.Path)
	want := []string{
		"config/project.yaml",
		"config/user.yaml",
		"logs/goa.log",
		"manifest.json",
		"README.md",
		"session.md",
		"session/events.jsonl",
		"system/info.json",
	}
	for _, w := range want {
		if !entries[w] {
			t.Errorf("missing zip entry: %s", w)
		}
	}

	logData, err := readZipFile(t, result.Path, "logs/goa.log")
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(logData), "sk-live-secret") {
		t.Errorf("agent log was not redacted")
	}

	cfgData, err := readZipFile(t, result.Path, "config/project.yaml")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(cfgData), "sk-config-secret") {
		t.Errorf("project config was not redacted")
	}
}

func TestBuildBundle_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	ctx := core.Context{
		Config: &config.Config{
			ConfigDir: filepath.Join(dir, ".goa"),
		},
		ProjectDir: dir,
	}

	result, err := BuildBundle(ctx, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildBundle failed: %v", err)
	}

	if len(result.Manifest.MissingFiles) == 0 {
		t.Errorf("expected missing files, got none")
	}
}

func TestBuildBundle_ManifestSchema(t *testing.T) {
	dir := t.TempDir()
	setupTestProject(t, dir)

	ctx := core.Context{
		Config: &config.Config{
			ConfigDir:      filepath.Join(dir, ".goa"),
			ActiveProvider: "openai",
			ActiveModel:    "gpt-4o",
		},
		ProjectDir: dir,
		SessionStore: &fakeSessionStore{
			sessionID:   "s1",
			sessionPath: filepath.Join(dir, ".goa", "sessions", "s1.jsonl"),
		},
		RenderChat: func(width int) string { return "" },
	}

	result, err := BuildBundle(ctx, BuildOptions{IssueDescription: "bug"})
	if err != nil {
		t.Fatalf("BuildBundle failed: %v", err)
	}

	if result.Manifest.GoaVersion == "" {
		t.Error("manifest.GoaVersion is empty")
	}
	if result.Manifest.ExportedAt == "" {
		t.Error("manifest.ExportedAt is empty")
	}
	if result.Manifest.IssueDescription != "bug" {
		t.Errorf("manifest.IssueDescription = %q, want bug", result.Manifest.IssueDescription)
	}
	if result.Manifest.Files.ProjectConfig != "config/project.yaml" {
		t.Errorf("manifest.Files.ProjectConfig = %q", result.Manifest.Files.ProjectConfig)
	}
}

func TestBuildBundle_IncludesUserModes(t *testing.T) {
	dir := t.TempDir()
	setupTestProject(t, dir)
	modeDir := filepath.Join(dir, ".goa", "prompts", "mode", "custom")
	if err := os.MkdirAll(modeDir, 0o755); err != nil {
		t.Fatalf("mkdir mode dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modeDir, "definition.md"), []byte("# Custom\n"), 0o644); err != nil {
		t.Fatalf("write mode: %v", err)
	}

	ctx := core.Context{
		Config: &config.Config{
			ConfigDir:      filepath.Join(dir, ".goa"),
			ActiveProvider: "openai",
			ActiveModel:    "gpt-4o",
		},
		ProjectDir: dir,
		SessionStore: &fakeSessionStore{
			sessionID:   "s1",
			sessionPath: filepath.Join(dir, ".goa", "sessions", "s1.jsonl"),
		},
		RenderChat: func(width int) string { return "" },
	}

	result, err := BuildBundle(ctx, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildBundle failed: %v", err)
	}

	if result.Manifest.Files.Modes != "prompts/mode" {
		t.Errorf("manifest.Files.Modes = %q, want prompts/mode", result.Manifest.Files.Modes)
	}

	entries := readZipEntries(t, result.Path)
	if !entries["prompts/mode/custom/definition.md"] {
		t.Errorf("missing zip entry for user mode definition")
	}
}

func setupTestProject(t *testing.T, dir string) {
	t.Helper()
	goaDir := filepath.Join(dir, ".goa")
	sessionDir := filepath.Join(goaDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	projectCfg := `model: gpt-4o
openai:
  api_key: sk-config-secret
`
	if err := os.WriteFile(filepath.Join(goaDir, "config.yaml"), []byte(projectCfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goaDir, "config.local.yaml"), []byte("local: true\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "test_session.jsonl"), []byte(`{"type":"content"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goa.log"), []byte("Authorization: Bearer sk-live-secret\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func readZipEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()

	entries := map[string]bool{}
	for _, f := range r.File {
		entries[f.Name] = true
	}
	return entries
}

func readZipFile(t *testing.T, path, name string) ([]byte, error) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("entry %q not found", name)
}

type fakeSessionStore struct {
	sessionID   string
	sessionPath string
}

func (f *fakeSessionStore) ListSessions() ([]core.SessionInfo, error) { return nil, nil }
func (f *fakeSessionStore) LoadSession(name string) ([]agentic.OutputEvent, error) {
	return nil, nil
}
func (f *fakeSessionStore) SaveCurrent(name string) error               { return nil }
func (f *fakeSessionStore) DeleteSession(name string) error             { return nil }
func (f *fakeSessionStore) ImportSession(name, sourcePath string) error { return nil }
func (f *fakeSessionStore) SessionID() string                           { return f.sessionID }
func (f *fakeSessionStore) CurrentSessionPath() string                  { return f.sessionPath }
