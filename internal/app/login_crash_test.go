// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/auth"
)

func TestLoginOpencodeOAuthUnsupportedDoesNotCrash(t *testing.T) {
	store, err := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatalf("auth store: %v", err)
	}
	a, engine, _, chat := newAsyncTestApp(t, commands.NewLoginCommand(store))
	defer engine.Stop()

	engine.ApplySync(func() { a.handleSlashCommand("/login:opencode:oauth") })
	waitFor(t, engine, time.Second, func() bool { return !a.commandBusy }, "login command did not finish")

	if !containsRendered(chat, `provider "opencode" does not support OAuth`) {
		t.Fatalf("expected a clean unsupported-OAuth error, got:\n%v", chat.Render(80))
	}
}
