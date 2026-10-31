// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const cleanupHomeFixture = `# home configuration
active_provider: shared
active_model: shared-model
providers:
  - id: shared
    endpoint: https://home.example.invalid/v1
    api_key: home-secret
  - id: duplicate
    endpoint: https://duplicate.example.invalid/v1
    api_key: duplicate-secret
  - id: conflict
    endpoint: https://conflict.example.invalid/v1
    api_key: home-conflict-secret
models:
  - id: shared-model
    provider: shared
    model: home-model
  - id: duplicate-model
    provider: duplicate
    model: duplicate-remote
  - id: conflict-model
    provider: shared
    model: home-remote
`

const cleanupProjectFixture = `# project configuration — keep this comment
active_provider: shared
active_model: project-model
providers:
  - id: duplicate
    endpoint: https://duplicate.example.invalid/v1
    api_key: duplicate-secret
  - id: conflict
    endpoint: https://conflict.example.invalid/v1
    api_key: project-conflict-secret
  - id: project-only
    endpoint: https://project.example.invalid/v1
    api_key: project-only-secret
models:
  - id: duplicate-model
    provider: duplicate
    model: duplicate-remote
  - id: conflict-model
    provider: shared
    model: project-remote
  - id: project-model
    provider: project-only
    model: project-remote
execution:
  retries: 7 # unrelated project override
