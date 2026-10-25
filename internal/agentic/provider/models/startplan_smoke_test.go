package models

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// Smoke: the curated Start Plan models must surface through the registry so
// /models and provider selection see them without extra config.
func TestStartPlanModelsRegistered(t *testing.T) {
	for _, p := range []schema.Provider{schema.ProviderZaiStartPlan, schema.ProviderBigModelStartPlan} {
		t.Run(string(p), func(t *testing.T) {
			assertStartPlanModels(t, p)
		})
	}
}

// assertStartPlanModels pins the per-provider catalog contract: exactly the
// three builtin Start Plan models, all reasoning-capable, with a context
// window at or above the zai floor (131072).
func assertStartPlanModels(t *testing.T, p schema.Provider) {
	t.Helper()
	models := GetModels(p)
	if len(models) != 3 {
		t.Fatalf("GetModels(%s) = %d models, want 3: %+v", p, len(models), models)
	}
	byID := map[string]schema.Model{}
	for _, m := range models {
		byID[m.ID] = m
	}
	for _, id := range []string{"glm-5.3-flash", "glm-5.2", "glm-5-turbo"} {
		m, ok := byID[id]
		if !ok {
			t.Fatalf("GetModels(%s) missing %q (have %v)", p, id, ids(models))
		}
		if !m.Reasoning {
			t.Errorf("%s/%s should be reasoning-capable", p, id)
		}
		if m.ContextWindow < 131072 {
			t.Errorf("%s/%s context window %d below zai floor", p, id, m.ContextWindow)
		}
	}
}

func ids(ms []schema.Model) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}
