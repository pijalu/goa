// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package prompts

import (
	"testing"
	"testing/fstest"
)

func TestRegistry_Load_Embedded(t *testing.T) {
	fs := fstest.MapFS{
		"test.md": {Data: []byte("hello {{.Name}}")},
	}
	reg := NewRegistry(fs, "", "")

	got, err := reg.Load("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello {{.Name}}" {
		t.Errorf("expected raw template, got %q", got)
	}
}

func TestRegistry_Load_Missing(t *testing.T) {
	fs := fstest.MapFS{}
	reg := NewRegistry(fs, "", "")

	_, err := reg.Load("nonexistent")
	if err == nil {
		t.Error("expected error for missing prompt")
	}
}

func TestRegistry_Render(t *testing.T) {
	fs := fstest.MapFS{
		"greet.md": {Data: []byte("hello {{.Name}}")},
	}
	reg := NewRegistry(fs, "", "")

	got, err := reg.Render("greet", map[string]string{"Name": "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestRegistry_Render_Missing(t *testing.T) {
	fs := fstest.MapFS{}
	reg := NewRegistry(fs, "", "")

	_, err := reg.Render("missing", nil)
	if err == nil {
		t.Error("expected error for missing prompt render")
	}
}

func TestRegistry_MustLoad(t *testing.T) {
	fs := fstest.MapFS{
		"test.md": {Data: []byte("ok")},
	}
	reg := NewRegistry(fs, "", "")

	got, err := reg.MustLoad("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Errorf("expected 'ok', got %q", got)
	}
}

func TestRegistry_MustLoad_Missing(t *testing.T) {
	fs := fstest.MapFS{}
	reg := NewRegistry(fs, "", "")

	_, err := reg.MustLoad("missing")
	if err == nil {
		t.Error("expected error for missing prompt")
	}
}

func TestRegistry_List(t *testing.T) {
	fs := fstest.MapFS{
		"a.md":     {Data: []byte("a")},
		"b.md":     {Data: []byte("b")},
		"sub/c.md": {Data: []byte("c")},
	}
	reg := NewRegistry(fs, "", "")

	names, err := reg.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 prompts, got %d", len(names))
	}
	// Should include nested files with path
	found := false
	for _, n := range names {
		if n == "sub/c" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'sub/c' in list")
	}
}

func TestRegistry_Source(t *testing.T) {
	fs := fstest.MapFS{
		"test.md": {Data: []byte("test")},
	}
	reg := NewRegistry(fs, "", "")

	if reg.Source("test") != "embedded" {
		t.Errorf("expected 'embedded', got %q", reg.Source("test"))
	}
	if reg.Source("missing") != "missing" {
		t.Errorf("expected 'missing', got %q", reg.Source("missing"))
	}
}