`

func cleanupFixture(t *testing.T) (*CascadeLoader, string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	homePath := filepath.Join(home, ".goa", "config.yaml")
	projectPath := filepath.Join(project, ".goa", "config.yaml")
	writeConfig(t, homePath, cleanupHomeFixture)
	writeConfig(t, projectPath, cleanupProjectFixture)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)
	return NewCascadeLoader(project, "", nil), homePath, projectPath, project
}

func cleanupCandidateKeys(plan *CleanupPlan) []string {
	keys := make([]string, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		keys = append(keys, candidate.Key)
	}
	return keys
}

func TestPlanProjectCleanup_ClassifiesInheritedEntries(t *testing.T) {
	loader, _, _, _ := cleanupFixture(t)
	plan, err := loader.PlanProjectCleanup()
	if err != nil {
		t.Fatalf("PlanProjectCleanup: %v", err)
	}

	wantKeys := []string{
		"active_provider", "active_model",
		"provider:duplicate", "provider:conflict",
		"model:duplicate-model", "model:conflict-model",
	}
	if got := cleanupCandidateKeys(plan); !slices.Equal(got, wantKeys) {
		t.Fatalf("candidate keys = %v, want %v", got, wantKeys)
	}
	byKey := make(map[string]CleanupCandidate, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		byKey[candidate.Key] = candidate
	}
	for _, key := range []string{"active_provider", "provider:duplicate", "model:duplicate-model"} {
		if byKey[key].Kind != CleanupRedundant {
			t.Errorf("%s kind = %q, want redundant", key, byKey[key].Kind)
		}
	}
	for _, key := range []string{"active_model", "provider:conflict", "model:conflict-model"} {
		if byKey[key].Kind != CleanupConflict {
			t.Errorf("%s kind = %q, want conflict", key, byKey[key].Kind)
		}
	}
	if got := byKey["provider:conflict"].Fields; !slices.Equal(got, []string{"api_key"}) {
		t.Errorf("provider conflict fields = %v, want only api_key", got)
	}
	if got := byKey["model:conflict-model"].Fields; !slices.Equal(got, []string{"model"}) {
		t.Errorf("model conflict fields = %v, want only model", got)
	}
}

func mustCleanupFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertCleanupContent(t *testing.T, content string, wanted, unwanted []string) {
	t.Helper()
	for _, value := range unwanted {
		if strings.Contains(content, value) {
			t.Errorf("approved entry %q remains:\n%s", value, content)
		}
	}
	for _, value := range wanted {
		if !strings.Contains(content, value) {
			t.Errorf("unrelated project data %q was not preserved:\n%s", value, content)
		}
	}
}

func TestCleanupPlanApply_ApprovedOnlyAndIdempotent(t *testing.T) {
	loader, homePath, projectPath, project := cleanupFixture(t)
	homeBefore := mustCleanupFile(t, homePath)
	projectBefore := mustCleanupFile(t, projectPath)
	localPath := filepath.Join(project, ".goa", "config.local.yaml")
	writeConfig(t, localPath, "active_model: local-model\n")

	plan, err := loader.PlanProjectCleanup()
	if err != nil {
		t.Fatal(err)
	}
	removed, err := plan.Apply([]string{"provider:duplicate", "provider:conflict", "model:duplicate-model"})
	if err != nil || removed != 3 {
		t.Fatalf("Apply = (%d, %v), want (3, nil)", removed, err)
	}

	content := mustCleanupFile(t, projectPath)
	assertCleanupContent(t, content, []string{
		"# project configuration — keep this comment",
		"active_model: project-model",
		"id: project-only",
		"project-only-secret",
		"id: project-model",
		"id: conflict-model",
		"retries: 7 # unrelated project override",
	}, []string{"id: duplicate\n", "id: conflict\n", "id: duplicate-model\n"})
	if homeAfter := mustCleanupFile(t, homePath); homeAfter != homeBefore {
		t.Error("home configuration changed")
	}
	if localAfter := mustCleanupFile(t, localPath); localAfter != "active_model: local-model\n" {
		t.Errorf("local configuration changed: %q", localAfter)
	}

	assertCleanedCascade(t, project)
	assertCleanupIdempotent(t, loader, projectPath, projectBefore, content)
}

func assertCleanedCascade(t *testing.T, project string) {
	t.Helper()
	reloaded, err := NewCascadeLoader(project, "", nil).Load()
	if err != nil {
		t.Fatalf("reload cleaned project: %v", err)
	}
	if reloaded.ActiveProvider != "shared" || reloaded.ActiveModel != "local-model" {
		t.Errorf("active selection = (%q, %q), want shared/local-model (local layer wins)", reloaded.ActiveProvider, reloaded.ActiveModel)
	}
	if provider := reloaded.GetProviderByID("conflict"); provider == nil || provider.APIKey != "home-conflict-secret" {
		t.Errorf("approved conflict did not inherit home provider: %+v", provider)
	}
}

func assertCleanupIdempotent(t *testing.T, loader *CascadeLoader, projectPath, before, after string) {
	t.Helper()
	second, err := loader.PlanProjectCleanup()
	if err != nil {
		t.Fatal(err)
	}
	wantRemaining := []string{"active_provider", "active_model", "model:conflict-model"}
	if got := cleanupCandidateKeys(second); !slices.Equal(got, wantRemaining) {
		t.Errorf("second-run candidates = %v, want %v", got, wantRemaining)
	}
	unchanged, err := second.Apply(nil)
	if err != nil || unchanged != 0 {
		t.Fatalf("empty Apply = (%d, %v), want (0, nil)", unchanged, err)
	}
	finalContent := mustCleanupFile(t, projectPath)
	if finalContent == before {
		t.Error("test precondition failed: approved cleanup never changed project file")
	}
	if finalContent != after {
		t.Error("empty Apply rewrote the project file")
	}
}

func TestCleanupPlanApply_PreservesLeadingCommentWhenFirstSettingRemoved(t *testing.T) {
	loader, _, projectPath, _ := cleanupFixture(t)
	plan, err := loader.PlanProjectCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply([]string{"active_provider"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "# project configuration — keep this comment") {
		t.Fatalf("leading file comment was lost with the first removed setting:\n%s", after)
	}
}

func TestCleanupPlanApply_PreservesLeadingCommentWhenCatalogEmptied(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOA_HOME", home)
	writeConfig(t, filepath.Join(home, ".goa", "config.yaml"), `providers:
  - id: shared
    provider_type: openai
`)
	projectPath := filepath.Join(project, ".goa", "config.yaml")
	writeConfig(t, projectPath, `# project configuration — keep this comment
providers:
  - id: shared
    provider_type: openai
execution:
  retries: 7
