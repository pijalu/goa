// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"slices"
	"testing"
)

func TestAnalyzer_EmptyCommand(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("")
	if err != nil {
		t.Fatalf("empty command should not error: %v", err)
	}
	if res.Destructive || res.Network || res.Interactive || res.TooComplex || res.Blocked {
		t.Errorf("empty command should have no flags set, got %+v", res)
	}
	if !res.Allowed {
		t.Errorf("empty command should be allowed when allowed list is empty")
	}
}

func TestAnalyzer_DestructiveCommand(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("rm -rf /tmp/foo")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Destructive {
		t.Errorf("expected destructive flag for rm command")
	}
	if res.Network || res.Interactive {
		t.Errorf("unexpected network/interactive flags, got %+v", res)
	}
	if !slices.Contains(res.Commands, "rm") {
		t.Errorf("expected commands to contain rm, got %v", res.Commands)
	}
}

func TestAnalyzer_NetworkCommand(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("curl -s https://example.com")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Network {
		t.Errorf("expected network flag for curl command")
	}
}

func TestAnalyzer_InteractiveCommand(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("vim /tmp/foo")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Interactive {
		t.Errorf("expected interactive flag for vim command")
	}
}

func TestAnalyzer_BlockedCommand(t *testing.T) {
	a := Analyzer{Blocked: []string{"rm", "curl"}}
	res, err := a.Analyze("rm -rf /")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Blocked {
		t.Errorf("expected blocked flag")
	}
}

func TestAnalyzer_AllowedCommand(t *testing.T) {
	a := Analyzer{Allowed: []string{"ls", "cat"}}
	res, err := a.Analyze("ls -la")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Allowed {
		t.Errorf("expected ls to be allowed, got %+v", res)
	}
}

func TestAnalyzer_NotAllowedCommand(t *testing.T) {
	a := Analyzer{Allowed: []string{"ls"}}
	res, err := a.Analyze("rm -rf /")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if res.Allowed {
		t.Errorf("expected rm not to be allowed")
	}
}

func TestAnalyzer_ChainedCommands(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("echo hello && rm /tmp/foo && curl example.com")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Destructive {
		t.Errorf("expected destructive flag")
	}
	if !res.Network {
		t.Errorf("expected network flag")
	}
	if len(res.Commands) != 3 {
		t.Errorf("expected 3 commands, got %d: %v", len(res.Commands), res.Commands)
	}
}

func TestAnalyzer_PipeCommands(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("cat file | grep foo | sort")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if len(res.Commands) != 3 {
		t.Errorf("expected 3 commands, got %d: %v", len(res.Commands), res.Commands)
	}
}

func TestAnalyzer_Subshell(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("$(echo rm -rf /tmp)")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.TooComplex {
		t.Errorf("expected command substitution to be too complex")
	}
}

func TestAnalyzer_InvalidShell(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("if then fi")
	if err != nil {
		t.Fatalf("analyze should not return error for invalid shell: %v", err)
	}
	if !res.TooComplex {
		t.Errorf("expected invalid shell to be too complex")
	}
}

func TestAnalyzer_PathCommand(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("/usr/bin/rm /tmp/foo")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Destructive {
		t.Errorf("expected destructive flag for /usr/bin/rm")
	}
	if !slices.Contains(res.Commands, "/usr/bin/rm") {
		t.Errorf("expected commands to contain /usr/bin/rm, got %v", res.Commands)
	}
}

func TestAnalyzer_EnvPrefix(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("FOO=bar rm /tmp/foo")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Destructive {
		t.Errorf("expected destructive flag with env prefix")
	}
}

func TestAnalyzer_ComplexityThreshold(t *testing.T) {
	a := Analyzer{MaxComplexityScore: 5}
	res, err := a.Analyze("echo a && echo b && echo c && echo d && echo e && echo f")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.TooComplex {
		t.Errorf("expected complexity to exceed threshold")
	}
}

func TestAnalyzer_ReasonNotEmpty(t *testing.T) {
	a := Analyzer{Blocked: []string{"rm"}}
	res, err := a.Analyze("rm -rf /")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if res.Reason == "" {
		t.Errorf("expected non-empty reason for blocked command")
	}
	if !containsString(res.Reason, "blocked") {
		t.Errorf("expected reason to mention blocked, got %q", res.Reason)
	}
}

func TestAnalyzer_CustomCategoryLists(t *testing.T) {
	a := Analyzer{
		DestructiveCommands: []string{"customdestroy"},
		NetworkCommands:     []string{"customnet"},
		InteractiveCommands: []string{"custominteractive"},
	}
	res, err := a.Analyze("customdestroy /foo && customnet http://x && custominteractive")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Destructive || !res.Network || !res.Interactive {
		t.Errorf("expected custom categories to match, got %+v", res)
	}
}

