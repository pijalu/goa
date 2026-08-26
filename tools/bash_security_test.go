// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/internal/secrets"
)

func TestBashTool_Execute_BlockedCommand_ReturnsError(t *testing.T) {
	tool := &BashTool{
		Blocked: []string{"rm -rf /"},
	}
	_, err := tool.Execute(`{"command": "rm -rf /"}`)
	if err == nil {
		t.Error("Execute with blocked command should return error")
	}
}

func TestBashTool_Execute_NotAllowedCommand_ReturnsError(t *testing.T) {
	tool := &BashTool{
		Allowed: []string{"ls", "echo"},
	}
	_, err := tool.Execute(`{"command": "rm file"}`)
	if err == nil {
		t.Error("Execute with non-allowed command should return error")
	}
}

func TestBashTool_Jail_RejectsParentDirectory(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "ls .."}`)
	if err == nil {
		t.Fatal("Expected jail violation for ls ..")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestBashTool_Jail_RejectsAbsoluteOutside(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "cat /etc/passwd"}`)
	if err == nil {
		t.Fatal("Expected jail violation for absolute outside path")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestBashTool_Jail_RejectsCdOutside(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "cd /tmp"}`)
	if err == nil {
		t.Fatal("Expected jail violation for cd outside")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestBashTool_Jail_AllowsInsideProject(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	result, err := tool.Execute(`{"command": "pwd"}`)
	if err != nil {
		t.Fatalf("Expected pwd to succeed: %v", err)
	}
	if !strings.Contains(result, dir) {
		t.Errorf("Expected pwd output to contain project dir, got: %q", result)
	}
}

func TestBashTool_Jail_Disabled(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: false}
	_, err := tool.Execute(`{"command": "ls .."}`)
	if err != nil {
		t.Fatalf("Expected ls .. to succeed when jail is disabled: %v", err)
	}
}

func TestBashTool_Jail_RejectsOutsideWorkdir(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "pwd", "workdir": "/tmp"}`)
	if err == nil {
		t.Fatal("Expected jail violation for outside workdir")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestCheckBlocked(t *testing.T) {
	tests := []struct {
		name     string
		blocked  []string
		cmd      string
		wantFail bool
	}{
		{"no blocked list", nil, "rm -rf /", false},
		{"empty blocked list", []string{}, "rm -rf /", false},
		{"exact match blocked", []string{"rm"}, "rm -rf /", true},
		{"not in blocked list", []string{"mkfs"}, "rm -rf /", false},
		{"substring not matched", []string{"rm -rf /"}, "rm file", false},
		{"first token match", []string{"sudo"}, "sudo rm -rf /", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &BashTool{Blocked: tt.blocked}
			err := tool.checkBlocked(tt.cmd)
			if tt.wantFail && err == nil {
				t.Errorf("checkBlocked(%q) should return error", tt.cmd)
			}
			if !tt.wantFail && err != nil {
				t.Errorf("checkBlocked(%q) should not return error, got: %v", tt.cmd, err)
			}
		})
	}
}

func TestBashTool_Jail_HeredocWithSlashSlashComment(t *testing.T) {
	// The static jail checker previously treated bare "//" tokens (e.g. Go
	// comments inside a heredoc) as absolute paths and rejected the command.
	// This test runs the real BashTool with the jail enabled to verify the
	// command is allowed and executes successfully.
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	cmd := `{"command": "cd ` + dir + ` && cat > repro.go << 'EOF'\npackage repro\n// This Go comment is a slash-slash token.\nimport \"fmt\"\nfunc main() { fmt.Println(\"ok\") }\nEOF\ncat repro.go"}`

	result, err := tool.Execute(cmd)
	if err != nil {
		t.Fatalf("heredoc with // comments should not trigger jail: %v", err)
	}
	if !strings.Contains(result, "This Go comment") {
		t.Errorf("expected file content in output, got: %q", result)
	}
}

func TestBashTool_Analyzer_BlocksCommand(t *testing.T) {
	tool := &BashTool{
		Analyzer: sandbox.NewAnalyzer([]string{"rm"}, nil),
	}
	_, err := tool.Execute(`{"command": "FOO=bar rm -rf /tmp"}`)
	if err == nil {
		t.Fatal("expected analyzer to block rm command")
	}
	if !strings.Contains(err.Error(), "blocked_command") {
		t.Errorf("expected blocked_command error, got: %v", err)
	}
}

