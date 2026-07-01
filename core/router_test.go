// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"
)

// setupTestRouter creates a router with test commands.
func setupTestRouter(t *testing.T) *CommandRouter {
	t.Helper()
	reg := NewCommandRegistry()
	reg.Register(&testCommand{name: "help", aliases: []string{"h"}})
	reg.Register(&testCommand{name: "mode"})
	reg.Register(&testCommand{name: "quit", aliases: []string{"q"}})
	return NewCommandRouter(reg, NewDocEngine(reg))
}

// TestRouterParseCommand verifies basic command parsing.
func TestRouterParseCommand(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/help")
	if result == nil {
		t.Fatal("Parse('/help') returned nil")
	}
	if result.Command == nil {
		t.Fatal("Command should not be nil")
	}
	if result.Command.Name() != "help" {
		t.Errorf("Command = %q, want %q", result.Command.Name(), "help")
	}
	if result.DocLevel != DocSuffixNone {
		t.Errorf("DocLevel = %d, want DocSuffixNone", result.DocLevel)
	}
	if result.IsHelp {
		t.Error("IsHelp should be false without ?")
	}
}

// TestRouterParseShortDoc verifies /command? routing.
func TestRouterParseShortDoc(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/help?")
	if result == nil {
		t.Fatal("Parse('/help?') returned nil")
	}
	if result.Command == nil {
		t.Fatal("Command should not be nil")
	}
	if result.DocLevel != DocSuffixShort {
		t.Errorf("DocLevel = %d, want DocSuffixShort", result.DocLevel)
	}
	if !result.IsHelp {
		t.Error("IsHelp should be true with ?")
	}
}

// TestRouterParseLongDoc verifies /command?? routing.
func TestRouterParseLongDoc(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/mode??")
	if result == nil {
		t.Fatal("Parse('/mode??') returned nil")
	}
	if result.DocLevel != DocSuffixLong {
		t.Errorf("DocLevel = %d, want DocSuffixLong", result.DocLevel)
	}
}

// TestRouterParseUnknown verifies unknown command handling.
func TestRouterParseUnknown(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/garbage")
	if result == nil {
		t.Fatal("Parse('/garbage') should return a result")
	}
	if result.Command != nil {
		t.Error("Command should be nil for unknown command")
	}
}

// TestRouterParseNonCommand verifies non-command input.
func TestRouterParseNonCommand(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("hello world")
	if result != nil {
		t.Error("Non-command input should return nil")
	}
}

// TestRouterParseArgs verifies colon-separated arguments.
func TestRouterParseArgs(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/mode:confirm")
	if result == nil {
		t.Fatal("Parse returned nil")
	}
	if len(result.Args) != 1 || result.Args[0] != "confirm" {
		t.Errorf("Args = %v, want [confirm]", result.Args)
	}
}

// TestRouterParseMultiArgs verifies multiple colon-separated arguments.
func TestRouterParseMultiArgs(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/skill:run:refactor")
	if result == nil {
		t.Fatal("Parse returned nil")
	}
	if len(result.Args) != 2 || result.Args[0] != "run" || result.Args[1] != "refactor" {
		t.Errorf("Args = %v, want [run refactor]", result.Args)
	}
}

// TestRouterParseSpaceOnly verify space-separated syntax is NOT supported.
func TestRouterParseSpaceOnly(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/mode confirm")
	if result == nil {
		t.Fatal("Parse returned nil")
	}
	if result.Command != nil {
		t.Error("Space-separated syntax should not resolve to a command")
	}
}

// TestRouterParseWithAlias verifies aliases resolve correctly.
func TestRouterParseWithAlias(t *testing.T) {
	router := setupTestRouter(t)

	result := router.Parse("/q")
	if result == nil {
		t.Fatal("Parse('/q') returned nil")
	}
	if result.Command.Name() != "quit" {
		t.Errorf("Command = %q, want %q", result.Command.Name(), "quit")
	}
}

