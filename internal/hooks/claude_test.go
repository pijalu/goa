// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Claude Code config parsing
// ---------------------------------------------------------------------------

// TestLoadConfig_ClaudeFixtureVetoesBash is the P17 acceptance fixture: a real
// Claude Code hooks.json (testdata/fixture-project) vetoes a bash call with a
// structured deny decision and exit 2, and its additionalContext is carried by
// the veto error so it reaches the model through the tool-result path.
func TestLoadConfig_ClaudeFixtureVetoesBash(t *testing.T) {
	project, err := filepath.Abs(filepath.Join("testdata", "fixture-project"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir() // no goa-native or CC hooks at the user scope

	cfg, err := LoadConfig(home, project)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Hooks) != 1 {
		t.Fatalf("expected 1 CC hook from the fixture, got %d", len(cfg.Hooks))
	}
	h := cfg.Hooks[0]
	if h.Dialect != DialectClaudeCode {
		t.Errorf("expected claude-code dialect, got %q", h.Dialect)
	}
	if h.Event != EventBeforeTool {
		t.Errorf("expected PreToolUse to map to beforeTool, got %v", h.Event)
	}
	if h.Matcher != "Bash" {
		t.Errorf("expected matcher Bash, got %q", h.Matcher)
	}
	if !strings.Contains(h.Command, project) {
		t.Errorf("expected ${CLAUDE_PROJECT_DIR} substitution in %q", h.Command)
	}

	engine := NewEngine(cfg, nil)
	err = engine.FireBeforeTool(context.Background(), ToolPayload{
		ToolName: "bash", ToolInput: `{"command":"ls"}`, CallID: "call_1",
		SessionID: "sess_1", CWD: project,
	})
	if err == nil {
		t.Fatal("expected the fixture CC hook to veto the bash call")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fixture denies bash") {
		t.Errorf("veto error should carry the deny reason, got: %s", msg)
	}
	if !strings.Contains(msg, "fixture additional context reaches the model") {
		t.Errorf("veto error should carry additionalContext for the model, got: %s", msg)
	}

	entries := engine.Store().Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Dialect != DialectClaudeCode {
		t.Errorf("audit entry should record claude-code dialect, got %q", entries[0].Dialect)
	}
	if entries[0].ExitCode != 2 {
		t.Errorf("expected exit code 2 recorded, got %d", entries[0].ExitCode)
	}
}

// TestLoadConfig_ClaudeCascadeComposes verifies goa-native and Claude Code
// hooks compose across user and project scopes: the merged config carries both
// dialects in scope order (user goa, user CC, project goa, project CC).
func TestLoadConfig_ClaudeCascadeComposes(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	project := filepath.Join(dir, "project")
	mkdirs(t, filepath.Join(home, ".goa"), filepath.Join(home, ".claude"))
	mkdirs(t, filepath.Join(project, ".goa"), filepath.Join(project, ".claude"))

	writeFile(t, filepath.Join(home, ".goa", "hooks.yaml"), "hooks:\n- event: sessionStart\n  command: a\n")
	writeFile(t, filepath.Join(home, ".claude", "hooks.json"), `{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"cc-user"}]}]}`)
	writeFile(t, filepath.Join(project, ".goa", "hooks.yaml"), "hooks:\n- event: beforeTool\n  command: b\n")
	writeFile(t, filepath.Join(project, ".claude", "settings.json"), `{"hooks":{"PostToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"cc-project"}]}]}}`)

	cfg, err := LoadConfig(home, project)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Hooks) != 4 {
		t.Fatalf("expected 4 composed hooks, got %d", len(cfg.Hooks))
	}
	want := []struct {
		event   Event
		command string
		dialect Dialect
	}{
		{EventSessionStart, "a", ""}, // goa-native hooks load with the zero dialect
		{EventBeforeTool, "cc-user", DialectClaudeCode},
		{EventBeforeTool, "b", ""},
		{EventAfterTool, "cc-project", DialectClaudeCode},
	}
	for i, w := range want {
		got := cfg.Hooks[i]
		if got.Event != w.event || got.Command != w.command || got.Dialect != w.dialect {
			t.Errorf("hook %d: got event=%v command=%q dialect=%q, want event=%v command=%q dialect=%q",
				i, got.Event, got.Command, got.Dialect, w.event, w.command, w.dialect)
		}
	}
}

