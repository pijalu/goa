//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/skillrunner"
)

//go:embed skills/*
//go:embed skills/*/*
//go:embed skills/*/*/*
var testSkillsFS embed.FS

// mockProvider implements provider.Api for fast, deterministic testing.
type mockProvider struct {
	responses map[string][]provider.AssistantMessageEvent
}

func (m *mockProvider) API() provider.Api {
	return provider.Api("test-mock")
}

func (m *mockProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		evts := m.responses[model.ID]
		for _, e := range evts {
			result.Push(e)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "mock done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (m *mockProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return m.Stream(model, ctx, base)
}

func TestDeepSkillHierarchy(t *testing.T) {
	loader := skillrunner.NewEmbeddedSkillsLoader(testSkillsFS, "skills")
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:  loader,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}

	genSent := runner.GetSkill("genSentence")
	if genSent == nil {
		t.Fatal("genSentence skill not found")
	}
	if len(genSent.SubSkills) != 3 {
		t.Fatalf("Expected 3 sub-skills, got %d", len(genSent.SubSkills))
	}

	subNames := make(map[string]bool)
	for _, sub := range genSent.SubSkills {
		subNames[sub.Name] = true
	}
	if !subNames["genSubject"] {
		t.Error("Expected genSubject as sub-skill")
	}
	if !subNames["genVerb"] {
		t.Error("Expected genVerb as sub-skill")
	}
	if !subNames["genObject"] {
		t.Error("Expected genObject as sub-skill")
	}

	if runner.GetSkill("genSubject") != nil {
		t.Error("genSubject should not be a top-level skill")
	}
	if runner.GetSkill("genVerb") != nil {
		t.Error("genVerb should not be a top-level skill")
	}
	if runner.GetSkill("genObject") != nil {
		t.Error("genObject should not be a top-level skill")
	}
}

func TestDeepSkillComposition(t *testing.T) {
	// This test requires a real LLM or a properly configured mock.
	// The mock setup is complex for the multi-turn skill composition.
	// See TestSubSkillIndividualExecution for isolated sub-skill tests.
	t.Skip("Deep skill composition requires real LLM or advanced mock — run manually with AGENTIC_E2E_URL")
}