func TestAnalyzer_CaseInsensitiveBlocked(t *testing.T) {
	a := Analyzer{Blocked: []string{"rm"}}
	res, err := a.Analyze("RM /tmp/foo")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if res.Blocked {
		t.Errorf("blocked matching is case-sensitive; RM should not match rm")
	}
}

func TestAnalyzer_NoFalsePositiveOnAssignment(t *testing.T) {
	a := Analyzer{Blocked: []string{"rm"}}
	res, err := a.Analyze("x=rm")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if res.Blocked {
		t.Errorf("variable assignment should not trigger blocked command")
	}
}

func TestAnalyzer_DynamicCommand_AllowedList(t *testing.T) {
	a := Analyzer{Allowed: []string{"echo"}}
	res, err := a.Analyze("echo hello && $VAR world")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.TooComplex {
		t.Errorf("expected dynamic command to be flagged as too complex")
	}
	if res.Allowed {
		t.Errorf("dynamic command should not be allowed because it cannot be statically verified")
	}
	if len(res.Commands) != 1 {
		t.Errorf("expected only echo to be detected, got %v", res.Commands)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestAnalyzer_DeduplicatesCommands(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("echo a; echo b; echo c")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if len(res.Commands) != 1 {
		t.Errorf("expected 1 deduplicated command, got %v", res.Commands)
	}
}

func TestAnalyzer_ReasonWhenNoFindings(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("echo hello")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if res.Reason == "" {
		t.Errorf("expected reason even when no findings")
	}
	if !containsString(res.Reason, "score=") {
		t.Errorf("expected reason to contain score, got %q", res.Reason)
	}
}

func TestAnalyzer_AllowedEmptyMeansAllowAll(t *testing.T) {
	a := Analyzer{}
	res, err := a.Analyze("somecustomcommand")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Allowed {
		t.Errorf("empty allowed list should allow all commands")
	}
}

func TestAnalyzer_MultipleCommandsRequireAllAllowed(t *testing.T) {
	a := Analyzer{Allowed: []string{"echo"}}
	res, err := a.Analyze("echo hello && cat file")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if res.Allowed {
		t.Errorf("chained command with cat not allowed should be rejected")
	}
}

func TestAnalyzer_BaseNameMatching(t *testing.T) {
	a := Analyzer{DestructiveCommands: []string{"rm"}}
	res, err := a.Analyze("/usr/local/bin/rm /tmp/foo")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Destructive {
		t.Errorf("expected basename matching for /usr/local/bin/rm")
	}
}

func TestAnalyzer_NewAnalyzer(t *testing.T) {
	a := NewAnalyzer([]string{"rm"}, []string{"echo"})
	if a == nil {
		t.Fatal("NewAnalyzer returned nil")
	}
	res, err := a.Analyze("rm -rf /")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Blocked {
		t.Errorf("expected NewAnalyzer blocked list to work")
	}
	if res.Allowed {
		t.Errorf("expected NewAnalyzer allowed list to reject rm")
	}
}

func TestAnalyzer_NewAnalyzer_NoRestrictions(t *testing.T) {
	a := NewAnalyzer(nil, nil)
	if a == nil {
		t.Fatal("NewAnalyzer returned nil")
	}
	res, err := a.Analyze("echo hello")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.Allowed {
		t.Errorf("empty restrictions should allow everything")
	}
}

func TestAnalyzer_LiteralWord_NonLiteral(t *testing.T) {
	// Indirectly tested via dynamic command detection, but verify the
	// analyzer reports the command correctly.
	a := NewAnalyzer(nil, []string{"echo"})
	res, err := a.Analyze("echo $VAR")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if len(res.Commands) != 1 || res.Commands[0] != "echo" {
		t.Errorf("expected echo command, got %v", res.Commands)
	}
}

func TestAnalyzer_ComplexityScore_High(t *testing.T) {
	a := NewAnalyzer(nil, nil)
	a.MaxComplexityScore = 10
	res, err := a.Analyze("for i in 1 2 3; do echo $i; done")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if !res.TooComplex {
		t.Errorf("expected for-loop to exceed low complexity threshold")
	}
}

func TestAnalyzer_BackquoteCommandSubstitution(t *testing.T) {
	a := NewAnalyzer(nil, []string{"echo"})
	res, err := a.Analyze("echo `date`")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	// Backquote in argument position is not dynamic command construction.
	if res.TooComplex {
		t.Errorf("backquote in argument position should not be too complex")
	}
}

func TestAnalyzer_ArithmeticNotTooComplex(t *testing.T) {
	a := NewAnalyzer(nil, []string{"echo"})
	res, err := a.Analyze("echo $((1+1))")
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if res.TooComplex {
		t.Errorf("arithmetic expansion in argument should not be too complex")
	}
}
