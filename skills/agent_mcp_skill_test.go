package skills

import (
	"testing"
)

// TestAgentMCPSkillEmbeddedAndDefaultOff pins the B43 deliverable: the
// agent_mcp skill ships embedded (run_skill catalog) and follows the
// every-embedded-skill-off-by-default policy — the model opts in via config,
// nothing MCP-related is advertised or sticky by default.
func TestAgentMCPSkillEmbeddedAndDefaultOff(t *testing.T) {
	names := EmbeddedSkillNames(EmbeddedSkillsFS)
	found := false
	for _, n := range names {
		if n == "agent_mcp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent_mcp skill not in embedded set: %v", names)
	}
	off := false
	for _, n := range DefaultEmbeddedOffNames(EmbeddedSkillsFS) {
		if n == "agent_mcp" {
			off = true
		}
	}
	if !off {
		t.Error("agent_mcp skill must be default-off")
	}
}
