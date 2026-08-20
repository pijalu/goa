// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/sandbox"
)

// schemaProps extracts the schema properties map for assertion.
func schemaProps(t *testing.T, tool *BashTool) map[string]any {
	t.Helper()
	schema := tool.Schema()
	props, ok := schema.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties is not a map: %T", schema.Schema["properties"])
	}
	return props
}

// TestBashSchema_NonSandboxed_HidesEscalationFields is the acceptance test for
// "non-sandboxed builds hide the fields": without jail (or without a project
// dir) the schema must not advertise sandbox_permissions/justification.
func TestBashSchema_NonSandboxed_HidesEscalationFields(t *testing.T) {
	cases := []struct {
		name string
		tool *BashTool
	}{
		{name: "zero value", tool: &BashTool{}},
		{name: "no jail", tool: &BashTool{ProjectDir: t.TempDir(), Jail: false}},
		{name: "jail no projectdir", tool: &BashTool{Jail: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := schemaProps(t, tc.tool)
			if _, ok := props["sandbox_permissions"]; ok {
				t.Error("sandbox_permissions must be hidden when confinement is inactive")
			}
			if _, ok := props["justification"]; ok {
				t.Error("justification must be hidden when confinement is inactive")
			}
			if _, ok := tc.tool.Schema().Schema["dependentRequired"]; ok {
				t.Error("dependentRequired must be hidden when confinement is inactive")
			}
		})
	}
}

// TestBashSchema_Sandboxed_AdvertisesEscalationFields verifies the confined
// schema advertises the closed escalation vocabulary and the required-with
// pairing.
func TestBashSchema_Sandboxed_AdvertisesEscalationFields(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	props := schemaProps(t, tool)
	permsVal, ok := props["sandbox_permissions"]
	if !ok {
		t.Fatal("sandbox_permissions must be advertised when confinement is active")
	}
	permsObj, ok := permsVal.(map[string]any)
	if !ok {
		t.Fatalf("sandbox_permissions is not an object: %T", permsVal)
	}
	enumVal, ok := permsObj["enum"]
	if !ok {
		t.Fatal("sandbox_permissions must declare an enum")
	}
	enum, ok := enumVal.([]string)
	if !ok {
		t.Fatalf("enum is not []string: %T", enumVal)
	}
	if len(enum) != len(sandbox.EscalationVocabulary) {
		t.Fatalf("enum = %v, want vocabulary %v", enum, sandbox.EscalationVocabulary)
	}
	for i, want := range sandbox.EscalationVocabulary {
		if enum[i] != want {
			t.Errorf("enum[%d] = %q, want %q", i, enum[i], want)
		}
	}
	if _, ok := props["justification"]; !ok {
		t.Error("justification must be advertised when confinement is active")
	}
	dep, ok := tool.Schema().Schema["dependentRequired"].(map[string]any)
	if !ok {
		t.Fatal("dependentRequired must be present when confinement is active")
	}
	if _, ok := dep["sandbox_permissions"]; !ok {
		t.Error("sandbox_permissions must require justification (dependentRequired)")
	}
	if _, ok := dep["justification"]; !ok {
		t.Error("justification must require sandbox_permissions (dependentRequired)")
	}
}

