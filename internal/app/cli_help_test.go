// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"flag"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/pijalu/goa/docs"
)

// TestCliFlagGroups_CoverEveryFlagOnce is the completeness gate for the option
// reference: a flag that is registered but unlisted would silently vanish from
// `goa --help`, and a listed but unregistered one would print a dead option.
func TestCliFlagGroups_CoverEveryFlagOnce(t *testing.T) {
	registered := map[string]bool{}
	cliFlags().fs.VisitAll(func(f *flag.Flag) { registered[f.Name] = true })
	if len(registered) == 0 {
		t.Fatal("no flags registered — the flag set is not being initialized")
	}

	grouped := map[string]int{}
	for _, g := range flagGroups {
		if strings.TrimSpace(g.Title) == "" {
			t.Error("a flag group has no title")
		}
		for _, name := range g.Flags {
			grouped[name]++
		}
	}

	for name := range registered {
		switch grouped[name] {
		case 1:
		case 0:
			t.Errorf("flag --%s is registered but missing from flagGroups: `goa --help` would not document it", name)
		default:
			t.Errorf("flag --%s appears in %d groups, want exactly 1", name, grouped[name])
		}
	}
	for name := range grouped {
		if !registered[name] {
			t.Errorf("flagGroups lists --%s, which is not a registered flag", name)
		}
	}
}

// TestCLIManual_DocumentsEveryRegisteredFlag guards the generated section's
// wiring: the groups existing is not enough, the manual must actually print each
// option, with the flag's own description.
func TestCLIManual_DocumentsEveryRegisteredFlag(t *testing.T) {
	manual := cliManual()
	if marker := "(undocumented option:"; strings.Contains(manual, marker) {
		t.Errorf("the manual reports %s — the option lookup is broken:\n%s", marker,
			excerptAround(manual, marker))
	}
	cliFlags().fs.VisitAll(func(f *flag.Flag) {
		if !strings.Contains(manual, "--"+f.Name) {
			t.Errorf("manual does not document --%s", f.Name)
		}
		// The description must come from the live flag definition, so the
		// reference can never describe an option differently than it behaves.
		_, usage := flag.UnquoteUsage(f)
		if usage != "" && !strings.Contains(manual, usage) {
			t.Errorf("manual does not carry the --%s description %q", f.Name, usage)
		}
	})
}