// TestRouterCanonicalColonForms locks the colon-only syntax the help docs now
// describe. Each entry is a documented canonical form and the args the
// corresponding command's Run must receive. If the router ever regressed to
// accepting space form, or a doc drifted back to it, this table — together
// with commands/help/help_colon_syntax_test.go — catches it.
func TestRouterCanonicalColonForms(t *testing.T) {
	reg := NewCommandRegistry()
	for _, name := range []string{
		"pair", "reviewer", "orchestrate", "swarm", "thinking", "exchange",
		"stats", "memory", "session", "skill", "pipeline", "ui", "goal",
		"mode", "autonomy", "theme", "permission", "trust", "telemetry",
		"docs", "export",
	} {
		if err := reg.Register(&testCommand{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	router := NewCommandRouter(reg, NewDocEngine(reg))

	cases := []struct {
		name  string
		input string
		cmd   string
		args  []string
	}{
		{"pair free text", "/pair:Implement auth", "pair", []string{"Implement auth"}},
		{"reviewer free text", "/reviewer:Add validation", "reviewer", []string{"Add validation"}},
		{"orchestrate tasks", "/orchestrate:Build auth, Add tests", "orchestrate", []string{"Build auth, Add tests"}},
		{"swarm on", "/swarm:on", "swarm", []string{"on"}},
		{"swarm task", "/swarm:fix all lints", "swarm", []string{"fix all lints"}},
		{"thinking level", "/thinking:high", "thinking", []string{"high"}},
		{"exchange turn", "/exchange:3", "exchange", []string{"3"}},
		{"stats turn", "/stats:3", "stats", []string{"3"}},
		{"memory show name", "/memory:show:context", "memory", []string{"show", "context"}},
		{"session save name", "/session:save:my-work", "session", []string{"save", "my-work"}},
		{"skill run args", "/skill:run:refactor:src/main.go", "skill", []string{"run", "refactor", "src/main.go"}},
		{"pipeline run input", "/pipeline:run:feat:Add auth", "pipeline", []string{"run", "feat", "Add auth"}},
		{"ui theme set", "/ui:theme:set:accent:#ff6600", "ui", []string{"theme", "set", "accent", "#ff6600"}},
		{"goal new text", "/goal:new:Build it", "goal", []string{"new", "Build it"}},
		{"mode switch", "/mode:coder", "mode", []string{"coder"}},
		{"export path desc", "/export:/tmp/x.zip:crash on start", "export", []string{"/tmp/x.zip", "crash on start"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertColonForm(t, router, c.input, c.cmd, c.args)
		})
	}
}

func assertColonForm(t *testing.T, router *CommandRouter, input, cmd string, wantArgs []string) {
	t.Helper()
	res := router.Parse(input)
	if res == nil {
		t.Fatalf("Parse(%q) returned nil", input)
	}
	if res.Command == nil {
		t.Fatalf("Parse(%q) did not resolve to a command", input)
	}
	if res.Command.Name() != cmd {
		t.Errorf("command = %q, want %q", res.Command.Name(), cmd)
	}
	if !equalArgs(res.Args, wantArgs) {
		t.Errorf("args = %v, want %v", res.Args, wantArgs)
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRouterParseWithUserAliases verifies user-defined aliases resolve.
func TestRouterParseWithUserAliases(t *testing.T) {
	t.Run("plain alias", testAliasToPlainCommand)
	t.Run("baked args", testAliasWithBakedArgs)
	t.Run("baked args plus extra", testAliasWithBakedArgsAndExtra)
}

func testAliasToPlainCommand(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&testCommand{name: "help", aliases: []string{"h"}})
	reg.Register(&testCommand{name: "mode"})
	reg.Register(&testCommand{name: "quit"})
	router := NewCommandRouter(reg, NewDocEngine(reg))
	router.SetAliases(map[string]string{"m": "mode", "q": "quit"})

	result := router.Parse("/m")
	if result == nil {
		t.Fatal("Parse('/m') returned nil")
	}
	if result.Command.Name() != "mode" {
		t.Errorf("Command = %q, want %q", result.Command.Name(), "mode")
	}
	if len(result.Args) != 0 {
		t.Errorf("Args = %v, want []", result.Args)
	}
}

func testAliasWithBakedArgs(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&testCommand{name: "mode"})
	router := NewCommandRouter(reg, NewDocEngine(reg))
	router.SetAliases(map[string]string{"m": "mode:coder"})

	result := router.Parse("/m")
	if result == nil {
		t.Fatal("Parse('/m') returned nil")
	}
	if result.Command.Name() != "mode" {
		t.Errorf("Command = %q, want %q", result.Command.Name(), "mode")
	}
	if len(result.Args) != 1 || result.Args[0] != "coder" {
		t.Errorf("Args = %v, want [coder]", result.Args)
	}
}

func testAliasWithBakedArgsAndExtra(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&testCommand{name: "mode"})
	router := NewCommandRouter(reg, NewDocEngine(reg))
	router.SetAliases(map[string]string{"m": "mode:coder"})

	result := router.Parse("/m:extra")
	if result == nil {
		t.Fatal("Parse('/m:extra') returned nil")
	}
	if result.Command.Name() != "mode" {
		t.Errorf("Command = %q, want %q", result.Command.Name(), "mode")
	}
	if len(result.Args) != 2 || result.Args[0] != "coder" || result.Args[1] != "extra" {
		t.Errorf("Args = %v, want [coder extra]", result.Args)
	}
}

// TestDocEngineShortDoc verifies short documentation lookup.
func TestDocEngineShortDoc(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&testCommand{name: "test"})
	de := NewDocEngine(reg)

	doc, err := de.ShortDoc("cmd:test")
	if err != nil {
		t.Fatalf("ShortDoc('cmd:test') error: %v", err)
	}
	if doc == "" {
		t.Error("ShortDoc should not be empty")
	}
}

// TestDocEngineLongDoc verifies long documentation lookup.
func TestDocEngineLongDoc(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&testCommand{name: "test"})
	de := NewDocEngine(reg)

	doc, err := de.LongDoc("cmd:test")
	if err != nil {
		t.Fatalf("LongDoc('cmd:test') error: %v", err)
	}
	if doc == "" {
		t.Error("LongDoc should not be empty")
	}
}

// TestDocEngineUnknown verifies error for unknown commands.
func TestDocEngineUnknown(t *testing.T) {
	reg := NewCommandRegistry()
	de := NewDocEngine(reg)

	_, err := de.ShortDoc("cmd:nonexistent")
	if err == nil {
		t.Error("Expected error for unknown command")
	}
}

// TestDocEngineNamespace verifies namespace prefixes.
func TestDocEngineNamespace(t *testing.T) {
	reg := NewCommandRegistry()
	de := NewDocEngine(reg)

	// Tool namespace should not error (returns placeholder)
	doc, err := de.ShortDoc("tool:read")
	if err != nil {
		t.Fatalf("ShortDoc('tool:read') error: %v", err)
	}
	if doc == "" {
		t.Error("Tool doc should not be empty")
	}

	// Unknown namespace should error
	_, err = de.ShortDoc("bad:name")
	if err == nil {
		t.Error("Expected error for unknown namespace")
	}
}
