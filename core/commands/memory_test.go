// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/memory"
)

// fakeMemoryStore is a test double for core.MemoryStore.
type fakeMemoryStore struct {
	files   []memory.MemoryFileInfo
	content map[string]string
	deleted []string
}

func newFakeMemoryStore() *fakeMemoryStore {
	return &fakeMemoryStore{
		content: make(map[string]string),
	}
}

func (s *fakeMemoryStore) List() ([]memory.MemoryFileInfo, error) { return s.files, nil }
func (s *fakeMemoryStore) Read(name string) (string, error) {
	if c, ok := s.content[name]; ok {
		return c, nil
	}
	return "", fmt.Errorf("memory file %q not found", name)
}
func (s *fakeMemoryStore) Write(name, content string) error {
	s.content[name] = content
	return nil
}
func (s *fakeMemoryStore) Append(name, section, content string) error {
	s.content[name] += content
	return nil
}
func (s *fakeMemoryStore) Delete(name string) error {
	s.deleted = append(s.deleted, name)
	delete(s.content, name)
	return nil
}
func (s *fakeMemoryStore) HasConsolidated() bool { return false }
func (s *fakeMemoryStore) ReadConsolidated() (string, error) {
	return "", fmt.Errorf("no consolidated memory")
}

// TestListMemoryFiles_WithStore verifies file listing through the narrow interface.
func TestListMemoryFiles_WithStore(t *testing.T) {
	store := newFakeMemoryStore()
	store.files = []memory.MemoryFileInfo{
		{Name: "context.md", Preview: "project goals"},
		{Name: "todos.md", Preview: "pending tasks"},
	}
	var buf strings.Builder

	err := listMemoryFiles(store, &testOutputWriter{buf: &buf})
	if err != nil {
		t.Fatalf("listMemoryFiles failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "context.md") {
		t.Errorf("output missing context.md: %q", out)
	}
	if !strings.Contains(out, "project goals") {
		t.Errorf("output missing preview: %q", out)
	}
}

// TestListMemoryFiles_NoStore prints the built-in fallback list.
func TestListMemoryFiles_NoStore(t *testing.T) {
	var buf strings.Builder

	err := listMemoryFiles(nil, &testOutputWriter{buf: &buf})
	if err != nil {
		t.Fatalf("listMemoryFiles failed: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"context.md", "decisions.md", "todos.md", "notes.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback output missing %s: %q", want, out)
		}
	}
}

// TestShowMemoryFile_NotFound is graceful when a file does not exist.
func TestShowMemoryFile_NotFound(t *testing.T) {
	store := newFakeMemoryStore()
	var buf strings.Builder

	err := showMemoryFile(store, &testOutputWriter{buf: &buf}, []string{"show", "missing"})
	if err != nil {
		t.Fatalf("showMemoryFile failed: %v", err)
	}

	if !strings.Contains(buf.String(), "Error:") {
		t.Errorf("expected error message, got %q", buf.String())
	}
}

// TestShowMemoryFile_ListAll lists all memory files when no name is given.
func TestShowMemoryFile_ListAll(t *testing.T) {
	store := newFakeMemoryStore()
	store.files = []memory.MemoryFileInfo{
		{Name: "context.md", Preview: "project goals"},
		{Name: "todos.md", Preview: "pending tasks"},
	}
	var buf strings.Builder

	err := showMemoryFile(store, &testOutputWriter{buf: &buf}, []string{"show"})
	if err != nil {
		t.Fatalf("showMemoryFile failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "context.md") {
		t.Errorf("output missing context.md: %q", out)
	}
	if !strings.Contains(out, "todos.md") {
		t.Errorf("output missing todos.md: %q", out)
	}
}

// TestShowMemoryFile_MultipleParams shows several files separated by semicolons.
func TestShowMemoryFile_MultipleParams(t *testing.T) {
	store := newFakeMemoryStore()
	store.content["context.md"] = "project goals"
	store.content["todos.md"] = "pending tasks"
	var buf strings.Builder

	cmd := &MemoryCommand{}
	ctx := core.Context{MemoryStore: store, OutputBuffer: &buf}
	err := cmd.Run(ctx, []string{"show", "context.md;todos.md"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "project goals") {
		t.Errorf("output missing context content: %q", out)
	}
	if !strings.Contains(out, "pending tasks") {
		t.Errorf("output missing todos content: %q", out)
	}
}

// TestMemoryCommand_CompleteArgs_Files lists memory files after a subcommand.
func TestMemoryCommand_CompleteArgs_Files(t *testing.T) {
	store := newFakeMemoryStore()
	store.files = []memory.MemoryFileInfo{
		{Name: "context.md"},
		{Name: "todos.md"},
	}
	ctx := core.Context{MemoryStore: store}
	cmd := &MemoryCommand{}

	comps := cmd.CompleteArgs(ctx, "show:")
	if len(comps) != 2 {
		t.Fatalf("expected 2 file completions, got %d", len(comps))
	}
	if comps[0].Value != "context.md" && comps[1].Value != "context.md" {
		t.Errorf("expected context.md in completions: %v", comps)
	}
}

// TestClearMemoryFile_DeletesFile verifies deletion through the narrow interface.
func TestClearMemoryFile_DeletesFile(t *testing.T) {
	store := newFakeMemoryStore()
	store.content["todos.md"] = "- do thing"
	var buf strings.Builder

	err := clearMemoryFile(store, &testOutputWriter{buf: &buf}, []string{"clear", "todos.md"})
	if err != nil {
		t.Fatalf("clearMemoryFile failed: %v", err)
	}

	if len(store.deleted) != 1 || store.deleted[0] != "todos.md" {
		t.Errorf("deleted = %v, want [todos.md]", store.deleted)
	}
	if !strings.Contains(buf.String(), "Cleared memory 'todos.md'") {
		t.Errorf("missing success message: %q", buf.String())
	}
}

// testOutputWriter is a tiny OutputWriter for unit tests.
type testOutputWriter struct {
	buf *strings.Builder
}

func (w *testOutputWriter) Writef(format string, args ...interface{}) {
	w.buf.WriteString(fmt.Sprintf(format, args...))
}
