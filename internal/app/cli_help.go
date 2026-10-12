// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pijalu/goa/docs"
)

// The command-line manual is assembled from four sources, each the single
// source of truth for what it contributes, so `goa --help` cannot drift away
// from the binary it documents:
//
//   - docs/CLI.md — the narrative (modes, configuration, files, examples). It is
//     embedded documentation, so `goa help cli` and the goa://CLI namespace serve
//     the user exactly what the model reads.
//   - cliUnits — one entry per command-line surface. A unit's Body is the same
//     text `goa <verb> --help` prints, so verb help and the manual section can
//     never diverge.
//   - docs.List() — the documentation index, making every feature reachable via
//     `goa help <topic>`.
//   - flag.CommandLine — the option reference, generated from the live flag set,
//     so a newly registered flag appears without touching this file.

// helpToken reports whether an argv element asks for help.
func helpToken(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "help":
		return true
	}
	return false
}

// cliUnit is one command-line surface: the verb that selects it, the heading it
// gets in the manual, and the one function rendering its help text.
type cliUnit struct {
	Name    string // verb, also the `goa help <name>` topic
	Title   string // manual section heading
	Summary string // one-line index entry
	Body    func() string
}

// cliUnits is the registry of command-line surfaces. Adding a surface here adds
// it to the manual, to `goa help <verb>`, and to `goa <verb> --help` at once.
var cliUnits = []cliUnit{
	{
		Name:    "server",
		Title:   "Web UI (goa server)",
		Summary: "Serve the TUI as a web page over HTTP/WebSocket",
		Body:    func() string { return webServerUsage },
	},
	{
		Name:    "attach",
		Title:   "Attach a terminal (goa attach)",
		Summary: "Drive a goa server session from this terminal",
		Body:    func() string { return attachUsage },
	},
	{
		Name:    "mcp",
		Title:   "MCP servers (goa mcp)",
		Summary: "Install, list, toggle and remove MCP servers from the shell",
		Body:    func() string { return mcpCLIUsage },
	},
}

// cliUnitByName resolves a verb (case-insensitively) to its unit.
func cliUnitByName(name string) (cliUnit, bool) {
	target := strings.TrimSpace(strings.ToLower(name))
	for _, u := range cliUnits {
		if u.Name == target {
			return u, true
		}
	}
	return cliUnit{}, false
}

// flagGroup is a titled block of options in the generated reference.
type flagGroup struct {
	Title string
	Flags []string
}

// flagGroups orders the generated option reference. The set must cover every
// registered flag exactly once; cli_help_test.go fails the build when a flag is
// added without a home here, which is what keeps `goa --help` complete.
var flagGroups = []flagGroup{
	{"Model & provider", []string{
		"model", "provider", "endpoint", "api-key", "temperature", "max-tokens",
		"reasoning", "thinking-level", "thinking-blocks",
	}},
	{"Agent & session behaviour", []string{
		"profile", "execution-mode", "skill-mode", "compression", "no-memory",
		"memory-budget", "no-plugins", "max-turns", "timeout", "max-stream-rounds",
		"max-consecutive-tool-rounds", "max-tool-calls", "max-tool-repeat-consecutive",
		"max-tool-repeat-total", "tool-call-limit-reset-window",
	}},
	{"Headless & automation", []string{
		"prompt", "prompt-file", "goal", "orchestrate", "yes", "plain", "color",
	}},
	{"Web UI (goa server)", []string{
		"server-addr", "server-read-only", "server-max-clients", "server-cells",
		"server-auth", "server-auth-user", "server-auth-password",
		"server-auth-token", "insecure-no-auth", "server-projects-root",
		"server-session-idle", "server-max-sessions",
	}},
	{"Other interfaces", []string{"acp"}},
	{"Configuration & paths", []string{"config", "home"}},
	{"Interface & theme", []string{"theme", "show-thinking"}},
	{"Diagnostics & logging", []string{
		"debug", "debug-keys", "logfile", "terminal-log", "render-log", "capture-stream",
	}},
	{"Profiling & load testing", []string{
		"cpuprofile", "memprofile", "trace", "with-profiling", "perf-load",
		"perf-load-duration",
	}},
	{"Maintenance & exports", []string{
		"dream", "dream-apply", "check-update", "telemetry", "export-output",
		"export-session", "include-global-log",
	}},
}