// TestBashEscalation_Validation covers the required-with pairing and the
// closed-vocabulary / availability checks.
func TestBashEscalation_Validation(t *testing.T) {
	dir := t.TempDir()
	confined := &BashTool{ProjectDir: dir, Jail: true}
	unconfined := &BashTool{ProjectDir: dir, Jail: false}
	inJailCmd := `{"command": "pwd"}`
	deniedCmd := fmt.Sprintf(`{"command": "cat /etc/passwd", "sandbox_permissions": %q, "justification": %q}`, sandbox.ModeDangerFullAccess, "need to read system info")

	cases := []struct {
		name     string
		tool     *BashTool
		input    string
		wantType string
	}{
		{
			name:     "perms without justification",
			tool:     confined,
			input:    `{"command": "cat /etc/passwd", "sandbox_permissions": "danger-full-access"}`,
			wantType: "invalid_escalation",
		},
		{
			name:     "justification without perms",
			tool:     confined,
			input:    `{"command": "cat /etc/passwd", "justification": "need it"}`,
			wantType: "invalid_escalation",
		},
		{
			name:     "empty justification",
			tool:     confined,
			input:    fmt.Sprintf(`{"command": "cat /etc/passwd", "sandbox_permissions": %q, "justification": "   "}`, sandbox.ModeDangerFullAccess),
			wantType: "invalid_escalation",
		},
		{
			name:     "unknown mode",
			tool:     confined,
			input:    `{"command": "cat /etc/passwd", "sandbox_permissions": "full-access-everything", "justification": "need it"}`,
			wantType: "invalid_escalation",
		},
		{
			name:     "escalation unavailable in non-sandboxed build",
			tool:     unconfined,
			input:    `{"command": "cat /etc/passwd", "sandbox_permissions": "danger-full-access", "justification": "need it"}`,
			wantType: "escalation_unavailable",
		},
		{
			name:     "escalation never speculative (in-jail command)",
			tool:     confined,
			input:    fmt.Sprintf(`{"command": "pwd", "sandbox_permissions": %q, "justification": %q}`, sandbox.ModeDangerFullAccess, "unnecessary"),
			wantType: "escalation_unneeded",
		},
		{
			name:     "valid escalation input passes validation (denied later without approver)",
			tool:     confined,
			input:    deniedCmd,
			wantType: "jail_violation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.tool.Execute(tc.input)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantType) {
				t.Errorf("error = %q, want type %q", err.Error(), tc.wantType)
			}
		})
	}
	// Explicit check: the plain in-jail command still succeeds.
	if _, err := confined.Execute(inJailCmd); err != nil {
		t.Errorf("in-jail command without escalation should succeed, got: %v", err)
	}
}