// TestLoadConfig_ClaudeWarnings verifies non-command hook types are skipped
// with a warning and do not register hooks.
func TestLoadConfig_ClaudeWarnings(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	mkdirs(t, filepath.Join(project, ".claude"))
	writeFile(t, filepath.Join(project, ".claude", "hooks.json"), `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "ok"},
          {"type": "http", "url": "http://example.com"}
        ]
      }
    ],
    "UserPromptSubmit": [
      {"matcher": "", "hooks": [{"type": "command", "command": "unsupported-event"}]}
    ]
  }
}`)

	cfg, err := LoadConfig(t.TempDir(), project)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Hooks) != 1 {
		t.Fatalf("expected only the command hook to register, got %d", len(cfg.Hooks))
	}
	if cfg.Hooks[0].Command != "ok" {
		t.Errorf("expected the command hook, got %q", cfg.Hooks[0].Command)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("expected a warning for the skipped http hook")
	}
	if !strings.Contains(cfg.Warnings[0], "http") {
		t.Errorf("expected the warning to name the http type, got %q", cfg.Warnings[0])
	}
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "UserPromptSubmit") {
			t.Errorf("unsupported events must be ignored before parsing, but warning mentions them: %s", w)
		}
	}
}

// TestParseClaudeHooks_ExecFormAndTimeout verifies exec-form args, per-hook
// timeouts, and matcher handling for tool vs session events.
func TestParseClaudeHooks_ExecFormAndTimeout(t *testing.T) {
	data := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash|Write", "hooks": [{"type": "command", "command": "node", "args": ["${CLAUDE_PROJECT_DIR}/x.js"], "timeout": 30}]}
    ],
    "SessionStart": [
      {"matcher": "startup", "hooks": [{"type": "command", "command": "init"}]}
    ]
  }
}`)
	project := "/proj"
	hooks, warnings, err := parseClaudeHooks(data, project)
	if err != nil {
		t.Fatalf("parseClaudeHooks: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	pre := hooks[0]
	if pre.Event != EventBeforeTool || pre.Dialect != DialectClaudeCode {
		t.Errorf("unexpected PreToolUse mapping: %+v", pre)
	}
	if pre.Matcher != "Bash|Write" {
		t.Errorf("tool matcher should be preserved, got %q", pre.Matcher)
	}
	if len(pre.Args) != 1 || pre.Args[0] != "/proj/x.js" {
		t.Errorf("exec-form args should be substituted, got %v", pre.Args)
	}
	if pre.TimeoutSeconds != 30 {
		t.Errorf("expected per-hook timeout 30, got %d", pre.TimeoutSeconds)
	}
	sess := hooks[1]
	if sess.Event != EventSessionStart {
		t.Errorf("expected SessionStart mapping, got %v", sess.Event)
	}
	if sess.Matcher != "" {
		t.Errorf("session matcher should be discarded, got %q", sess.Matcher)
	}
	if sess.TimeoutSeconds != 600 {
		t.Errorf("expected CC default timeout 600, got %d", sess.TimeoutSeconds)
	}
}

// ---------------------------------------------------------------------------
// Claude Code matcher
// ---------------------------------------------------------------------------

func TestMatchesClaudeMatcher(t *testing.T) {
	tests := []struct {
		name    string
		matcher string
		query   string
		want    bool
	}{
		{"match all star", "*", "bash", true},
		{"match all empty", "", "bash", true},
		{"literal exact", "Bash", "bash", true},
		{"literal case", "bash", "Bash", true},
		{"literal no match", "Bash", "read", false},
		{"pipe alternatives", "Bash|Write", "write", true},
		{"pipe alternatives no match", "Bash|Write", "edit", false},
		{"comma alternatives with spaces", "Edit, Write", "write", true},
		{"hyphenated literal", "code-reviewer", "code-reviewer", true},
		{"regex unanchored", "^Bash$", "bash", false}, // regex is case-sensitive
		{"regex unanchored match", "bash.*", "bash_tool", true},
		{"invalid regex never matches", "[", "bash", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesClaudeMatcher(tt.matcher, tt.query); got != tt.want {
				t.Errorf("matchesClaudeMatcher(%q, %q) = %v, want %v", tt.matcher, tt.query, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Claude Code output codec
// ---------------------------------------------------------------------------

func TestDecodeClaudeOutcome(t *testing.T) {
	tests := []struct {
		name     string
		exit     int
		stdout   string
		stderr   string
		wantVeto bool
		wantIn   string
	}{
		{"exit0 no output", 0, "", "", false, ""},
		{"exit0 plain text", 0, "hello", "", false, ""},
		{"exit0 malformed json", 0, "{oops", "", false, ""},
		{"exit2 stderr blocks", 2, "", "blocked by stderr", true, "blocked by stderr"},
		{"exit2 json allow cannot override", 2, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`, "", true, ""},
		{"exit0 json deny", 0, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no db writes"}}`, "", true, "no db writes"},
		{"exit1 json deny decides", 1, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"json says no"}}`, "stderr", true, "json says no"},
		{"exit0 json ask blocks", 0, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"confirm?"}}`, "", true, "confirm?"},
		{"exit0 json defer blocks", 0, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"defer"}}`, "", true, ""},
		{"exit0 json allow", 0, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`, "", false, ""},
		{"exit1 no json non-blocking", 1, "", "boom", false, ""},
		{"exit0 top-level block", 0, `{"decision":"block","reason":"suite must pass"}`, "", true, "suite must pass"},
		{"exit0 top-level approve", 0, `{"decision":"approve"}`, "", false, ""},
		{"exit0 additionalContext", 0, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"ctx text"}}`, "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := decodeClaudeOutcome(tt.exit, tt.stdout, tt.stderr)
			if o.veto != tt.wantVeto {
				t.Errorf("veto = %v, want %v", o.veto, tt.wantVeto)
			}
			if tt.wantIn != "" && !strings.Contains(o.reason, tt.wantIn) {
				t.Errorf("reason %q should contain %q", o.reason, tt.wantIn)
			}
		})
	}

	ctx := decodeClaudeOutcome(0, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"ctx text"}}`, "")
	if ctx.context != "ctx text" {
		t.Errorf("additionalContext = %q, want %q", ctx.context, "ctx text")
	}
	sys := decodeClaudeOutcome(0, `{"systemMessage":"warning"}`, "")
	if sys.system != "warning" {
		t.Errorf("systemMessage = %q, want %q", sys.system, "warning")
	}
}

