// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/telemetry"
)

func TestTelemetryCommandToggle(t *testing.T) {
	client := telemetry.NewClient(true, t.TempDir())
	cmd := &TelemetryCommand{Client: client}
	if err := cmd.Run(core.Context{}, []string{"off"}); err != nil {
		t.Fatalf("off: %v", err)
	}
	if client.Enabled() {
		t.Error("expected disabled")
	}
	if err := cmd.Run(core.Context{}, []string{"on"}); err != nil {
		t.Fatalf("on: %v", err)
	}
	if !client.Enabled() {
		t.Error("expected enabled")
	}
}

func TestTelemetryCommandNoClient(t *testing.T) {
	cmd := &TelemetryCommand{}
	if err := cmd.Run(core.Context{}, nil); err == nil {
		t.Fatal("expected error")
	}
}
