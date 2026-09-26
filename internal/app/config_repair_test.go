// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// repairTestApp builds the full clarify harness (engine + chat + editor) plus
// a loader whose home config carries the given YAML, wires the loader report
// into the subsystems, and returns the app with a goroutine-ready surface.
func repairTestApp(t *testing.T, homeYAML string) (*App, *tui.TUI, *clarifyKeyTerminal, string, *config.LoadReport) {
	t.Helper()
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	internal.SetGoaHome(homeDir)
	t.Cleanup(func() { internal.SetGoaHome("") })

	homeCfg := filepath.Join(homeDir, ".goa", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(homeCfg), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeCfg, []byte(homeYAML), 0o644); err != nil {
		t.Fatalf("write home config: %v", err)
	}

	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, rep, err := loader.LoadWithReport()
	if err != nil || cfg == nil {
		t.Fatalf("LoadWithReport: cfg=%v err=%v", cfg, err)
	}

	term := &clarifyKeyTerminal{}
	term.w, term.h = 100, 30
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)
	engine.RunLoops()

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.inputEditor = inp
	subs.cfg = cfg
	subs.loader = loader
	subs.cfgReport = rep
	return New(subs), engine, term, homeCfg, rep
}

// TestOfferConfigRepair_YesRewritesBrokenConfig drives the startup repair
// offer end to end: a garbage stall value forces the defaults fallback, the
// app announces it and offers the confirmed repair, and confirming rewrites
// the file (original backed up) so a restart loads the user's own config.
func TestOfferConfigRepair_YesRewritesBrokenConfig(t *testing.T) {
	app, engine, term, homeCfg, rep := repairTestApp(t, "execution:\n  activity_timeout: soon\n")
	if !rep.UsedDefaults {
		t.Fatalf("precondition: garbage stall value must force the defaults fallback, got %+v", rep)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.announceConfigIssues()
		app.offerConfigRepair()
	}()

	// The fallback announcement and the repair question both surface in the UI.
	waitForVisibleText(t, engine, "starting with the default configuration")
	waitForVisibleText(t, engine, repairOptionNo)
	term.sendKey("\r") // "Yes — write corrected values (backup kept)" is preselected

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("repair offer did not return after confirmation")
	}

	repaired, err := os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read repaired config: %v", err)
	}
	if strings.Contains(string(repaired), "soon") {
		t.Errorf("unparseable stall value must be removed, file now:\n%s", repaired)
	}
	backups, _ := filepath.Glob(homeCfg + ".bak-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	orig, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(string(orig), "soon") {
		t.Errorf("backup must hold the original bytes, got:\n%s", orig)
	}
	// The repaired file must load cleanly.
	if _, err := config.NewCascadeLoader(filepath.Dir(filepath.Dir(homeCfg)), "", nil).Load(); err != nil {
		t.Errorf("repaired config must load: %v", err)
	}
	waitForVisibleText(t, engine, "Restart goa to apply the corrected configuration")
}

// TestOfferConfigRepair_NoLeavesFilesUntouched pins the consent requirement:
// declining (Esc) leaves every config file byte-identical and points the user
// at manual repair instead.
func TestOfferConfigRepair_NoLeavesFilesUntouched(t *testing.T) {
	app, engine, term, homeCfg, _ := repairTestApp(t, "execution:\n  activity_timeout: soon\n")

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.announceConfigIssues()
		app.offerConfigRepair()
	}()

	waitForVisibleText(t, engine, repairOptionNo)
	term.sendKey("\x1b") // Esc cancels the selector

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("repair offer did not return after cancellation")
	}

	got, err := os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(got), "soon") {
		t.Errorf("declined repair must not touch the file, got:\n%s", got)
	}
	backups, _ := filepath.Glob(homeCfg + ".bak-*")
	if len(backups) != 0 {
		t.Errorf("no backup must be created for a declined repair, got %v", backups)
	}
	waitForVisibleText(t, engine, "fix them by hand")
}

// TestAnnounceConfigIssues_CleanReportIsSilent verifies a clean load produces
// no announcements and no repair offer (the common path must stay quiet).
func TestAnnounceConfigIssues_CleanReportIsSilent(t *testing.T) {
	app, engine, _, homeCfg, rep := repairTestApp(t, "execution:\n  activity_timeout: 2m\n")
	if !rep.Empty() {
		t.Fatalf("precondition: clean config must yield an empty report, got %+v", rep)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.announceConfigIssues()
		app.offerConfigRepair()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("clean report must not block on any offer")
	}

	engine.RenderNow()
	for _, l := range engine.AgentFrame().Visible {
		if strings.Contains(l, "Repair configuration files?") || strings.Contains(l, "default configuration") {
			t.Errorf("clean load must stay silent, but frame shows: %s", l)
		}
	}
	if _, err := os.Stat(homeCfg + ".bak-00000000-000000"); !os.IsNotExist(err) {
		t.Errorf("no backup must exist, stat err: %v", err)
	}
}