func TestCapClaudeContext(t *testing.T) {
	big := strings.Repeat("x", claudeContextCap+500)
	got := capClaudeContext(big)
	if len(got) > claudeContextCap+200 {
		t.Errorf("capped context too large: %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("expected truncation notice")
	}
	small := "hello"
	if capClaudeContext(small) != small {
		t.Error("small context should pass through untouched")
	}
}

// ---------------------------------------------------------------------------
// Engine: CC beforeTool veto / merge / payload
// ---------------------------------------------------------------------------

// ccDeny is a CC-dialect hook that prints a structured deny and exits 2.
func ccDeny(reason, context string) Hook {
	return Hook{
		Event:   EventBeforeTool,
		Command: `printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + reason + `","additionalContext":"` + context + `"}}'; exit 2`,
		Dialect: DialectClaudeCode,
	}
}

func TestEngine_CCBeforeTool_VetoWithAdditionalContext(t *testing.T) {
	engine := NewEngine(&Config{Hooks: []Hook{ccDeny("no bash here", "ctx-for-model")}}, nil)
	err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "bash", ToolInput: `{}`})
	if err == nil {
		t.Fatal("expected veto")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no bash here") {
		t.Errorf("veto should carry the reason: %s", msg)
	}
	if !strings.Contains(msg, "ctx-for-model") {
		t.Errorf("veto should carry additionalContext for the model: %s", msg)
	}
}

func TestEngine_CCBeforeTool_JSONAllowDoesNotVeto(t *testing.T) {
	h := Hook{
		Event:   EventBeforeTool,
		Command: `printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'`,
		Dialect: DialectClaudeCode,
	}
	engine := NewEngine(&Config{Hooks: []Hook{h}}, nil)
	if err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "bash"}); err != nil {
		t.Fatalf("allow should not veto: %v", err)
	}
}

func TestEngine_CCBeforeTool_NonBlockingExit(t *testing.T) {
	h := Hook{
		Event:   EventBeforeTool,
		Command: "echo boom >&2; exit 1",
		Dialect: DialectClaudeCode,
	}
	engine := NewEngine(&Config{Hooks: []Hook{h}}, nil)
	if err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "bash"}); err != nil {
		t.Fatalf("non-zero exit without JSON is non-blocking: %v", err)
	}
	entries := engine.Store().Entries()
	if len(entries) != 1 || entries[0].ExitCode != 1 {
		t.Fatalf("expected audit entry with exit 1, got %+v", entries)
	}
}

func TestEngine_CCBeforeTool_MatcherFilters(t *testing.T) {
	h := Hook{
		Event:   EventBeforeTool,
		Command: "exit 2",
		Dialect: DialectClaudeCode,
		Matcher: "Write",
	}
	engine := NewEngine(&Config{Hooks: []Hook{h}}, nil)
	// The Write matcher must not fire for a bash call.
	if err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "bash"}); err != nil {
		t.Fatalf("matcher Write should not veto a bash call: %v", err)
	}
	if len(engine.Store().Entries()) != 0 {
		t.Fatal("matcher-missed hook must not run")
	}
	// It must fire for a write call.
	if err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "write"}); err == nil {
		t.Fatal("matcher Write should veto a write call")
	}
}