func TestSubSkillIndividualExecution(t *testing.T) {
	loader := skillrunner.NewEmbeddedSkillsLoader(testSkillsFS, "skills")
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:  loader,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}

	genSent := runner.GetSkill("genSentence")
	if genSent == nil {
		t.Fatal("genSentence skill not found")
	}
	subRunner, err := skillrunner.NewRunner(skillrunner.Config{
		Skills:   genSent.SubSkills,
		WorkDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Failed to create sub-runner: %v", err)
	}

	tests := []struct {
		name       string
		skillName  string
		task       string
		wantSubset string
	}{
		{
			name:       "genSubject",
			skillName:  "genSubject",
			task:       `{"seed": "test"}`,
			wantSubset: `subject`,
		},
		{
			name:       "genVerb",
			skillName:  "genVerb",
			task:       `{"form": "third-person-singular"}`,
			wantSubset: `verb`,
		},
		{
			name:       "genObject",
			skillName:  "genObject",
			task:       `{"verb": "chases"}`,
			wantSubset: `object`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"skill_name": %q, "task": %q}`, tt.skillName, tt.task)
			result, err := subRunner.Execute(input)
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if !strings.Contains(result, tt.wantSubset) {
				t.Errorf("Result %q does not contain %q", result, tt.wantSubset)
			}
		})
	}
}

func TestE2EWithLLM(t *testing.T) {
	mockModel, err := newE2EModel()
	if err != nil {
		t.Skip(err)
	}

	loader := skillrunner.NewEmbeddedSkillsLoader(testSkillsFS, "skills")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	t.Run("generate_sentence", func(t *testing.T) {
		assertSentenceGeneration(t, ctx, loader, mockModel, "Use the genSentence skill to generate a sentence.")
	})

	t.Run("generate_another_sentence", func(t *testing.T) {
		assertSentenceGeneration(t, ctx, loader, mockModel, "Use the genSentence skill to generate a completely different sentence.")
	})
}

func newE2EModel() (provider.Model, error) {
	llmURL := os.Getenv("AGENTIC_E2E_URL")
	if llmURL == "" {
		return provider.Model{}, fmt.Errorf("set AGENTIC_E2E_URL to run LLM-backed E2E tests")
	}
	llmModel := os.Getenv("AGENTIC_E2E_MODEL")
	if llmModel == "" {
		llmModel = "llama-3.2-1b-instruct"
	}
	return provider.Model{
		ID:       llmModel,
		Name:     llmModel,
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderCustom,
		BaseURL:  llmURL,
	}, nil
}

func assertSentenceGeneration(t *testing.T, ctx context.Context, loader *skillrunner.EmbeddedSkillsLoader, mockModel provider.Model, prompt string) {
	t.Helper()
	agent, err := skillrunner.NewAgentWithSkillsLoader(
		agentic.Config{
			Model:        mockModel,
			SystemPrompt: "You are a helpful sentence generator.",
			Logger:       agentic.NewLogger(agentic.Warn),
		},
		loader,
		t.TempDir(),
	)
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	if err := agent.Run(ctx, prompt); err != nil {
		t.Fatalf("Agent run failed: %v", err)
	}
	agent.Stop()

	result := drainLastAssistantContent(agent.Output)
	if result == "" {
		t.Fatal("No sentence generated")
	}
	words := strings.Fields(result)
	if len(words) < 3 {
		t.Errorf("Sentence too short (%d words): %q", len(words), result)
	}
	t.Logf("Generated sentence: %s", result)
}

func drainLastAssistantContent(output <-chan agentic.OutputEvent) string {
	var result string
	for msg := range output {
		if msg.Type == agentic.Content && msg.Role == agentic.Assistant && msg.Content != "" {
			result = msg.Content
		}
	}
	return result
}

func TestSkillSchemaValidation(t *testing.T) {
	loader := skillrunner.NewEmbeddedSkillsLoader(testSkillsFS, "skills")
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:  loader,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}

	schema := runner.Schema()
	if schema.Name != "run_skill" {
		t.Errorf("Schema name = %q, want %q", schema.Name, "run_skill")
	}

	props := schema.Schema["properties"].(map[string]interface{})
	skillNameProp := props["skill_name"].(map[string]interface{})
	enum := skillNameProp["enum"].([]string)
	if len(enum) != 1 || enum[0] != "genSentence" {
		t.Errorf("Top-level enum = %v, want [genSentence]", enum)
	}

	for _, name := range enum {
		if name == "genSubject" || name == "genVerb" || name == "genObject" {
			t.Errorf("Sub-skill %q should not appear in top-level schema", name)
		}
	}
}

func TestGenerateSkillsSectionContent(t *testing.T) {
	loader := skillrunner.NewEmbeddedSkillsLoader(testSkillsFS, "skills")
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:  loader,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}

	section := runner.GenerateSkillsSection()
	if section == "" {
		t.Fatal("GenerateSkillsSection returned empty string")
	}

	if !strings.Contains(section, "genSentence") {
		t.Error("Skills section missing genSentence")
	}
	if !strings.Contains(section, "genSubject") {
		t.Error("Skills section missing genSubject")
	}
	if !strings.Contains(section, "genVerb") {
		t.Error("Skills section missing genVerb")
	}
	if !strings.Contains(section, "genObject") {
		t.Error("Skills section missing genObject")
	}
}

func TestJSONOutputValidation(t *testing.T) {
	subRunner := newGenSentenceSubRunner(t)
	for _, tt := range jsonValidationCases() {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"skill_name": %q, "task": %q}`, tt.skillName, tt.task)
			result, err := subRunner.Execute(input)
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			tt.validate(t, result)
		})
	}
}

func newGenSentenceSubRunner(t *testing.T) *skillrunner.Runner {
	t.Helper()
	loader := skillrunner.NewEmbeddedSkillsLoader(testSkillsFS, "skills")
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:  loader,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}
	genSent := runner.GetSkill("genSentence")
	if genSent == nil {
		t.Fatal("genSentence skill not found")
	}
	subRunner, err := skillrunner.NewRunner(skillrunner.Config{
		Skills:  genSent.SubSkills,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Failed to create sub-runner: %v", err)
	}
	return subRunner
}

func jsonValidationCases() []struct {
	name      string
	skillName string
	task      string
	validate  func(t *testing.T, result string)
} {
	return []struct {
		name      string
		skillName string
		task      string
		validate  func(t *testing.T, result string)
	}{
		{
			name:      "genSubject_returns_valid_json",
			skillName: "genSubject",
			task:      `{"seed": "test"}`,
			validate:  validateGenSubject,
		},
		{
			name:      "genVerb_returns_valid_json",
			skillName: "genVerb",
			task:      `{"form": "third-person-singular"}`,
			validate:  validateGenVerb,
		},
		{
			name:      "genObject_returns_valid_json",
			skillName: "genObject",
			task:      `{"verb": "chases"}`,
			validate:  validateGenObject,
		},
	}
}

func validateGenSubject(t *testing.T, result string) {
	var out struct {
		Subject string `json:"subject"`
		Form    string `json:"form"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Errorf("Invalid JSON: %v", err)
	}
	if out.Subject == "" {
		t.Error("Missing subject field")
	}
}

func validateGenVerb(t *testing.T, result string) {
	var out struct {
		Verb string `json:"verb"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Errorf("Invalid JSON: %v", err)
	}
	if out.Verb == "" {
		t.Error("Missing verb field")
	}
}

func validateGenObject(t *testing.T, result string) {
	var out struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Errorf("Invalid JSON: %v", err)
	}
	if out.Object == "" {
		t.Error("Missing object field")
	}
}