// TestCLIManual_IndexesEveryEmbeddedDoc keeps `goa help <topic>` discoverable
// from the manual: a document that is not indexed is a feature the user cannot
// find.
func TestCLIManual_IndexesEveryEmbeddedDoc(t *testing.T) {
	manual := cliManual()
	list, err := docs.List()
	if err != nil {
		t.Fatalf("docs.List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("no embedded docs")
	}
	for _, d := range list {
		if !strings.Contains(manual, d.Name) {
			t.Errorf("manual does not index the %s document", d.Name)
		}
		if d.Description != "" && !strings.Contains(manual, d.Description) {
			t.Errorf("manual does not describe the %s document (%q)", d.Name, d.Description)
		}
	}
}

// TestCLIManual_CoversEveryFeature is the content gate: `goa --help` is the
// entry point for every mode the binary has, so each of them must be reachable
// from the text — including the Web UI, which is otherwise only discoverable
// from a document the user has to know to look for.
func TestCLIManual_CoversEveryFeature(t *testing.T) {
	manual := cliManual()
	for _, want := range []string{
		// Invocation surfaces
		"goa server", "goa mcp", "goa help",
		// Modes
		"--prompt", "--prompt-file", "--goal", "--orchestrate", "--acp",
		"--dream", "--export-output", "--check-update", "--perf-load",
		// Configuration layers
		"~/.goa/config.yaml", ".goa/config.yaml", ".goa/config.local.yaml",
		"GOA_HOME", "GOA_SERVER_AUTH_TOKEN", "--config", "--home",
		// Web UI specifics a user cannot guess
		"127.0.0.1:8080", "--server-auth", "--server-read-only",
		"--insecure-no-auth", "--server-max-clients",
		// MCP specifics
		"goa mcp list", "goa mcp add",
		// Generated sections
		"## Documentation", "## Command-line options",
	} {
		if !strings.Contains(manual, want) {
			t.Errorf("`goa --help` does not document %q", want)
		}
	}
}

// TestCLIManual_HasNoTUIArtifacts states the contract the user asked for: help
// is documentation, not a session. No escape sequence, no screen clearing.
func TestCLIManual_HasNoTUIArtifacts(t *testing.T) {
	manual := cliManual()
	for _, banned := range []string{"\x1b[", "\x1b]"} {
		if strings.Contains(manual, banned) {
			t.Errorf("manual contains the terminal control sequence %q", banned)
		}
	}
	if !strings.HasPrefix(manual, "# Goa") {
		t.Errorf("manual does not start with its title: %.60q", manual)
	}
}

// TestCLIShortUsage_PointsAtTheManual keeps the error path useful: a usage error
// must tell the user how to reach the full documentation.
func TestCLIShortUsage_PointsAtTheManual(t *testing.T) {
	for _, want := range []string{"goa --help", "goa server", "goa mcp", "goa help"} {
		if !strings.Contains(cliShortUsage, want) {
			t.Errorf("short usage does not mention %q", want)
		}
	}
}

func TestHelpTopic(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantTopic string
		wantOK    bool
	}{
		{"no args", nil, "", false},
		{"long help", []string{"--help"}, "", true},
		{"short help", []string{"-h"}, "", true},
		{"bare help verb", []string{"help"}, "", true},
		{"help with topic", []string{"help", "webui"}, "webui", true},
		{"help with help token", []string{"help", "--help"}, "", true},
		{"verb help long", []string{"server", "--help"}, "server", true},
		{"verb help short", []string{"server", "-h"}, "server", true},
		{"verb help word", []string{"server", "help"}, "server", true},
		{"mcp verb help", []string{"mcp", "help"}, "mcp", true},
		// Not help requests: they must fall through to the normal paths.
		{"server mode", []string{"server"}, "", false},
		{"unknown verb", []string{"serve"}, "", false},
		{"flag then help", []string{"--model", "x", "--help"}, "", false},
		{"prompt", []string{"--prompt", "hi"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topic, ok := helpTopic(tt.args)
			if ok != tt.wantOK || topic != tt.wantTopic {
				t.Errorf("helpTopic(%v) = (%q, %v), want (%q, %v)", tt.args, topic, ok, tt.wantTopic, tt.wantOK)
			}
		})
	}
}

func TestHelpText_Topics(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		want  []string
	}{
		{"manual", "", []string{"# Goa", "## Command-line options", "## Documentation"}},
		{"server unit", "server", []string{"goa server", "--server-auth", "--server-read-only"}},
		{"mcp unit", "mcp", []string{"goa mcp add", "goa mcp remove"}},
		{"webui doc", "webui", []string{"goa server", "127.0.0.1:8080"}},
		{"webui doc case-insensitive", "WEBUI", []string{"goa server"}},
		{"options section", "options", []string{"--model", "--server-addr"}},
		{"docs section", "docs", []string{"ARCHITECTURE", "TOOLS"}},
		{"cli doc", "cli", []string{"Command-Line Manual"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := helpText(tt.topic)
			if err != nil {
				t.Fatalf("helpText(%q): %v", tt.topic, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("helpText(%q) does not contain %q", tt.topic, want)
				}
			}
		})
	}
}

