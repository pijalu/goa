// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// testBase is the project root used in jail tests.
// Using a real directory so pathUnderDir's Abs resolves correctly.
var testBase string

func init() {
	testBase, _ = os.Getwd()
	// Fallback for tests running from other directories
	if testBase == "" {
		testBase = "/tmp/goa-test"
	}
}

func TestBashJail_Cd_SafePaths(t *testing.T) {
	// These commands cd to locations inside the project and should NOT trigger jail.
	tests := []struct {
		name string
		cmd  string
	}{
		{"cd to project root (absolute)", "cd " + testBase + " && find . -maxdepth 3 -type f -name \"*.go\" | head -10"},
		{"cd to subdir (relative) + chained command", "cd tools && ls -la"},
		{"cd with dot", "cd . && pwd"},
		{"cd with relative subdir", "cd ./internal && go build ./..."},
		{"cd with no args (goes home)", "cd && ls"},
		{"plain find with dot", "find . -name \"*.go\""},
		{"plain ls with relative path", "ls ./tools/bash_jail.go"},
		{"no paths at all", "echo hello world"},
		{"absolute path that IS the project", "ls " + testBase + "/tools/"},
		{"pipe with cd to safe dir", "cd ./tools | wc -l"},
		{"semicolon with cd to safe dir", "cd ./tools; ls"},
		{"cd with env var before it", "FOO=bar cd " + testBase + " && ls"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if bashReferencesOutsidePath(tt.cmd, testBase) {
				t.Errorf("bashReferencesOutsidePath(%q, %q) = true, want false", tt.cmd, testBase)
			}
		})
	}
}

func TestBashJail_Cd_UnsafePaths(t *testing.T) {
	// These commands cd to locations outside the project and SHOULD trigger jail.
	tests := []struct {
		name string
		cmd  string
	}{
		{"cd to parent", "cd .."},
		{"cd with ../ and command", "cd ../sibling && ls"},
		{"cd to grandparent", "cd ../../etc"},
		{"cd to absolute outside", "cd /tmp"},
		{"cd up via relative", "cd ./../../../etc/passwd"},
		{"pipe to unsafe cd", "cd /tmp | ls"},
		{"semicolon unsafe cd", "cd /tmp; ls"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !bashReferencesOutsidePath(tt.cmd, testBase) {
			t.Errorf("bashReferencesOutsidePath(%q, %q) = false, want true", tt.cmd, testBase)
			}
		})
	}
}

func TestBashJail_NonCd_UnsafePaths(t *testing.T) {
	// Commands without cd that reference paths outside the project.
	tests := []struct {
		name string
		cmd  string
	}{
		{"ls to parent", "ls .."},
		{"absolute path outside", "cat /etc/passwd"},
		{"relative path going up", "cat ../../file"},
		{"find outside", "find .. -name \"*.go\""},
		{"copy from outside", "cp ../outside/file.go ./"},
		{"mv from outside", "mv ../../file ./"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !bashReferencesOutsidePath(tt.cmd, testBase) {
				t.Errorf("bashReferencesOutsidePath(%q, %q) = false, want true", tt.cmd, testBase)
			}
		})
	}
}

func TestBashJail_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"empty command", "", false},
		{"just spaces", "   ", false},
		{"just newlines", "\n\n", false},
		{"cd with no target", "cd", false},
		{"cd with just spaces after", "cd   ", false},
		{"very long safe command", "cd " + testBase + " && find . -maxdepth 3 -type f \\( -name \"*.go\" -o -name \"*.md\" -o -name \"go.mod\" \\) | head -80 && echo done", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bashReferencesOutsidePath(tt.cmd, testBase)
			if got != tt.want {
				t.Errorf("bashReferencesOutsidePath(%q, %q) = %v, want %v", tt.cmd, testBase, got, tt.want)
			}
		})
	}
}

// TestBashJail_ExactRepro asserts the exact command from the bug report
// no longer triggers a false positive jail_violation.
func TestBashJail_ExactRepro(t *testing.T) {
	cmd := "cd " + testBase + " && find . -maxdepth 3 -type f -name \"*.go\" -o -name \"*.md\" -o -name \"go.mod\" | head -80"
	if bashReferencesOutsidePath(cmd, testBase) {
		t.Errorf("exact repro command should NOT trigger jail_violation:\n  cmd: %q\n  base: %q", cmd, testBase)
	}
}

// TestBashJail_ChainedCommands verifies that compound commands with &&
// and || are handled correctly.
func TestBashJail_ChainedCommands(t *testing.T) {
	t.Run("safe chain with cd to project", func(t *testing.T) {
		cmd := "cd " + testBase + " && go build ./... && go test ./..."
		if bashReferencesOutsidePath(cmd, testBase) {
			t.Error("safe chain with cd to project should not trigger jail")
		}
	})

	t.Run("unsafe chain with cd to parent", func(t *testing.T) {
		cmd := "cd ../other && go build ./..."
		if !bashReferencesOutsidePath(cmd, testBase) {
			t.Error("cd to parent should trigger jail")
		}
	})

	t.Run("safe chain with or operator", func(t *testing.T) {
		cmd := "cd " + testBase + " || echo fallback"
		if bashReferencesOutsidePath(cmd, testBase) {
			t.Error("safe chain with OR should not trigger jail")
		}
	})

	t.Run("redirect to devnull triggers jail", func(t *testing.T) {
		// /dev/null is outside the project directory, so the jail correctly
		// flags it. Only the exiting cd target is safe.
		cmd := "cd " + testBase + " && find . -name \"*.go\" > /dev/null"
		if !bashReferencesOutsidePath(cmd, testBase) {
			t.Error("redirect to /dev/null should trigger jail (outside project)")
		}
	})
}

