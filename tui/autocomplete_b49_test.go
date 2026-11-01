// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B49 regression tests: the @ completer must expand a leading ~ to $HOME
// and decode file:// / percent-encoded paths before matching, so a pasted
// absolute path (spaces encoded as %20) completes. A decoded path that
// yields nothing falls back to the raw form so literal "%20" file names
// still complete.

func disableFDForB49(t *testing.T) {
	t.Helper()
	old := fdAvailable
	fdAvailable = false
	t.Cleanup(func() { fdAvailable = old })
}

func seedHomeB49(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plans/plan.md", "todo.txt"} {
		if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(name)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileCompleter_TildeExpandsToHome(t *testing.T) {
	disableFDForB49(t)
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedHomeB49(t, home)
	fc := NewFileCompleter(workdir)

	cases := []struct {
		name      string
		prefix    string
		wantCount int
	}{
		{"bare ~ lists home", "@~", 2},
		{"tilde slash lists home", "@~/", 2},
		{"tilde dir drills down", "@~/plans/", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fc.Complete(tc.prefix)
			if len(got) != tc.wantCount {
				t.Fatalf("Complete(%q) = %#v, want %d entries", tc.prefix, got, tc.wantCount)
			}
			for _, c := range got {
				if !strings.HasPrefix(c.Value, "@"+home) {
					t.Errorf("completion %q not rooted at HOME (%s)", c.Value, home)
				}
				if !strings.HasPrefix(c.Display, "@~/") {
					t.Errorf("display %q not abbreviated with ~", c.Display)
				}
			}
		})
	}
}

// TestFileCompleter_TildeDirYieldsPlan drills into ~/plans and gets the
// file completion with the CatFiles category.
func TestFileCompleter_TildeDirYieldsPlan(t *testing.T) {
	disableFDForB49(t)
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedHomeB49(t, home)

	got := NewFileCompleter(workdir).Complete("@~/plans/")
	if len(got) != 1 || got[0].Value != "@"+home+"/plans/plan.md" {
		t.Fatalf("Complete(\"@~/plans/\") = %#v, want @%s/plans/plan.md", got, home)
	}
	if got[0].Category != CatFiles {
		t.Errorf("category = %v, want CatFiles", got[0].Category)
	}
}

// TestFileCompleter_TildeWithoutHome: with HOME unset, ~ completes to
// nothing instead of searching a literal "~" directory.
func TestFileCompleter_TildeWithoutHome(t *testing.T) {
	disableFDForB49(t)
	t.Setenv("HOME", "")
	fc := NewFileCompleter(t.TempDir())
	if got := fc.Complete("@~/x"); len(got) != 0 {
		t.Errorf("Complete(\"@~/x\") with empty HOME = %#v, want none", got)
	}
}

func TestFileCompleter_EncodedPath(t *testing.T) {
	disableFDForB49(t)
	workdir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(workdir, "f g.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := NewFileCompleter(workdir)
	abs := filepath.ToSlash(workdir)

	cases := []struct {
		name   string
		prefix string
		want   string // "" = expect no completions
	}{
		{"percent-encoded space", "@f%20g", "@f g.md"},
		{"file URL", "@file://" + abs + "/f%20g", "@f g.md"},
		{"file URL localhost", "@file://localhost" + abs + "/f%20g", "@f g.md"},
		{"encoded exact file suppresses popup", "@file://" + abs + "/f%20g.md", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fc.Complete(tc.prefix)
			if tc.want == "" {
				if len(got) != 0 {
					t.Errorf("Complete(%q) = %#v, want none", tc.prefix, got)
				}
				return
			}
			if len(got) != 1 || got[0].Value != tc.want {
				t.Errorf("Complete(%q) = %#v, want [%s]", tc.prefix, got, tc.want)
			}
		})
	}
}

// TestFileCompleter_LiteralPercentName: a decoded prefix matching nothing
// falls back to the raw form, so a file literally named "f%20g.md" still
// completes.
func TestFileCompleter_LiteralPercentName(t *testing.T) {
	disableFDForB49(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f%20g.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := NewFileCompleter(dir).Complete("@f%20g")
	if len(got) != 1 || got[0].Value != "@f%20g.md" {
		t.Fatalf("got %#v, want [@f%%20g.md]", got)
	}
}
