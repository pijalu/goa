// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny helper for tests creating instruction files.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionTracker_AddedScope(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "pkg", "sub", "AGENTS.md"), "nested instructions")

	tracker := NewInstructionTracker(project, nil)
	changes := tracker.Reconcile(filepath.Join(project, "pkg", "sub", "main.go"))

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.Action != InstructionAdded {
		t.Errorf("Action = %q, want %q", c.Action, InstructionAdded)
	}
	if c.Path != filepath.Join("pkg", "sub", "AGENTS.md") {
		t.Errorf("Path = %q, want %q", c.Path, filepath.Join("pkg", "sub", "AGENTS.md"))
	}
	if !strings.Contains(c.Content, "nested instructions") {
		t.Errorf("Content missing text: %q", c.Content)
	}
	if !strings.Contains(RenderInstructionMessage(c), "Additional instructions from") {
		t.Errorf("rendered message missing header: %q", RenderInstructionMessage(c))
	}
}

func TestInstructionTracker_NoChangeOnSecondTouch(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "pkg", "sub", "AGENTS.md"), "nested instructions")

	tracker := NewInstructionTracker(project, nil)
	first := tracker.Reconcile(filepath.Join(project, "pkg", "sub", "main.go"))
	if len(first) != 1 {
		t.Fatalf("expected 1 change first, got %d", len(first))
	}
	second := tracker.Reconcile(filepath.Join(project, "pkg", "sub", "other.go"))
	if len(second) != 0 {
		t.Fatalf("expected 0 changes on unchanged state, got %d: %+v", len(second), second)
	}
}

func TestInstructionTracker_UpdatedScope(t *testing.T) {
	project := t.TempDir()
	agentsPath := filepath.Join(project, "pkg", "AGENTS.md")
	writeFile(t, agentsPath, "version one")

	tracker := NewInstructionTracker(project, nil)
	if changes := tracker.Reconcile(filepath.Join(project, "pkg", "a.go")); len(changes) != 1 {
		t.Fatalf("expected 1 added, got %d", len(changes))
	}

	writeFile(t, agentsPath, "version two — changed instructions")
	changes := tracker.Reconcile(filepath.Join(project, "pkg", "b.go"))
	if len(changes) != 1 {
		t.Fatalf("expected 1 change after edit, got %d: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.Action != InstructionUpdated {
		t.Errorf("Action = %q, want %q", c.Action, InstructionUpdated)
	}
	if !strings.Contains(RenderInstructionMessage(c), "Updated instructions from") {
		t.Errorf("rendered message missing header: %q", RenderInstructionMessage(c))
	}
	if !strings.Contains(c.Content, "version two") {
		t.Errorf("Content not updated: %q", c.Content)
	}
}

func TestInstructionTracker_RemovedScope(t *testing.T) {
	project := t.TempDir()
	agentsPath := filepath.Join(project, "pkg", "AGENTS.md")
	writeFile(t, agentsPath, "nested instructions")

	tracker := NewInstructionTracker(project, nil)
	if changes := tracker.Reconcile(filepath.Join(project, "pkg", "a.go")); len(changes) != 1 {
		t.Fatalf("expected 1 added, got %d", len(changes))
	}

	if err := os.Remove(agentsPath); err != nil {
		t.Fatal(err)
	}
	changes := tracker.Reconcile(filepath.Join(project, "pkg", "b.go"))
	if len(changes) != 1 {
		t.Fatalf("expected 1 change after removal, got %d: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.Action != InstructionRemoved {
		t.Errorf("Action = %q, want %q", c.Action, InstructionRemoved)
	}
	if !strings.Contains(RenderInstructionMessage(c), "Instructions removed") {
		t.Errorf("rendered message missing header: %q", RenderInstructionMessage(c))
	}
}

func TestInstructionTracker_ByteIdenticalSiblingsLoadOnce(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, "pkg")
	content := "# same content for both\n"
	writeFile(t, filepath.Join(dir, "AGENTS.md"), content)
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), content)

	tracker := NewInstructionTracker(project, nil)
	changes := tracker.Reconcile(filepath.Join(dir, "main.go"))

	if len(changes) != 1 {
		t.Fatalf("byte-identical siblings must load once, got %d changes: %+v", len(changes), changes)
	}
	if c := changes[0]; c.Path != filepath.Join("pkg", "AGENTS.md") {
		t.Errorf("earlier candidate should win, got %q", c.Path)
	}
	// A second touch stays quiet — the sibling is a persistent duplicate.
	if again := tracker.Reconcile(filepath.Join(dir, "other.go")); len(again) != 0 {
		t.Errorf("expected no changes on repeat, got %+v", again)
	}
}

