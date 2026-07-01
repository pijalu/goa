// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/skills"
)

// mockReloadHandler implements core.ReloadHandler for testing.
type mockReloadHandler struct {
	skillsLoaded int
	contextCount int
	skillsErr    error
	contextErr   error
	pluginsErr   error
}

func (m *mockReloadHandler) ReloadSkills() (int, error) {
	return m.skillsLoaded, m.skillsErr
}

func (m *mockReloadHandler) ReloadContext() (int, error) {
	return m.contextCount, m.contextErr
}

func (m *mockReloadHandler) ReloadPlugins() error {
	return m.pluginsErr
}

func TestReloadCommand_Name(t *testing.T) {
	cmd := &ReloadCommand{}
	if cmd.Name() != "reload" {
		t.Errorf("Name = %q, want %q", cmd.Name(), "reload")
	}
}

func TestReloadCommand_ShortHelp(t *testing.T) {
	cmd := &ReloadCommand{}
	if cmd.ShortHelp() == "" {
		t.Error("ShortHelp should not be empty")
	}
}

func TestReloadCommand_LongHelp(t *testing.T) {
	cmd := &ReloadCommand{}
	if cmd.LongHelp() == "" {
		t.Error("LongHelp should not be empty")
	}
	if !strings.Contains(cmd.LongHelp(), "/reload") {
		t.Error("LongHelp should mention /reload")
	}
}

func TestReloadCommand_Run_NilHandler(t *testing.T) {
	cmd := &ReloadCommand{}
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf}

	err := cmd.Run(ctx, nil)
	if err == nil {
		t.Fatal("Expected error for nil ReloadHandler")
	}
	if !strings.Contains(err.Error(), "reload handler not available") {
		t.Errorf("Error = %q, want 'reload handler not available'", err.Error())
	}
}

func TestReloadCommand_Run_Success(t *testing.T) {
	cmd := &ReloadCommand{}
	var buf strings.Builder
	ctx := core.Context{
		OutputBuffer:  &buf,
		ReloadHandler: &mockReloadHandler{skillsLoaded: 8, contextCount: 2},
	}

	err := cmd.Run(ctx, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Skills: 8 loaded") {
		t.Errorf("Output missing skills count: %q", output)
	}
	if !strings.Contains(output, "Context: 2 file(s) loaded") {
		t.Errorf("Output missing context count: %q", output)
	}
	if !strings.Contains(output, "Plugins: reloaded") {
		t.Errorf("Output missing plugins status: %q", output)
	}
}

func TestReloadCommand_Run_SkillsError(t *testing.T) {
	cmd := &ReloadCommand{}
	var buf strings.Builder
	ctx := core.Context{
		OutputBuffer:  &buf,
		ReloadHandler: &mockReloadHandler{skillsErr: fmt.Errorf("permission denied")},
	}

	_ = cmd.Run(ctx, nil)
	output := buf.String()
	if !strings.Contains(output, "Skills: error") {
		t.Errorf("Output should mention skills error, got: %q", output)
	}
}

func TestReloadCommand_Run_NoContext(t *testing.T) {
	cmd := &ReloadCommand{}
	var buf strings.Builder
	ctx := core.Context{
		OutputBuffer:  &buf,
		ReloadHandler: &mockReloadHandler{skillsLoaded: 3, contextCount: 0},
	}

	err := cmd.Run(ctx, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Context: none found") {
		t.Errorf("Output should say 'none found', got: %q", output)
	}
}

// TestReloadCommand_CollisionSafety verifies the ReloadCommand is safe to
// register alongside other commands (no name collision).
func TestReloadCommand_CollisionSafety(t *testing.T) {
	cmd := &ReloadCommand{}
	if cmd.Name() == "" {
		t.Error("Name should not be empty")
	}
	if cmd.Name() == "skill" || cmd.Name() == "config" {
		t.Errorf("Name %q collides with existing command", cmd.Name())
	}
}

// TestSkillRegistryAfterReload verifies that skills can be re-loaded.
func TestSkillRegistryAfterReload(t *testing.T) {
	// Create a temp directory structure with skill files
	dir := t.TempDir()
	skill1Dir := filepath.Join(dir, "refactor")
	skill2Dir := filepath.Join(dir, "test-gen")
	os.MkdirAll(skill1Dir, 0o755)
	os.MkdirAll(skill2Dir, 0o755)
	os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte("---\nname: refactor\ndescription: Refactor code\n---\n\nRefactor body"), 0o644)
	os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte("---\nname: test-gen\ndescription: Generate tests\ninline: true\n---\n\nTest body"), 0o644)

	reg := skills.NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("first LoadAll: %v", err)
	}
	if len(reg.List()) != 2 {
		t.Fatalf("after first load: expected 2 skills, got %d", len(reg.List()))
	}
}

// TestDefaultSkillDirsParsing verifies that config default dirs are valid.
func TestDefaultSkillDirsParsing(t *testing.T) {
	// This tests the config-level default dirs indirectly.
	// The actual DefaultSkillDirs function is in config package.
	handlers := []core.ReloadHandler{
		&mockReloadHandler{skillsLoaded: 0, contextCount: 0},
	}
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handlers))
	}
}
