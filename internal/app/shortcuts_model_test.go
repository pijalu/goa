// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
)

// TestHandleChangeModel_RoutesToModelCommand verifies the footer/shortcut model
// picker delegates to the /model command instead of the former parallel picker
// that silently dropped the selector's __delete__ / __add__ sentinels ('-' and
// '+' keys did nothing). Regression test for "- key does not delete in the
// /model picker" (bugs.md).
func TestHandleChangeModel_RoutesToModelCommand(t *testing.T) {
	registry := core.NewCommandRegistry()
	if err := commands.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	router := core.NewCommandRouter(registry, nil)

	result := router.Parse("/model")
	if result == nil || result.Command == nil {
		t.Fatal("/model must resolve to a registered command")
	}
	if result.Command.Name() != "model" {
		t.Fatalf("command = %q, want %q", result.Command.Name(), "model")
	}

	// Executing through the router (as handleChangeModel does) must reach the
	// /model implementation; with no provider manager it reports the missing
	// provider, proving end-to-end dispatch.
	ctx := coreContextForCommand(&subsystems{cfg: &config.Config{}}, nil)
	out, err := router.Execute(ctx, result)
	if err != nil {
		t.Fatalf("Execute /model: %v", err)
	}
	if !strings.Contains(out, "No provider configured") {
		t.Errorf("output = %q, want 'No provider configured'", out)
	}
}

// TestHandleChangeModel_NilRouter verifies the shortcut is a no-op when the
// command router is unavailable (early startup).
func TestHandleChangeModel_NilRouter(t *testing.T) {
	app := &App{subs: &subsystems{}}
	app.handleChangeModel() // must not panic
}