`)
	plan, err := NewCascadeLoader(project, "", nil).PlanProjectCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply([]string{"provider:shared"}); err != nil {
		t.Fatal(err)
	}
	after := mustCleanupFile(t, projectPath)
	assertCleanupContent(t, after,
		[]string{"# project configuration — keep this comment", "retries: 7"},
		[]string{"providers:", "id: shared"})
}

func TestCleanupPlanApply_RefusesConcurrentChanges(t *testing.T) {
	loader, homePath, projectPath, _ := cleanupFixture(t)
	plan, err := loader.PlanProjectCleanup()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, homePath, cleanupHomeFixture+"# changed while the wizard was open\n")

	if _, err := plan.Apply([]string{"provider:duplicate"}); err == nil {
		t.Fatal("Apply succeeded after home configuration changed")
	}
	after, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("project changed despite stale cleanup plan")
	}
}

func TestPlanProjectCleanup_DuplicateCatalogIDUsesEffectiveEntry(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOA_HOME", home)
	writeConfig(t, filepath.Join(home, ".goa", "config.yaml"), `providers:
  - id: shared
    api_key: home-secret
`)
	writeConfig(t, filepath.Join(project, ".goa", "config.yaml"), `providers:
  - id: shared
    api_key: home-secret
  - id: shared
    api_key: project-secret
`)
	plan, err := NewCascadeLoader(project, "", nil).PlanProjectCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].Kind != CleanupConflict {
		t.Fatalf("candidates = %+v, want one conflict for the effective last entry", plan.Candidates)
	}
	if _, err := plan.Apply([]string{"provider:shared"}); err != nil {
		t.Fatal(err)
	}
	layer, err := NewCascadeLoader(project, "", nil).Load()
	if err != nil {
		t.Fatal(err)
	}
	if provider := layer.GetProviderByID("shared"); provider == nil || provider.APIKey != "home-secret" {
		t.Errorf("provider after cleanup = %+v, want inherited home entry", provider)
	}
}

func TestPlanProjectCleanup_SharedTopLevelSections(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOA_HOME", home)
	homePath := filepath.Join(home, ".goa", "config.yaml")
	projectPath := filepath.Join(project, ".goa", "config.yaml")
	writeConfig(t, homePath, `execution:
  retries: 3
context_compression:
  max_tokens: 12345
`)
	writeConfig(t, projectPath, `execution:
  retries: 7
context_compression:
  max_tokens: 12345
aliases:
  n: session:new
unknown_project_data:
  preserve: true
`)
	loader := NewCascadeLoader(project, "", nil)
	plan, err := loader.PlanProjectCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if got := cleanupCandidateKeys(plan); !slices.Equal(got, []string{"section:execution", "section:context_compression"}) {
		t.Fatalf("section candidates = %v", got)
	}
	if plan.Candidates[0].Kind != CleanupConflict || plan.Candidates[0].Type != "section" {
		t.Errorf("execution candidate = %+v, want conflicting section", plan.Candidates[0])
	}
	if plan.Candidates[1].Kind != CleanupRedundant {
		t.Errorf("compression candidate = %+v, want redundant", plan.Candidates[1])
	}
	if got := plan.Candidates[0].Fields; !slices.Equal(got, []string{"retries"}) {
		t.Errorf("execution differing fields = %v, want retries", got)
	}
	removed, err := plan.Apply([]string{"section:context_compression"})
	if err != nil || removed != 1 {
		t.Fatalf("Apply = (%d, %v), want (1, nil)", removed, err)
	}
	after, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	assertCleanupContent(t, string(after),
		[]string{"execution:", "retries: 7", "aliases:", "unknown_project_data:"},
		[]string{"context_compression:"})
}

func TestPlanProjectCleanup_NoHomeOrProjectData(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOA_HOME", home)
	loader := NewCascadeLoader(project, "", nil)
	plan, err := loader.PlanProjectCleanup()
	if err != nil {
		t.Fatalf("PlanProjectCleanup without files: %v", err)
	}
	if len(plan.Candidates) != 0 {
		t.Fatalf("candidates = %v, want none", cleanupCandidateKeys(plan))
	}
	if _, err := os.Stat(filepath.Join(project, ".goa", "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("planning created a project config: %v", err)
	}
}