// cliShortUsage is the one-screen synopsis printed on a usage error, where the
// full manual would bury the mistake in a wall of text.
const cliShortUsage = `Usage:
  goa [options]                        Interactive TUI (default mode)
  goa --prompt "<text>" [options]      Headless: run one prompt and exit
  goa server [options]                 Serve the TUI as a web page
  goa attach --server HOST:PORT        Drive a goa server session from here
  goa mcp <subcommand>                 Manage MCP servers from the shell
  goa help [topic]                     Full manual, or one topic

Run "goa --help" for the complete manual: all modes, every option, and the
index of embedded documentation.`

// helpTopic classifies a command-line help request and returns the topic it
// asks about. An empty topic means the full manual. ok is false when the
// invocation is not a help request at all.
func helpTopic(args []string) (topic string, ok bool) {
	if len(args) == 0 {
		return "", false
	}
	switch args[0] {
	case "-h", "-help", "--help":
		return "", true
	case "help":
		if len(args) > 1 && !helpToken(args[1]) {
			return args[1], true
		}
		return "", true
	}
	// Verb-scoped help: `goa server --help`, `goa mcp help`.
	if u, found := cliUnitByName(args[0]); found && len(args) > 1 && helpToken(args[1]) {
		return u.Name, true
	}
	return "", false
}

// helpText renders the manual (empty topic) or a single topic.
func helpText(topic string) (string, error) {
	trimmed := strings.TrimSpace(topic)
	if trimmed == "" {
		return cliManual(), nil
	}
	switch strings.ToLower(trimmed) {
	case "options", "flags":
		return cliOptionsSection(), nil
	case "docs", "documentation":
		return cliDocsSection(), nil
	}
	if u, found := cliUnitByName(trimmed); found {
		return bodyOf(u), nil
	}
	if info, err := docs.FindDocFile(trimmed); err == nil {
		if content, getErr := docs.Get(info.Name); getErr == nil {
			return content, nil
		}
	}
	return "", unknownTopicError(trimmed)
}

// writeHelp writes the manual or one topic to w.
func writeHelp(w io.Writer, topic string) error {
	text, err := helpText(topic)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, text)
	return err
}

// exitWithHelp prints the manual (or one topic) and exits: the single exit
// primitive every help path shares. A bad topic is a usage error, not a crash.
func exitWithHelp(topic string) {
	if err := writeHelp(os.Stdout, topic); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		exitAfterFlush(2)
	}
	exitAfterFlush(0)
}

// runHelpCLI answers a command-line help request and reports whether the
// invocation was one. It exits the process itself: help must be printed even
// when the caller is a shell that will not wait for a deferred flush, so every
// exit goes through exitAfterFlush.
func runHelpCLI(args []string) bool {
	topic, ok := helpTopic(args)
	if !ok {
		return false
	}
	exitWithHelp(topic)
	return true
}

// cliManual returns the complete manual printed by `goa --help`.
func cliManual() string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(cliPreamble(), "\n"))
	b.WriteString("\n")
	for _, u := range cliUnits {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", u.Title, strings.TrimRight(bodyOf(u), "\n"))
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(cliDocsSection(), "\n"))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimRight(cliOptionsSection(), "\n"))
	b.WriteString("\n")
	return b.String()
}

// cliPreamble returns the embedded narrative manual (docs/CLI.md). A missing or
// unreadable document degrades to the synopsis instead of printing nothing.
func cliPreamble() string {
	text, err := docs.Get("CLI")
	if err != nil {
		return "# Goa\n\n" + cliShortUsage + "\n"
	}
	return text
}

// bodyOf normalizes a unit body: documentation text always ends in exactly one
// newline so sections compose predictably.
func bodyOf(u cliUnit) string {
	return strings.TrimRight(u.Body(), "\n") + "\n"
}

