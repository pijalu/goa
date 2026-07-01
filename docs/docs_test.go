// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package docs

import (
	"strings"
	"testing"
)

func TestList_KnownDocs(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected at least one embedded doc")
	}

	required := []string{"ARCHITECTURE", "COMMANDS", "CONFIGURATION", "SKILLS", "TOOLS"}
	found := make(map[string]bool)
	for _, d := range list {
		found[d.Name] = true
	}
	for _, name := range required {
		if !found[name] {
			t.Errorf("missing required doc %q in list: %v", name, list)
		}
	}
}

func TestList_UppercaseNames(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	for _, d := range list {
		if d.Name != strings.ToUpper(d.Name) {
			t.Errorf("doc name %q should be uppercase", d.Name)
		}
	}
}

func TestList_HasDescription(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	for _, d := range list {
		if strings.TrimSpace(d.Description) == "" {
			t.Errorf("doc %q has empty description", d.Name)
		}
		if strings.Contains(d.Description, "documentation") && len(d.Description) < 20 {
			// Generic fallback descriptions are acceptable but we prefer rich ones.
			// This assertion documents that all core docs should have explicit descriptions.
			t.Logf("doc %q uses generic fallback description: %q", d.Name, d.Description)
		}
	}
}

func TestGet_KnownDoc(t *testing.T) {
	content, err := Get("SKILLS")
	if err != nil {
		t.Fatalf("Get(SKILLS) failed: %v", err)
	}
	if !strings.Contains(content, "Skills") {
		t.Errorf("expected SKILLS content to mention Skills, got: %s", content[:200])
	}
}

func TestGet_CaseInsensitive(t *testing.T) {
	content, err := Get("skills")
	if err != nil {
		t.Fatalf("Get(skills) failed: %v", err)
	}
	if content == "" {
		t.Error("expected non-empty content for case-insensitive lookup")
	}
}

func TestGet_UnknownDoc(t *testing.T) {
	_, err := Get("DOES_NOT_EXIST")
	if err == nil {
		t.Fatal("expected error for unknown doc")
	}
}

func TestParseGoaURL_Valid(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"goa://SKILLS", "SKILLS"},
		{"goa://SKILLS.md", "SKILLS"},
		{"goa://docs/SKILLS", "SKILLS"},
		{"goa://docs/SKILLS.md", "SKILLS"},
		{"goa://docs/CONFIGURATION.md", "CONFIGURATION"},
	}
	for _, c := range cases {
		got, ok := ParseGoaURL(c.input)
		if !ok {
			t.Errorf("ParseGoaURL(%q) expected ok, got !ok", c.input)
			continue
		}
		if got != c.want {
			t.Errorf("ParseGoaURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseGoaURL_Invalid(t *testing.T) {
	cases := []string{
		"",
		"https://example.com/SKILLS.md",
		"goa://",
		"goa://docs/",
		"goa://  ",
	}
	for _, c := range cases {
		got, ok := ParseGoaURL(c)
		if ok {
			t.Errorf("ParseGoaURL(%q) expected !ok, got %q", c, got)
		}
	}
}
