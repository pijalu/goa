// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"os"

	"github.com/pijalu/goa/internal/telemetry"
	"github.com/pijalu/goa/internal/update"
	"github.com/pijalu/goa/internal/version"
)

// checkForUpdates runs an update check, optionally recording telemetry.
func checkForUpdates(subs *subsystems, telemetryEnabled bool) {
	current := version.Version()
	checker := update.NewChecker(current, subs.cfg.ConfigDir)
	result, err := checker.Check(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update check failed: %v\n", err)
		return
	}

	if checker.IsNewer(result.LatestVersion) {
		fmt.Printf("A new version of Goa is available: %s (current: %s)\n", result.LatestVersion, current)
		fmt.Printf("Release notes: %s\n", result.URL)
	} else {
		fmt.Printf("Goa is up to date (%s)\n", current)
	}

	if telemetryEnabled {
		tel := telemetry.NewClient(true, subs.cfg.ConfigDir)
		tel.Record("update_check", map[string]string{
			"current": current,
			"latest":  result.LatestVersion,
		})
		_ = tel.Flush()
	}
}
