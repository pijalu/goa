// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

// Repair-question wording. Kept as constants so tests can drive the selector
// without duplicating strings.
const (
	repairOptionYes = "Yes — write corrected values (backup kept)"
	repairOptionNo  = "No — leave the files; I'll fix them"
)

// announceConfigIssues surfaces the loader's self-healing report in the chat:
// every healed value (the error and the correction it applied), every dropped
// layer, and a defaults-fallback notice.
// Load time already warned on stderr (visible in terminal scrollback /
// headless runs); these flashes reach the TUI user who never sees stderr.
// No-op with a nil or empty report.
func (a *App) announceConfigIssues() {
	rep := a.subs.cfgReport
	if rep == nil || rep.Empty() {
		return
	}
	for _, h := range rep.Healed {
		a.flashFromReporter("Config corrected in memory, file unchanged: " + h.Describe())
	}
	for _, d := range rep.Dropped {
		a.flashFromReporter(fmt.Sprintf("Ignoring invalid config %s: %v — defaults apply for its settings",
			d.Source, d.Err))
	}
	if rep.UsedDefaults {
		a.flashFromReporter(fmt.Sprintf("Config error: %v — starting with the default configuration",
			rep.FallbackErr))
	}
}

// offerConfigRepair asks the user to confirm rewriting the config files that
// lost against the loader into the loadable shape goa already used at startup
// (RepairLayerFile; the originals are backed up).
//
// It is offered whenever the startup report is non-empty — a dropped layer, a
// defaults fallback, OR an in-memory heal. A heal only rewrites memory: the file
// on disk keeps carrying the poison, so without this confirmation the loader
// re-detects and re-reports the same correction on every single start and the
// message never goes away. A "no" (or Esc) leaves everything untouched; the
// flash and name the file so the user can fix it by hand. Never blocks startup
// semantics: the session is already running on defaults/healed values.
//
// The question is CLOSED: Yes/No is the whole answer set (the free-text
// "type your own answer" escape hatch exists for model-authored questions, not
// for a host-owned rewrite consent).
func (a *App) offerConfigRepair() {
	rep := a.subs.cfgReport
	if rep == nil || rep.Empty() {
		return
	}
	paths := a.repairCandidatePaths(rep)
	if len(paths) == 0 {
		return
	}

	var summary strings.Builder
	if rep.UsedDefaults {
		fmt.Fprintf(&summary, "Your configuration could not be used (%v), so goa started on defaults.\n", rep.FallbackErr)
	}
	for _, d := range rep.Dropped {
		fmt.Fprintf(&summary, "%s could not be read (%v).\n", d.Source, d.Err)
	}
	for _, h := range rep.Healed {
		fmt.Fprintf(&summary, "• %s\n", h.Describe())
	}
	summary.WriteString("goa can write these corrections into the file(s) (bare timings get units, contradictory or unreadable values are removed). Originals are kept as *.bak-<timestamp>.")

	card := tui.NewClosedClarifyCard("Repair configuration files?",
		summary.String(),
		"Write the corrections to disk now? They take effect immediately and stop this warning.",
		[]string{repairOptionYes, repairOptionNo})
	answer, ok := a.clarify(card)
	if !ok || answer != repairOptionYes {
		a.flashFromReporter("Config files left unchanged — goa keeps applying the corrections in memory and will ask again on every start.")
		return
	}
	a.flashFromReporter(a.applyConfirmedRepair(paths))
}

// applyConfirmedRepair runs RepairLayerFile on each candidate path and
// returns the user-facing result flash.
func (a *App) applyConfirmedRepair(paths []string) string {
	var results strings.Builder
	repaired := 0
	for _, p := range paths {
		backup, err := a.subs.loader.RepairLayerFile(p)
		switch {
		case err != nil:
			fmt.Fprintf(&results, "\n✗ %s", err)
		case backup == "":
			// Already loadable — nothing to do for this file.
		default:
			repaired++
			fmt.Fprintf(&results, "\n✓ %s repaired (backup: %s)", p, backup)
		}
	}
	switch {
	case repaired > 0:
		return fmt.Sprintf("Repaired %d file(s) — the corrections are on disk now and load as-is.%s", repaired, results.String())
	case results.Len() > 0:
		return fmt.Sprintf("Nothing needed rewriting, but some files could not be repaired:%s", results.String())
	default:
		return "Nothing needed rewriting — the files already match the loaded configuration."
	}
}

// repairCandidatePaths lists the config files worth running RepairLayerFile on:
// the source of every healed value (the correction lives in memory only, so the
// file still carries the poison), the layers the loader dropped, and — for a
// defaults fallback, where the offending key's file is not identifiable from the
// merged error — every existing writable layer (RepairLayerFile is a no-op on
// valid files).
func (a *App) repairCandidatePaths(rep *config.LoadReport) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, h := range rep.Healed {
		add(h.Source)
	}
	for _, d := range rep.Dropped {
		add(d.Source)
	}
	if rep.UsedDefaults {
		for _, p := range a.subs.loader.WritableConfigPaths() {
			if _, err := os.Stat(p); err == nil {
				add(p)
			}
		}
	}
	return out
}

// flashFromReporter posts a flash through the commandLoop (the sole state
// owner) so callers outside event handlers can surface messages safely.
func (a *App) flashFromReporter(text string) {
	a.apply(func() { a.flash(text) })
}
