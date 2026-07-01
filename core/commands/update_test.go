// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/update"
)

func TestUpdateCommand(t *testing.T) {
	dir := t.TempDir()
	checker := update.NewChecker("v0.0.1", dir)
	_ = checker.WriteCache(&update.CheckResult{LatestVersion: "v1.0.0", URL: "http://example.com", CheckedAt: time.Now()})
	cmd := &UpdateCommand{Checker: checker}
	if err := cmd.Run(core.Context{}, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
}