// cliDocsSection indexes every embedded document: a feature that ships with a
// document is reachable from the manual without editing it.
func cliDocsSection() string {
	var b strings.Builder
	b.WriteString("## Documentation\n\n")
	b.WriteString("Every document below is embedded in the binary. Read one with\n")
	b.WriteString("`goa help <topic>` on the command line, or `goa://<TOPIC>` from a prompt.\n\n")

	list, err := docs.List()
	if err != nil || len(list) == 0 {
		fmt.Fprintf(&b, "(embedded documentation unavailable: %v)\n", err)
		return b.String()
	}
	width := 0
	for _, d := range list {
		if len(d.Name) > width {
			width = len(d.Name)
		}
	}
	for _, d := range list {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, d.Name, d.Description)
	}
	return b.String()
}

// cliOptionsSection generates the option reference from the live flag set.
func cliOptionsSection() string {
	var b strings.Builder
	b.WriteString("## Command-line options\n\n")
	b.WriteString("Generated from the live flag set: every option below is one this binary\n")
	b.WriteString("accepts. Boolean flags take no value (`--flag`); all others take one\n")
	b.WriteString("(`--model gpt-4o` or `--model=gpt-4o`).\n")
	for _, g := range flagGroups {
		b.WriteString("\n### " + g.Title + "\n\n")
		for _, name := range g.Flags {
			b.WriteString(optionLine(name))
		}
	}
	return b.String()
}

// optionLine renders one option. A flag that is registered but missing from
// flagGroups is reported in the output itself rather than silently dropped —
// cli_help_test.go turns that marker into a test failure.
func optionLine(name string) string {
	f := cliFlags().fs.Lookup(name)
	if f == nil {
		return "  (undocumented option: --" + name + ")\n"
	}
	placeholder, usage := flag.UnquoteUsage(f)
	left := "  --" + f.Name
	if placeholder != "" {
		left += " " + placeholder
	}
	if len(left) < 30 {
		left += strings.Repeat(" ", 30-len(left))
	} else {
		left += "\n    "
	}
	line := left + usage
	if v := f.DefValue; v != "" && v != "false" && v != "0" {
		line += " (default: " + v + ")"
	}
	return line + "\n"
}

// helpTopics lists everything `goa help` accepts, deduplicated: the generated
// sections, the command-line units and every embedded document.
func helpTopics() []string {
	seen := map[string]bool{}
	topics := make([]string, 0, 8+len(cliUnits))
	add := func(topic string) {
		if topic == "" || seen[topic] {
			return
		}
		seen[topic] = true
		topics = append(topics, topic)
	}
	add("options")
	add("docs")
	for _, u := range cliUnits {
		add(u.Name)
	}
	if list, err := docs.List(); err == nil {
		for _, d := range list {
			add(strings.ToLower(d.Name))
		}
	}
	return topics
}

// unknownTopicError lists everything `goa help` can be asked about, so a typo
// points at the valid topics instead of dead-ending.
func unknownTopicError(topic string) error {
	return fmt.Errorf("unknown help topic %q\navailable topics: %s\nrun \"goa --help\" for the full manual",
		topic, strings.Join(helpTopics(), ", "))
}

// unknownCommandError classifies leftover positional arguments. Goa takes none:
// a stray word is either a mistyped subcommand (`goa serve`) or a prompt passed
// positionally. Silently ignoring it used to start the TUI, which looked like
// goa doing nothing at all.
func unknownCommandError(args []string) error {
	if len(args) == 0 {
		return nil
	}
	cmd := args[0]
	if suggestion := closestVerb(cmd); suggestion != "" {
		return fmt.Errorf("unknown command %q\n  did you mean: goa %s\n  run \"goa --help\" for the full manual",
			cmd, suggestion)
	}
	return fmt.Errorf("unknown command %q\n  goa takes no positional arguments — pass a prompt with --prompt %q\n  run \"goa --help\" for the full manual",
		cmd, cmd)
}

// closestVerb suggests a known verb for a mistyped one (`serve` -> `server`).
// It only matches on shared prefixes and ignores very short input, so it never
// invents a suggestion out of unrelated words.
func closestVerb(cmd string) string {
	if len(cmd) < 3 {
		return ""
	}
	for _, u := range cliUnits {
		if strings.HasPrefix(u.Name, cmd) || strings.HasPrefix(cmd, u.Name) {
			return u.Name
		}
	}
	return ""
}
