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
// every healed value, every dropped layer, and a defaults-fallback notice.
// Load time already warned on stderr (visible in terminal scrollback /
// headless runs); these flashes reach the TUI user who never sees stderr.
// No-op with a nil or empty report.
func (a *App) announceConfigIssues() {
	rep := a.subs.cfgReport
	if rep == nil || rep.Empty() {
		return
	}
	for _, h := range rep.Healed {
		a.flashFromReporter("Config healed: " + h)
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

// offerConfigRepair asks the user to confirm rewriting the invalid config
// files into a loadable shape (RepairLayerFile; the originals are backed up).
// It is offered whenever the startup report shows a dropped layer or a
// defaults fallback — the two states where the file on disk lost against the
// loader. A "no" (or Esc) leaves everything untouched; the flashes then point
// at the file so the user can fix it by hand. Never blocks startup
// semantics: the session is already running on defaults/healed values.
func (a *App) offerConfigRepair() {
	rep := a.subs.cfgReport
	if rep == nil || (len(rep.Dropped) == 0 && !rep.UsedDefaults) {
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
	summary.WriteString("goa can rewrite the broken file(s) into a loadable shape (bare timings get units, unreadable values are removed). Originals are kept as *.bak-<timestamp>.")

	card := tui.NewClarifyCard("Repair configuration files?",
		summary.String(),
		"Write corrected values now? Changes apply after a restart.",
		[]string{repairOptionYes, repairOptionNo})
	answer, ok := a.clarify(card)
	if !ok || answer != repairOptionYes {
		a.flashFromReporter("Config files left unchanged — fix them by hand and restart to apply.")
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
		return fmt.Sprintf("Repaired %d file(s). Restart goa to apply the corrected configuration.%s", repaired, results.String())
	case results.Len() > 0:
		return fmt.Sprintf("Nothing needed rewriting, but some files could not be repaired:%s", results.String())
	default:
		return "Nothing needed rewriting — restart to re-check the configuration."
	}
}

// repairCandidatePaths lists the config files worth running RepairLayerFile
// on: the sources the loader actually dropped, plus — for a defaults fallback,
// where the offending key's file is not identifiable from the merged error —
// every existing writable layer (RepairLayerFile is a no-op on valid files).
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
