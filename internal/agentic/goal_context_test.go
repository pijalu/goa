package agentic

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func assertGoalSystemClean(t *testing.T, pctx provider.Context) {
	t.Helper()
	if strings.Contains(pctx.SystemPrompt, "STATIC GOAL REMINDER") || strings.Contains(pctx.SystemPrompt, "DYNAMIC PROGRESS LINE") {
		t.Errorf("goal text in system prompt: %q", pctx.SystemPrompt)
	}
	for index, message := range pctx.Messages {
		if message.Role != provider.RoleSystem {
			continue
		}
		for _, content := range message.Content {
			if strings.Contains(content.Text, "STATIC GOAL REMINDER") || strings.Contains(content.Text, "DYNAMIC PROGRESS LINE") {
				t.Errorf("goal text in system message %d: %q", index, content.Text)
			}
		}
	}
}