// TestBashEscalation_DenialMarker verifies a jail denial without escalation
// carries the dsh-style denial + escalation-available markers.
func TestBashEscalation_DenialMarker(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "cat /etc/passwd"}`)
	if err == nil {
		t.Fatal("expected jail violation")
	}
	msg := err.Error()
	if !strings.Contains(msg, "jail_violation") {
		t.Errorf("expected jail_violation, got: %q", msg)
	}
	if !strings.Contains(msg, "[sandbox: file access denied under workspace-write mode]") {
		t.Errorf("expected denial marker, got: %q", msg)
	}
	if !strings.Contains(msg, "escalation available") {
		t.Errorf("expected escalation-available guidance, got: %q", msg)
	}
	if !strings.Contains(msg, "sandbox_permissions") {
		t.Errorf("expected escalation fields in hint, got: %q", msg)
	}
}

// TestBashEscalation_NonWidening_FailsWithoutPrompting is the acceptance test
// for "non-widening requests fail without prompting": an escalation to the
// current mode must be rejected before the approver is consulted.
func TestBashEscalation_NonWidening_FailsWithoutPrompting(t *testing.T) {
	dir := t.TempDir()
	var prompted atomic.Int32
	tool := &BashTool{
		ProjectDir: dir,
		Jail:       true,
		EscalationApprover: func(ctx context.Context, req sandbox.EscalationRequest) (bool, error) {
			prompted.Add(1)
			return true, nil
		},
	}
	// workspace-write equals the jail's effective mode → not strictly wider.
	input := fmt.Sprintf(`{"command": "cat /etc/passwd", "sandbox_permissions": %q, "justification": "read system info"}`, sandbox.ModeWorkspaceWrite)
	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("expected sandbox_not_widening error")
	}
	if !strings.Contains(err.Error(), "sandbox_not_widening") {
		t.Errorf("expected sandbox_not_widening, got: %q", err.Error())
	}
	if prompted.Load() != 0 {
		t.Errorf("approver must not be consulted for a non-widening request, called %d times", prompted.Load())
	}
}

// TestBashEscalation_Approved_RunsWider is the acceptance test for "a denied
// write can be retried wider only post-approval": with user approval the exact
// denied command runs unconfined; without approval it stays denied and does
// not run.
func TestBashEscalation_Approved_RunsWider(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(os.TempDir(), fmt.Sprintf("goa-esc-approve-%d-%d.txt", os.Getpid(), time.Now().UnixNano()))
	defer os.Remove(out)

	var reqs []sandbox.EscalationRequest
	tool := &BashTool{
		ProjectDir: dir,
		Jail:       true,
		EscalationApprover: func(ctx context.Context, req sandbox.EscalationRequest) (bool, error) {
			reqs = append(reqs, req)
			return true, nil
		},
	}
	cmd := fmt.Sprintf("echo approved > %s", out)
	input := fmt.Sprintf(`{"command": %q, "sandbox_permissions": %q, "justification": "write scratch outside project"}`, cmd, sandbox.ModeDangerFullAccess)
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("approved escalation should run, got error: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("approver called %d times, want 1", len(reqs))
	}
	r := reqs[0]
	if r.RequestedMode != sandbox.ModeDangerFullAccess {
		t.Errorf("RequestedMode = %q, want %q", r.RequestedMode, sandbox.ModeDangerFullAccess)
	}
	if r.CurrentMode != sandbox.ModeWorkspaceWrite {
		t.Errorf("CurrentMode = %q, want %q", r.CurrentMode, sandbox.ModeWorkspaceWrite)
	}
	if r.Justification != "write scratch outside project" {
		t.Errorf("Justification = %q", r.Justification)
	}
	if !strings.Contains(r.Command, "echo approved") {
		t.Errorf("approver did not receive the exact command: %q", r.Command)
	}
	// The command redirected stdout to the outside file; verify the write
	// actually happened (the escalation really ran the command unconfined).
	data, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("approved escalated command did not write the outside file: %v", readErr)
	}
	if !strings.Contains(string(data), "approved") {
		t.Errorf("outside file content = %q, want approved marker", string(data))
	}
}

// TestBashEscalation_LongDocNotice verifies the escalation notice appears in
// LongDoc only when confinement is active, and always carries the closed
// vocabulary + never-speculative guidance.
func TestBashEscalation_LongDocNotice(t *testing.T) {
	unconfined := &BashTool{ProjectDir: t.TempDir(), Jail: false}
	if strings.Contains(unconfined.LongDoc(), "Sandbox escalation") {
		t.Error("LongDoc must not advertise escalation when confinement is inactive")
	}

	confined := &BashTool{ProjectDir: t.TempDir(), Jail: true}
	long := confined.LongDoc()
	for _, want := range []string{
		"Sandbox escalation",
		"workspace-write",
		"danger-full-access",
		"justification",
		"never speculative",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("LongDoc should mention %q when confined, got: %q", want, long)
		}
	}
}

// TestBashEscalation_Rejected_DoesNotRun covers the negative path: the user
// rejects the escalation (or no approver is wired) → the denied command never
// runs and the denial is final.
func TestBashEscalation_Rejected_DoesNotRun(t *testing.T) {
	cases := []struct {
		name     string
		approver sandbox.EscalationApprover
		wantType string
	}{
		{
			name: "approver rejects",
			approver: func(ctx context.Context, req sandbox.EscalationRequest) (bool, error) {
				return false, nil
			},
			wantType: "jail_violation",
		},
		{
			name:     "no approver wired (fail closed)",
			approver: nil,
			wantType: "jail_violation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(os.TempDir(), fmt.Sprintf("goa-esc-reject-%d-%d.txt", os.Getpid(), time.Now().UnixNano()))
			defer os.Remove(out)
			tool := &BashTool{ProjectDir: dir, Jail: true, EscalationApprover: tc.approver}
			cmd := fmt.Sprintf("echo nope > %s", out)
			input := fmt.Sprintf(`{"command": %q, "sandbox_permissions": %q, "justification": "write outside"}`, cmd, sandbox.ModeDangerFullAccess)
			_, err := tool.Execute(input)
			if err == nil {
				t.Fatal("expected denial")
			}
			if !strings.Contains(err.Error(), tc.wantType) {
				t.Errorf("error = %q, want %q", err.Error(), tc.wantType)
			}
			if _, statErr := os.Stat(out); statErr == nil {
				t.Error("rejected escalation must NOT run the command (file was created)")
			}
		})
	}
}