func TestInstructionTracker_DistinctSiblingsBothLoad(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, "pkg")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# agents content")
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "# claude content")

	tracker := NewInstructionTracker(project, nil)
	changes := tracker.Reconcile(filepath.Join(dir, "main.go"))

	if len(changes) != 2 {
		t.Fatalf("distinct siblings should both load, got %d changes: %+v", len(changes), changes)
	}
}

func TestInstructionTracker_BecomesDuplicateRemovesLaterCandidate(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, "pkg")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# agents content")
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "# claude content")

	tracker := NewInstructionTracker(project, nil)
	if changes := tracker.Reconcile(filepath.Join(dir, "a.go")); len(changes) != 2 {
		t.Fatalf("expected 2 added, got %d", len(changes))
	}

	// Make CLAUDE.md a byte-identical duplicate of AGENTS.md.
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "# agents content")
	changes := tracker.Reconcile(filepath.Join(dir, "b.go"))
	if len(changes) != 1 {
		t.Fatalf("expected 1 removal (duplicate), got %d: %+v", len(changes), changes)
	}
	if c := changes[0]; c.Action != InstructionRemoved || c.Path != filepath.Join("pkg", "CLAUDE.md") {
		t.Errorf("expected CLAUDE.md removal, got %+v", c)
	}
}

func TestInstructionTracker_BaselineSeeded(t *testing.T) {
	project := t.TempDir()
	agentsPath := filepath.Join(project, "AGENTS.md")
	writeFile(t, agentsPath, "project root instructions")

	baseline := LoadProjectContextFiles(project, "")
	if len(baseline) != 1 {
		t.Fatalf("expected 1 baseline file, got %d", len(baseline))
	}
	tracker := NewInstructionTracker(project, baseline)

	// Touching the project root must NOT re-report the baseline as added.
	changes := tracker.Reconcile(filepath.Join(project, "main.go"))
	if len(changes) != 0 {
		t.Fatalf("baseline scope should not be re-added, got %+v", changes)
	}

	// Editing the baseline file surfaces an update.
	writeFile(t, agentsPath, "project root instructions v2")
	changes = tracker.Reconcile(filepath.Join(project, "main.go"))
	if len(changes) != 1 || changes[0].Action != InstructionUpdated {
		t.Fatalf("expected 1 updated for edited baseline, got %+v", changes)
	}
}

func TestInstructionTracker_RelativeTouchPath(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "pkg", "AGENTS.md"), "nested")

	tracker := NewInstructionTracker(project, nil)
	changes := tracker.Reconcile(filepath.Join("pkg", "main.go"))
	if len(changes) != 1 {
		t.Fatalf("expected 1 change for relative path, got %d", len(changes))
	}
}

func TestInstructionTracker_SymlinkToFileLoads(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, "pkg")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "nested instructions")
	if err := os.Symlink(filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	tracker := NewInstructionTracker(project, nil)
	changes := tracker.Reconcile(filepath.Join(dir, "main.go"))
	// The symlink is a byte-identical duplicate of AGENTS.md → loads once.
	if len(changes) != 1 {
		t.Fatalf("expected 1 change (symlink duplicate), got %d: %+v", len(changes), changes)
	}
}

func TestInstructionTracker_SymlinkToDirIsAbsence(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, "pkg")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "nested")
	if err := os.MkdirAll(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	tracker := NewInstructionTracker(project, nil)
	changes := tracker.Reconcile(filepath.Join(dir, "main.go"))
	if len(changes) != 1 {
		t.Fatalf("link to directory is an absence; expected only AGENTS.md, got %d: %+v", len(changes), changes)
	}
	if changes[0].Path != filepath.Join("pkg", "AGENTS.md") {
		t.Errorf("expected AGENTS.md only, got %+v", changes[0])
	}
}

func TestRenderInstructionMessage_RemovedShape(t *testing.T) {
	msg := RenderInstructionMessage(InstructionChange{
		Action: InstructionRemoved,
		Path:   "pkg/AGENTS.md",
	})
	for _, want := range []string{
		"<system-reminder>",
		"Instructions removed: pkg/AGENTS.md",
		"The previously loaded instructions from this file no longer apply.",
		"</system-reminder>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestRenderInstructionMessage_AdditionalShape(t *testing.T) {
	msg := RenderInstructionMessage(InstructionChange{
		Action:  InstructionAdded,
		Path:    "pkg/sub/AGENTS.md",
		Content: "nested guidance",
	})
	for _, want := range []string{
		"<system-reminder>",
		"Additional instructions from: pkg/sub/AGENTS.md",
		"apply to work under `pkg/sub`",
		"nested guidance",
		"</system-reminder>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestInstructionTracker_NoProjectDirIsNoop(t *testing.T) {
	tracker := NewInstructionTracker("", nil)
	if changes := tracker.Reconcile("/tmp/x.go"); len(changes) != 0 {
		t.Errorf("expected no changes with empty project dir, got %+v", changes)
	}
}