// TestBashJail_LooksLikePath tests the path detection helper.
func TestBashJail_LooksLikePath(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want string
	}{
		{"empty", "", ""},
		{"just dot", ".", "."},
		{"dotdot", "..", ".."},
		{"absolute path", "/usr/bin", "/usr/bin"},
		{"relative dot", "./foo", "./foo"},
		{"relative up", "../foo", "../foo"},
		{"plain word", "hello", ""},
		{"command flag", "-maxdepth", ""},
		{"shell operator", "&&", ""},
		{"pipe", "|", ""},
		{"redirect", ">", ""},
		{"wildcard", "*.go", ""},
		{"tilde", "~/config", ""},
		{"variable", "$HOME", ""},
		{"double slash comment", "//", ""},
		{"triple slash", "///", ""},
		{"double slash with path", "//tmp", "//tmp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikePath(tt.s)
			if got != tt.want {
				t.Errorf("looksLikePath(%q) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

// TestBashJail_Subdir tests that references to subdirectories of the project
// are correctly treated as inside.
func TestBashJail_TildeNotDetected(t *testing.T) {
	// Tilde (~) is a shell expansion resolved at runtime. The static path
	// checker cannot resolve it, so cd ~ is not detected as outside. This
	// is an acceptable false negative — shell variables, tilde expansion,
	// and $VARIABLE paths are inherently unresolvable without execution.
	tests := []struct {
		name string
		cmd  string
	}{
		{"cd tilde", "cd ~"},
		{"cd tilde with subdir", "cd ~/.config"},
		{"cat tilde file", "cat ~/.bashrc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if bashReferencesOutsidePath(tt.cmd, testBase) {
				t.Logf("tilde %q not detected (expected — shell expansion", tt.cmd)
			}
		})
	}
}

func TestBashJail_Subdir(t *testing.T) {
	dir, err := os.MkdirTemp("", "goa-jail-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0755)

	// Reference to subdir should be safe
	if bashReferencesOutsidePath("cd "+sub, dir) {
		t.Error("cd to subdir should be safe")
	}
	if bashReferencesOutsidePath("ls "+sub, dir) {
		t.Error("ls to subdir should be safe")
	}

	// Reference to parent of dir should be unsafe
	parent := filepath.Dir(dir)
	if !bashReferencesOutsidePath("cd "+parent, dir) {
		t.Error("cd to parent should be unsafe")
	}
}

// TestBashJail_SlashSlashComment_NoFalsePositive verifies that "//" tokens
// inside a heredoc (e.g. Go comments) are not mistaken for absolute paths.
// This was the root cause of false-positive jail_violation errors when the
// agent generated commands that wrote code into the project.
func TestBashJail_SlashSlashComment_NoFalsePositive(t *testing.T) {
	dir := t.TempDir()
	cmd := `cd ` + dir + ` && cat > repro_test.go << 'EOF'
package repro

import "testing"

// This Go comment is a slash-slash token and is not a jail escape.
func TestRepro(t *testing.T) {
	t.Log("ok")
}
EOF
go test -timeout 10s -count=1 -run TestRepro ./... -v`

	if bashReferencesOutsidePath(cmd, dir) {
		t.Errorf("bashReferencesOutsidePath(%q) = true, want false (// comment in heredoc must not be treated as outside path)", cmd)
	}
}

// TestBashJail_RealAbsolutePaths_StillDetected makes sure the slash-only
// refinement does not accidentally allow real absolute paths outside the
// project.
func TestBashJail_RealAbsolutePaths_StillDetected(t *testing.T) {
	dir := t.TempDir()
	for _, cmd := range []string{
		"ls /tmp",
		"cat /etc/passwd",
		"cd /tmp && pwd",
		"find /tmp -name '*.go'",
	} {
		if !bashReferencesOutsidePath(cmd, dir) {
			t.Errorf("bashReferencesOutsidePath(%q) = false, want true", cmd)
		}
	}
}

func TestBashJail_QuotedPaths_NoFalsePositive(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name string
		cmd  string
	}{
		{"grep pattern in double quotes", `grep -rn "/wf" --include="*.go"`},
		{"path in echo string", `echo "=== /wf references ==="`},
		{"path in single quotes", `grep -rn '/wf' *.go`},
		{"multiple quoted args", `echo "output in /tmp" && grep "/pattern" file`},
		{"pipe with quoted grep", `cat file | grep "/usr/bin"`},
		{"find with quoted path-like string", `find . -name "*.go" -exec grep "/api/v1" {} +`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if bashReferencesOutsidePath(tt.cmd, dir) {
				t.Errorf("bashReferencesOutsidePath(%q) = true, want false (quoted /-token should not be treated as path)", tt.cmd)
			}
		})
	}
}

// TestBashJail_UnquotedPaths_StillCatchesRealPaths ensures that unquoted
// absolute paths outside the project are still correctly rejected even after
// the quote-aware tokenization change.
func TestBashJail_UnquotedPaths_StillCatchesRealPaths(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name string
		cmd  string
	}{
		{"unquoted absolute outside", "ls /etc"},
		{"unquoted absolute outside 2", "cat /var/log/syslog"},
		{"cd to absolute outside", "cd /tmp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !bashReferencesOutsidePath(tt.cmd, dir) {
				t.Errorf("bashReferencesOutsidePath(%q) = false, want true (unquoted absolute path outside should be rejected)", tt.cmd)
			}
		})
	}
}