// TestEngine_ComposesGoaAndCC is the acceptance "goa-native and CC hooks
// compose": both dialects run serially and the most-restrictive outcome wins.
func TestEngine_ComposesGoaAndCC(t *testing.T) {
	ccAllow := Hook{
		Event:   EventBeforeTool,
		Command: `printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'`,
		Dialect: DialectClaudeCode,
	}
	ccDenyHook := ccDeny("cc says no", "cc-ctx")
	goaPass := Hook{Event: EventBeforeTool, Command: "sh", Args: []string{"-c", "cat"}}
	goaVeto := Hook{Event: EventBeforeTool, Command: "sh", Args: []string{"-c", "exit 1"}}

	tests := []struct {
		name  string
		hooks []Hook
		want  bool // true = vetoed
	}{
		{"goa pass + cc allow", []Hook{goaPass, ccAllow}, false},
		{"goa veto + cc allow", []Hook{goaVeto, ccAllow}, true},
		{"goa pass + cc deny", []Hook{goaPass, ccDenyHook}, true},
		{"goa veto + cc deny", []Hook{goaVeto, ccDenyHook}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewEngine(&Config{Hooks: tt.hooks}, nil)
			err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "bash"})
			if (err != nil) != tt.want {
				t.Fatalf("vetoed = %v, want %v (err=%v)", err != nil, tt.want, err)
			}
			if err != nil && tt.name == "goa pass + cc deny" {
				if !strings.Contains(err.Error(), "cc-ctx") {
					t.Errorf("CC additionalContext should reach the model through the merged veto: %s", err)
				}
			}
		})
	}
}

// TestEngine_CCBeforeTool_ReceivesCCPayload verifies the CC stdin payload is
// CC-shaped (hook_event_name, tool_name, tool_input object, tool_use_id, cwd).
func TestEngine_CCBeforeTool_ReceivesCCPayload(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "in.json")
	h := Hook{
		Event:          EventBeforeTool,
		Command:        "cat > " + outFile,
		Dialect:        DialectClaudeCode,
		TimeoutSeconds: 5,
	}
	engine := NewEngine(&Config{Hooks: []Hook{h}}, nil)
	err := engine.FireBeforeTool(context.Background(), ToolPayload{
		ToolName: "bash", ToolInput: `{"command":"ls"}`, CallID: "call_9",
		SessionID: "sess_9", CWD: "/proj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("captured payload is not JSON: %v (%s)", err, data)
	}
	if got["hook_event_name"] != "PreToolUse" {
		t.Errorf("hook_event_name = %v, want PreToolUse", got["hook_event_name"])
	}
	if got["tool_name"] != "bash" {
		t.Errorf("tool_name = %v, want bash", got["tool_name"])
	}
	if got["tool_use_id"] != "call_9" {
		t.Errorf("tool_use_id = %v, want call_9", got["tool_use_id"])
	}
	if got["session_id"] != "sess_9" {
		t.Errorf("session_id = %v, want sess_9", got["session_id"])
	}
	if got["cwd"] != "/proj" {
		t.Errorf("cwd = %v, want /proj", got["cwd"])
	}
	ti, ok := got["tool_input"].(map[string]any)
	if !ok || ti["command"] != "ls" {
		t.Errorf("tool_input should be the parsed object, got %v", got["tool_input"])
	}
}

// TestEngine_CCSessionStart_ReceivesCCPayload verifies SessionStart hooks
// receive the CC session payload (session_id, cwd, hook_event_name).
func TestEngine_CCSessionStart_ReceivesCCPayload(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "in.json")
	h := Hook{
		Event:          EventSessionStart,
		Command:        "cat > " + outFile,
		Dialect:        DialectClaudeCode,
		TimeoutSeconds: 5,
	}
	engine := NewEngine(&Config{Hooks: []Hook{h}}, nil)
	if err := engine.FireSessionStart(context.Background(), SessionPayload{SessionID: "sess_1", ProjectDir: "/proj"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("captured payload is not JSON: %v (%s)", err, data)
	}
	if got["hook_event_name"] != "SessionStart" {
		t.Errorf("hook_event_name = %v, want SessionStart", got["hook_event_name"])
	}
	if got["session_id"] != "sess_1" {
		t.Errorf("session_id = %v, want sess_1", got["session_id"])
	}
	if got["cwd"] != "/proj" {
		t.Errorf("cwd = %v, want /proj", got["cwd"])
	}
}

// helpers

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
