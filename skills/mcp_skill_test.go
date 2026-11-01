package skills

import (
	"testing"
)

// TestMCPSkillEmbeddedAndDefaultOff pins the B46 naming: the mcp skill
// (agent-mcp bridge authoring guide) ships embedded (run_skill catalog) and
// follows the every-embedded-skill-off-by-default policy — the model opts in
// via config, nothing MCP-related is advertised or sticky by default.
func TestMCPSkillEmbeddedAndDefaultOff(t *testing.T) {
	names := EmbeddedSkillNames(EmbeddedSkillsFS)
	found := false
	for _, n := range names {
		if n == "mcp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mcp skill not in embedded set: %v", names)
	}
	off := false
	for _, n := range DefaultEmbeddedOffNames(EmbeddedSkillsFS) {
		if n == "mcp" {
			off = true
		}
	}
	if !off {
		t.Error("mcp skill must be default-off")
	}
}
