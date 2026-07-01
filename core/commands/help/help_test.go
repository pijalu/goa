// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package help

import (
	"embed"
	"strings"
	"testing"
)

//go:embed *.md
var testDocs embed.FS

func TestLongHelp_KnownCommands(t *testing.T) {
	cases := []struct {
		name    string
		wantSub string
	}{
		{"help", "Usage: /help"},
		{"mode", "Usage: /mode"},
		{"quit", "Usage: /quit"},
		{"goa", "Show information"},
		{"tools", "Usage: /tools"},
		{"workflows", "Usage: /workflows"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LongHelp(tc.name)
			if got == "" {
				t.Fatalf("LongHelp(%q) returned empty string", tc.name)
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("LongHelp(%q) does not contain %q; got:\n%s", tc.name, tc.wantSub, got)
			}
		})
	}
}

func TestLongHelp_EveryFileHasContent(t *testing.T) {
	files, err := testDocs.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".long.md") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ".long.md")
		got := LongHelp(name)
		if strings.TrimSpace(got) == "" {
			t.Errorf("LongHelp(%q) returned empty content for %s", name, f.Name())
		}
	}
}

func TestLongHelp_MissingFile(t *testing.T) {
	if got := LongHelp("definitely-not-a-command"); got != "" {
		t.Errorf("expected empty string for missing command, got %q", got)
	}
}

func TestLongHelp_EmptyName(t *testing.T) {
	if got := LongHelp(""); got != "" {
		t.Errorf("expected empty string for empty name, got %q", got)
	}
}

func TestLongHelp_Consistency(t *testing.T) {
	files, err := testDocs.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".long.md") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ".long.md")
		data, err := testDocs.ReadFile(f.Name())
		if err != nil {
			t.Errorf("ReadFile(%q): %v", f.Name(), err)
			continue
		}
		body := string(data)

		// Each help file should either show a Usage line or mention its command.
		mentionsCommand := strings.Contains(body, "/"+name)
		mentionsUsage := strings.Contains(body, "Usage:")
		if !mentionsCommand && !mentionsUsage {
			t.Errorf("%s should mention /%s or contain a Usage line", f.Name(), name)
		}
	}
}
