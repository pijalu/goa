// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"context"
	"fmt"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/tools"
	"github.com/stretchr/testify/require"
)

func TestRegisterTools_ClarifyDefaultOn(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil, false)
	if _, ok := reg.Get("ask_user_question"); !ok {
		t.Fatal("ask_user_question should be registered by default")
	}
}

func TestRegisterTools_ClarifyDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("clarify_disabled", true)
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg, nil, false)
	if _, ok := reg.Get("ask_user_question"); ok {
		t.Fatal("ask_user_question should NOT be registered when clarify_disabled is true")
	}
}

// TestRegisterTools_HeadlessSkipsAskUser pins Bug C: headless mode
// has no user at the input line, so the ask_user_question tool must not be
// registered — regardless of the clarify_disabled flag.
func TestRegisterTools_HeadlessSkipsAskUser(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil, true)
	if _, ok := reg.Get("ask_user_question"); ok {
		t.Fatal("ask_user_question must NOT be registered in headless mode")
	}

	// Interactive keeps the default-on behavior.
	reg = tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil, false)
	if _, ok := reg.Get("ask_user_question"); !ok {
		t.Fatal("ask_user_question must be registered in interactive mode by default")
	}
}

func TestAttachClarifyTool(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil, false)
	called := false
	attachClarifyTool(reg, func(title, summary, question string, options []string, step, total int) (string, bool) {
		called = true
		return "x", true
	})
	tt, ok := reg.Get("ask_user_question")
	if !ok {
		t.Fatal("tool missing")
	}
	// Execute a single question to confirm the callback was wired.
	out, err := tt.Execute(`{"questions":[{"question":"q"}]}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Error("clarify callback was not invoked")
	}
	if out == "" {
		t.Error("expected non-empty result")
	}
}

func TestAttachClarifyTool_NilSafe(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil, false)
	attachClarifyTool(reg, nil) // must not panic
}

// TestAttachEscalationApprover verifies the sandbox escalation approver is
// injected into the registered bash tool.
func TestAttachEscalationApprover(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil, false)
	called := false
	attachEscalationApprover(reg, func(ctx context.Context, req sandbox.EscalationRequest) (bool, error) {
		called = true
		return true, nil
	})
	tt, ok := reg.Get("bash")
	if !ok {
		t.Fatal("bash tool missing")
	}
	bt, ok := tt.(*tools.BashTool)
	if !ok {
		t.Fatalf("bash tool is %T", tt)
	}
	if bt.EscalationApprover == nil {
		t.Fatal("escalation approver was not wired")
	}
	// The approver must actually be invoked through the tool's escalation path.
	project := t.TempDir()
	bt.ProjectDir = project
	bt.Jail = true
	// Widen to danger-full-access and reject; a nil approver would have the
	// same denial, so assert the callback ran via the flag.
	_, _ = bt.Execute(fmt.Sprintf(`{"command": "cat /etc/passwd", "sandbox_permissions": %q, "justification": "read system info"}`, sandbox.ModeDangerFullAccess))
	if !called {
		t.Error("escalation approver was not invoked")
	}
}

func TestAttachEscalationApprover_NilSafe(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil, false)
	attachEscalationApprover(reg, nil) // must not panic
}

func TestRegisterTools_SmartSearchRespectsEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.SmartSearch.Enabled = false

	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg, nil, false)
	if _, ok := reg.Get("smartsearch"); ok {
		t.Fatal("smartsearch should NOT be registered when disabled")
	}

	cfg.Tools.SmartSearch.Enabled = true
	reg = tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg, nil, false)
	if _, ok := reg.Get("smartsearch"); !ok {
		t.Fatal("smartsearch should be registered when enabled")
	}
}

// TestRegisterTools_RunCodeRespectsEnabled pins the run_code code-mode tool
// (gap TL7) registration gating: opt-out by default (mirroring python), so the
// model can submit a multi-tool program unless the user disables it.
func TestRegisterTools_RunCodeRespectsEnabled(t *testing.T) {
	projectDir := t.TempDir()

	// Enabled (embedded default): the tool is registered and wired with the
	// app registry as its sub-call surface plus a dispatch log dir under the
	// project.
	cfg, err := config.NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, projectDir, cfg, nil, false)
	rc, ok := reg.Get("run_code")
	if !ok {
		t.Fatal("run_code should be registered by default (opt-out like python)")
	}
	rcTool, ok := rc.(*tools.RunCodeTool)
	if !ok {
		t.Fatalf("run_code tool has type %T, want *tools.RunCodeTool", rc)
	}
	if rcTool.Registry == nil {
		t.Error("run_code tool must be wired with the app registry for its sub-call surface")
	}
	if rcTool.DispatchDir != projectDir+"/.goa/dispatch" {
		t.Errorf("DispatchDir = %q, want %q", rcTool.DispatchDir, projectDir+"/.goa/dispatch")
	}
	if !rcTool.Jail {
		t.Error("run_code worker must be jailed by default (gap TL7 jailed worker)")
	}

	// Disabled: absent from the registry.
	cfg.Tools.Enabled.SetEnabled("run_code", false)
	reg = tools.NewToolRegistry()
	registerTools(reg, nil, nil, projectDir, cfg, nil, false)
	if _, ok := reg.Get("run_code"); ok {
		t.Fatal("run_code should NOT be registered when disabled")
	}
}

// TestRegisterTools_RunCodeDispatchDirEmptyWithoutProject pins that a
// project-less registration disables dispatch-log persistence rather than
// writing into a relative directory.
func TestRegisterTools_RunCodeDispatchDirEmptyWithoutProject(t *testing.T) {
	cfg, err := config.NewCascadeLoader("", "", nil).Load()
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, "", cfg, nil, false)
	rc, ok := reg.Get("run_code")
	if !ok {
		t.Fatal("run_code should be registered")
	}
	if rcTool := rc.(*tools.RunCodeTool); rcTool.DispatchDir != "" {
		t.Errorf("DispatchDir = %q, want empty when there is no project dir", rcTool.DispatchDir)
	}
}

func TestRuntimeOptions_EmptyPromptImpliesHeadless(t *testing.T) {
	opts := RuntimeOptions{PromptArg: "", PromptGiven: true}
	if !opts.Headless() {
		t.Error("expected headless when --prompt is explicitly empty")
	}
}

func TestRuntimeOptions_PromptArgImpliesHeadless(t *testing.T) {
	opts := RuntimeOptions{PromptArg: "hello"}
	if !opts.Headless() {
		t.Error("expected headless when --prompt has a value")
	}
}

func TestRuntimeOptions_PromptFileImpliesHeadless(t *testing.T) {
	opts := RuntimeOptions{PromptFile: "/tmp/prompt.txt"}
	if !opts.Headless() {
		t.Error("expected headless when --prompt-file is set")
	}
}

func TestRuntimeOptions_DefaultIsTUI(t *testing.T) {
	opts := RuntimeOptions{}
	if opts.Headless() {
		t.Error("expected TUI mode by default")
	}
}

// TestParseCLIFlags_RepeatedCallsNoPanic is the regression test for the crash
// after the setup wizard finishes: Main() relaunches via runApp(), which calls
// ParseCLIFlags() a SECOND time. Before the fix, flags were registered on the
// process-global flag.CommandLine, so the second registration panicked with
// "goa flag redefined: model" and the app died right after the wizard saved
// the config.
func TestParseCLIFlags_RepeatedCallsNoPanic(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"goa"} // flag-less launch, mirroring the wizard relaunch

	firstFlags, firstOpts := ParseCLIFlags()
	secondFlags, secondOpts := ParseCLIFlags() // used to panic: flag redefined

	require.Equal(t, firstFlags, secondFlags)
	require.Equal(t, firstOpts, secondOpts)
}
