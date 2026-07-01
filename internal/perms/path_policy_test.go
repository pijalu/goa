// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"path/filepath"
	"testing"
)

func TestPathPolicy_SoloBlocksProtectedAndOutside(t *testing.T) {
	project := t.TempDir()
	policy := PathPolicy{ProjectDir: project, Autonomy: "solo"}

	cases := []struct {
		name   string
		tool   string
		input  string
		want   PathDecision
	}{
		{"read inside project", "read", `{"path":"main.go"}`, PathAllow},
		{"read protected .goa", "read", `{"path":".goa/config.yaml"}`, PathDeny},
		{"read protected .git", "read", `{"path":".git/config"}`, PathDeny},
		{"read absolute outside", "read", `{"path":"/etc/passwd"}`, PathDeny},
		{"bash inside project", "bash", `{"command":"ls"}`, PathAllow},
		{"bash outside project", "bash", `{"command":"cat /etc/passwd"}`, PathDeny},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.Decide(tc.tool, tc.input)
			if got != tc.want {
				t.Errorf("Decide(%q, %q) = %q, want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
}

func TestPathPolicy_YoloAllowsAll(t *testing.T) {
	policy := PathPolicy{ProjectDir: t.TempDir(), Autonomy: "yolo"}

	cases := []struct {
		name  string
		tool  string
		input string
	}{
		{"read protected", "read", `{"path":".goa/config.yaml"}`},
		{"read outside", "read", `{"path":"/etc/passwd"}`},
		{"bash outside", "bash", `{"command":"rm -rf /"}`},
		{"write outside", "write", `{"path":"/tmp/x","content":"y"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.Decide(tc.tool, tc.input); got != PathAllow {
				t.Errorf("Decide(%q, %q) = %q, want allow", tc.tool, tc.input, got)
			}
		})
	}
}

func TestPathPolicy_AskAllowsReadInside(t *testing.T) {
	project := t.TempDir()
	policy := PathPolicy{ProjectDir: project, Autonomy: "confirm"}

	cases := []struct {
		name  string
		tool  string
		input string
		want  PathDecision
	}{
		{"read inside project", "read", `{"path":"main.go"}`, PathAllow},
		{"read media inside", "read_media_file", `{"path":"img.png"}`, PathAllow},
		{"bash inside project", "bash", `{"command":"go test ./..."}`, PathAsk},
		{"write inside", "write", `{"path":"main.go","content":"x"}`, PathAsk},
		{"edit inside", "edit", `{"path":"main.go","operation":"replace"}`, PathAsk},
		{"read outside", "read", `{"path":"/etc/passwd"}`, PathAsk},
		{"bash outside", "bash", `{"command":"cat /etc/passwd"}`, PathAsk},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.Decide(tc.tool, tc.input)
			if got != tc.want {
				t.Errorf("Decide(%q, %q) = %q, want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
}

func TestIsProtectedPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".goa/config.yaml", true},
		{".git/config", true},
		{"foo/bar/.goa/x", true},
		{"foo.go", false},
		{"src/main.go", false},
		{"/etc/passwd", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsProtectedPath(tc.path); got != tc.want {
				t.Errorf("IsProtectedPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsUnderDir(t *testing.T) {
	project := t.TempDir()
	inside := filepath.Join(project, "main.go")

	if !isUnderDir(inside, project) {
		t.Errorf("expected %q to be under %q", inside, project)
	}
	if isUnderDir("/etc/passwd", project) {
		t.Errorf("expected %q not to be under %q", "/etc/passwd", project)
	}
}