func TestBashTool_Analyzer_EnforcesAllowedList(t *testing.T) {
	tool := &BashTool{
		Analyzer: sandbox.NewAnalyzer(nil, []string{"echo"}),
	}
	_, err := tool.Execute(`{"command": "cat file"}`)
	if err == nil {
		t.Fatal("expected analyzer to reject cat command")
	}
	if !strings.Contains(err.Error(), "command_not_allowed") {
		t.Errorf("expected command_not_allowed error, got: %v", err)
	}
}

func TestBashTool_Analyzer_CatchesObfuscatedBlocked(t *testing.T) {
	tool := &BashTool{
		Analyzer: sandbox.NewAnalyzer([]string{"rm"}, nil),
	}
	// firstCommandToken extracts "rm" directly here, but the analyzer also
	// sees it clearly. The main value is that env prefixes and chained
	// commands are parsed rather than regexed.
	_, err := tool.Execute(`{"command": "echo clean && rm -rf /tmp"}`)
	if err == nil {
		t.Fatal("expected analyzer to block chained rm command")
	}
	if !strings.Contains(err.Error(), "blocked_command") {
		t.Errorf("expected blocked_command error, got: %v", err)
	}
}

func TestBashTool_Analyzer_RejectsDynamicCommand(t *testing.T) {
	tool := &BashTool{
		Analyzer: sandbox.NewAnalyzer(nil, []string{"echo"}),
	}
	_, err := tool.Execute(`{"command": "echo ok && $CMD"}`)
	if err == nil {
		t.Fatal("expected analyzer to reject dynamic command")
	}
	if !strings.Contains(err.Error(), "command_too_complex") {
		t.Errorf("expected command_too_complex error, got: %v", err)
	}
}

func TestBashTool_Analyzer_AllowedCommandSucceeds(t *testing.T) {
	tool := &BashTool{
		Analyzer: sandbox.NewAnalyzer(nil, []string{"echo"}),
	}
	result, err := tool.Execute(`{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("expected allowed command to succeed: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output to contain hello, got: %q", result)
	}
}

func TestBashTool_Analyzer_Nil_DoesNotAnalyze(t *testing.T) {
	tool := &BashTool{} // Analyzer is nil
	result, err := tool.Execute(`{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("expected command to succeed without analyzer: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output to contain hello, got: %q", result)
	}
}

func TestBashTool_Analyzer_HigherComplexityThresholdAllowsForLoop(t *testing.T) {
	cmd := `{"command": "for f in a b c d e f g h i j; do echo \"--- $f ---\" && echo \"$(echo $f)\" || echo \"(not tracked)\"; done"}`

	// With the default threshold, this for-loop is rejected.
	toolLow := &BashTool{
		Analyzer: &sandbox.Analyzer{Allowed: []string{"echo"}},
	}
	if _, err := toolLow.Execute(cmd); err == nil {
		t.Fatal("expected for-loop to be rejected with default complexity threshold")
	}

	// With an explicit higher threshold, the same command is allowed.
	toolHigh := &BashTool{
		Analyzer: &sandbox.Analyzer{
			Allowed:            []string{"echo"},
			MaxComplexityScore: 200,
		},
	}
	result, err := toolHigh.Execute(cmd)
	if err != nil {
		t.Fatalf("expected for-loop to pass with raised complexity threshold: %v", err)
	}
	if !strings.Contains(result, "--- a ---") {
		t.Errorf("expected loop output, got: %q", result)
	}
}

func TestBashTool_Analyzer_ReportsDestructiveCategory(t *testing.T) {
	tool := &BashTool{Analyzer: &sandbox.Analyzer{}}
	res, err := tool.Analyzer.Analyze("rm -rf /tmp")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Destructive {
		t.Errorf("expected rm to be flagged as destructive")
	}
}

// Redactor integration tests. These exercise the secret scanner wired into
// BashTool to scrub credentials from command output.

func TestBashTool_Redactor_RemovesSecrets(t *testing.T) {
	tool := &BashTool{
		Redactor: secrets.DefaultRedactor(),
	}
	key := "AKIAIOSFODNN7EXAMPLE"
	result, err := tool.Execute(fmt.Sprintf(`{"command": "echo %s"}`, key))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if strings.Contains(result, key) {
		t.Errorf("expected secret to be redacted, got: %q", result)
	}
	if !strings.Contains(result, "***") {
		t.Errorf("expected placeholder in output, got: %q", result)
	}
}
