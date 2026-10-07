// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"io"
	"log"
	"os"
)

// Main is the top-level entry point used by cmd/goa. It parses CLI flags,
// loads config, initializes subsystems, and runs the application loop.
func Main() {
	log.SetOutput(io.Discard)
	// Capture runtime fatal errors (which bypass recover()) and log output to
	// .goa/crash.log. Deferred BEFORE handleShutdown so the shutdown handler
	// runs first during unwind and can still writeCrashLog to the open file.
	wd, _ := os.Getwd()
	crashCleanup := setupCrashLog(wd)
	defer crashCleanup()
	defer handleShutdown()

	// Help is answered before any subsystem starts, so `goa --help`,
	// `goa help [topic]` and `goa <verb> --help` print documentation and exit
	// without loading config, running the first-run wizard or starting the TUI.
	if runHelpCLI(os.Args[1:]) {
		return
	}

	// `goa mcp ...` is handled before flag parsing: it is a plain CLI
	// subcommand (no TUI, no headless agent) for managing MCP servers.
	if runMCPCLI(os.Args[1:]) {
		return
	}

	// `goa attach` is a plain CLI subcommand with its own flag set: it never
	// builds this process's session, it drives a remote one.
	if runAttachCLI(os.Args[1:]) {
		return
	}

	// `goa server` is likewise a subcommand: the verb is stripped from argv
	// before flag parsing, then handed to runApp as a mode selector (spec §19
	// decision 5).
	serverMode := stripSubcommand(os.Args, "server")

	for {
		relaunch := runApp(serverMode)
		if !relaunch {
			break
		}
	}
}

// stripSubcommand removes a leading `name` verb from argv so the flag parser
// never sees it, and reports whether the verb was present. Verb-scoped help
// (`goa name --help`) never reaches this function: Main answers help before any
// parsing.
func stripSubcommand(argv []string, name string) bool {
	if len(argv) < 2 || argv[1] != name {
		return false
	}
	os.Args = append([]string{argv[0]}, argv[2:]...)
	return true
}
