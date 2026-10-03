// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// The injected terminal is what makes `goa server` possible: the engine must
// render into the injected VirtualTerminal instead of the process TTY.
func TestCreateTerminal_HonoursInjectedTerminal(t *testing.T) {
	cfg := &config.Config{}
	vt := webui.NewVirtualTerminal(80, 24)
	subs := &subsystems{cfg: cfg, terminal: vt}
	a := &App{subs: subs}

	got := a.createTerminal()
	if got != tui.Terminal(vt) {
		t.Fatalf("createTerminal ignored the injected terminal: %T", got)
	}
}

// With no injected terminal the app must behave exactly as before: the real
// process terminal. Asserted through the type, since NewProcessTerminal needs
// a controlling TTY only once Start runs.
func TestCreateTerminal_DefaultsToProcessTerminal(t *testing.T) {
	t.Setenv("GOA_DEBUG_TERMINAL", "")
	subs := &subsystems{cfg: &config.Config{}}
	a := &App{subs: subs}

	got := a.createTerminal()
	if _, ok := got.(*tui.ProcessTerminal); !ok {
		t.Fatalf("default terminal = %T, want *tui.ProcessTerminal", got)
	}
}

// The terminal debug log must keep wrapping the *injected* terminal, not the
// process one.
func TestCreateTerminal_DebugLogWrapsInjectedTerminal(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "term.log")
	cfg := &config.Config{}
	cfg.Logging.TerminalLog = logPath
	vt := webui.NewVirtualTerminal(40, 10)
	subs := &subsystems{cfg: cfg, terminal: vt}
	a := &App{subs: subs}

	got := a.createTerminal()
	if _, ok := got.(*tui.LogTerminal); !ok {
		t.Fatalf("terminal = %T, want a *tui.LogTerminal wrapper", got)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}

// `goa server` is dispatched as a subcommand: the verb must be stripped from
// argv before flag parsing, and the mode handed to runApp.
func TestStripSubcommand_Server(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })

	os.Args = []string{"goa", "server", "--server-addr", "127.0.0.1:9000"}
	got, err := stripSubcommand(os.Args, "server")
	if err != nil {
		t.Fatalf("stripSubcommand: %v", err)
	}
	if !got {
		t.Error("server verb not detected")
	}
	if len(os.Args) != 3 || os.Args[1] != "--server-addr" {
		t.Errorf("argv = %v, want the verb stripped", os.Args)
	}
}

func TestStripSubcommand_LeavesOtherArgvAlone(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })

	os.Args = []string{"goa", "--model", "x"}
	got, err := stripSubcommand(os.Args, "server")
	if err != nil {
		t.Fatalf("stripSubcommand: %v", err)
	}
	if got {
		t.Error("server mode must not be inferred from unrelated args")
	}
	if os.Args[1] != "--model" {
		t.Errorf("argv mutated: %v", os.Args)
	}
}

func TestWebSessionID_EmptyWithoutStore(t *testing.T) {
	if got := webSessionID(nil)(); got != "" {
		t.Errorf("nil subsystems session id = %q", got)
	}
	if got := webSessionID(&subsystems{})(); got != "" {
		t.Errorf("session-less subsystems id = %q", got)
	}
}

func TestRuntimeOptions_ValidateServerAddrUnconstrained(t *testing.T) {
	// Any host:port is accepted at flag level; binding failures surface at
	// Listen time with the address in the message.
	opts := RuntimeOptions{Server: true, ServerAddr: "0.0.0.0:99999"}
	if err := opts.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
