// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// TestBuildBundle_CacheMissRequestsArtifact drives a real provider cache bust
// through the generic runtime and expects the debug bundle to carry the
// COMPLETE requests around the miss (the bust + the preceding call) in
// logs/cache_miss_requests.json — and, when no miss was detected, an empty
// array (requests are never logged wholesale).
func TestBuildBundle_CacheMissRequestsArtifact(t *testing.T) {
	provider.ResetCacheForensics()
	defer provider.ResetCacheForensics()
	old := transport.Default()
	defer transport.SetDefault(old)

	call := func(cachedTokens int) {
		transport.SetDefault(&cacheMissMockTransport{cached: cachedTokens})
		model := schema.Model{
			ID:       "kimi-k2",
			Api:      schema.ApiOpenAICompletions,
			Provider: schema.ProviderKimiCode,
			BaseURL:  "http://example.com/v1/chat/completions",
		}
		stream, err := provider.Stream(model,
			schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
			schema.StreamOptions{SessionID: "sess-1"})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		_ = stream.Result()
	}
	call(100) // establish the cache baseline
	call(0)   // bust

	deadline := time.Now().Add(2 * time.Second)
	for len(provider.CacheForensicsReports()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(provider.CacheForensicsReports()) != 1 {
		t.Fatalf("expected 1 forensics report, got %d", len(provider.CacheForensicsReports()))
	}

	dir := t.TempDir()
	setupTestProject(t, dir)
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

	data, err := readZipFile(t, result.Path, "logs/cache_miss_requests.json")
	if err != nil {
		t.Fatalf("cache-miss artifact missing from bundle: %v", err)
	}
	var reports []map[string]any
	if err := json.Unmarshal(data, &reports); err != nil {
		t.Fatalf("artifact is not a JSON array: %v\n%s", err, data)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 report in artifact, got %d", len(reports))
	}
	requests, _ := reports[0]["requests"].([]any)
	if len(requests) != 2 {
		t.Fatalf("report must hold the miss + the preceding request, got %d", len(requests))
	}
	missReq, _ := requests[1].(map[string]any)
	body, _ := missReq["body"].(map[string]any)
	if body["messages"] == nil {
		t.Errorf("miss request body is not the COMPLETE wire request: %v", body)
	}

	// No miss → empty array, never a wholesale request dump.
	provider.ResetCacheForensics()
	result, err = BuildBundle(ctx, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildBundle failed: %v", err)
	}
	data, err = readZipFile(t, result.Path, "logs/cache_miss_requests.json")
	if err != nil {
		t.Fatalf("artifact missing on empty journal: %v", err)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Errorf("empty journal must export an empty array, got: %s", data)
	}
}

// cacheMissMockTransport answers an OpenAI-completions SSE stream whose usage
// chunk reports cached tokens, enough for the journal's miss detection.
type cacheMissMockTransport struct {
	cached int
}

func (m *cacheMissMockTransport) Do(_ context.Context, _ *transport.TransportRequest) (*transport.TransportResponse, error) {
	body := `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n" +
		fmt.Sprintf(`data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":200,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":%d}}}`, m.cached) + "\n\n" +
		`data: [DONE]` + "\n\n"
	return &transport.TransportResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "text/event-stream"},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

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
func (f *fakeSessionStore) StartSessionWithID(id string) string         { return id }

// TestBuildBundle_IncludesContributorArtifacts verifies the Open/Closed
// extension point: a registered ArtifactContributor's artifacts are bundled
// (both inline Data and copied Path) without the bundler knowing their content.
func TestBuildBundle_IncludesContributorArtifacts(t *testing.T) {
	dir := t.TempDir()
	setupTestProject(t, dir)
	// Sentinel file so the contributor fires only for this test's dir and is a
	// harmless no-op for every other test sharing the package registry.
	if err := os.WriteFile(filepath.Join(dir, ".contrib-marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "contrib-source.txt")
	if err := os.WriteFile(srcPath, []byte("path-contributed"), 0o644); err != nil {
		t.Fatal(err)
	}
	RegisterContributor(func(projectDir string) []Artifact {
		if _, err := os.Stat(filepath.Join(projectDir, ".contrib-marker")); err != nil {
			return nil
		}
		return []Artifact{
			{Name: "contrib/data.txt", Data: []byte("data-contributed")},
			{Name: "contrib/copied.txt", Path: srcPath},
		}
	})

	ctx := core.Context{
		Config:     &config.Config{ConfigDir: filepath.Join(dir, ".goa")},
		ProjectDir: dir,
	}
	result, err := BuildBundle(ctx, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	entries := readZipEntries(t, result.Path)
	if !entries["contrib/data.txt"] || !entries["contrib/copied.txt"] {
		t.Errorf("contributor artifacts missing in bundle: %+v", entries)
	}
	if data, err := readZipFile(t, result.Path, "contrib/data.txt"); err != nil || string(data) != "data-contributed" {
		t.Errorf("contrib/data.txt = %q (%v), want data-contributed", string(data), err)
	}
	if data, err := readZipFile(t, result.Path, "contrib/copied.txt"); err != nil || string(data) != "path-contributed" {
		t.Errorf("contrib/copied.txt = %q (%v), want path-contributed", string(data), err)
	}
}