func TestHelpText_UnknownTopicListsAlternatives(t *testing.T) {
	_, err := helpText("nosuchtopic")
	if err == nil {
		t.Fatal("unknown topic accepted")
	}
	for _, want := range []string{"nosuchtopic", "available topics", "webui", "server", "options"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestHelpTopics_HasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, topic := range helpTopics() {
		if seen[topic] {
			t.Errorf("topic %q listed twice", topic)
		}
		seen[topic] = true
	}
	if !seen["server"] || !seen["docs"] || !seen["webui"] {
		t.Errorf("topic list is missing core entries: %v", helpTopics())
	}
}

func TestUnknownCommandError(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantErr  bool
		contains []string
	}{
		{"no args", nil, false, nil},
		{"serve typo suggests server", []string{"serve"}, true, []string{`"serve"`, "goa server", "goa --help"}},
		{"positional prompt suggests --prompt", []string{"fix the bug"}, true, []string{"fix the bug", "--prompt"}},
		{"empty verb", []string{""}, true, []string{"unknown command"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := unknownCommandError(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unknownCommandError(%v) = %v, want error: %v", tt.args, err, tt.wantErr)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestClosestVerb(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"serve", "server"},
		{"serveR", ""},
		{"srv", ""},
		{"s", ""},
		{"", ""},
		{"mc", ""},
		{"mcp", "mcp"},
	}
	for _, tt := range tests {
		if got := closestVerb(tt.cmd); got != tt.want {
			t.Errorf("closestVerb(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

// TestWriteHelp_NoTrailingGarbage keeps the printed manual clean enough to pipe
// into a pager.
func TestWriteHelp_NoTrailingGarbage(t *testing.T) {
	var b strings.Builder
	if err := writeHelp(&b, ""); err != nil {
		t.Fatalf("writeHelp: %v", err)
	}
	got := b.String()
	if !strings.HasSuffix(got, "\n") {
		t.Error("manual does not end with a newline")
	}
	if strings.Contains(got, "\n\n\n") {
		t.Error("manual contains a triple blank line")
	}
}

// optionTokenRE matches an option as written in the documentation.
var optionTokenRE = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// TestCLIPreamble_OnlyNamesRealFlags validates the manual's narrative against
// the live flag set: documenting an option that does not exist is worse than
// omitting it, because the user types it and gets a usage error.
func TestCLIPreamble_OnlyNamesRealFlags(t *testing.T) {
	// --help is a request the parser answers, not a registered flag.
	notFlags := map[string]bool{"--help": true}
	found := 0
	for _, tok := range optionTokenRE.FindAllString(cliPreamble(), -1) {
		if notFlags[tok] {
			continue
		}
		found++
		if cliFlags().fs.Lookup(strings.TrimPrefix(tok, "--")) == nil {
			t.Errorf("CLI.md documents %q, which is not a registered flag", tok)
		}
	}
	if found < 20 {
		t.Errorf("only %d options found in the manual narrative — the extraction or the document is wrong", found)
	}
}

// TestCLIManual_HeadlessExitCodesMatchTheCode pins the exit-code table to the
// constants the headless runner returns: scripts branch on these numbers, so a
// renumbering that leaves the documentation behind is a silent breakage.
func TestCLIManual_HeadlessExitCodesMatchTheCode(t *testing.T) {
	rows := []struct {
		code    int
		meaning string
	}{
		{headlessExitOK, "Success"},
		{headlessExitConfigError, "Configuration or empty-prompt error"},
		{headlessExitProviderError, "Provider/session start failure"},
		{headlessExitMaxTurns, "`--max-turns` exceeded"},
		{headlessExitTimeout, "`--timeout` expired"},
		{headlessExitGoalFailed, "Goal not achieved"},
		{headlessExitOrchFailed, "Orchestrator run failed"},
	}
	text := cliPreamble()
	for _, r := range rows {
		row := fmt.Sprintf("| `%d` | %s |", r.code, r.meaning)
		if !strings.Contains(text, row) {
			t.Errorf("CLI.md must document exit code %d as %q (missing row %q)", r.code, r.meaning, row)
		}
	}
}

// excerptAround returns a small window of text around the first occurrence of
// needle, for readable assertion failures.
func excerptAround(text, needle string) string {
	i := strings.Index(text, needle)
	if i < 0 {
		return ""
	}
	start := max(0, i-120)
	end := min(len(text), i+len(needle)+120)
	return text[start:end]
}
