// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/auth"
)

func TestLoginCommandStoreToken(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"github", "mytoken"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if got, ok := store.Get("github"); !ok || got.AccessToken != "mytoken" {
		t.Errorf("token = %+v", got)
	}
}

func TestLoginCommandList(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	_ = store.Set("github", nil)
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, nil); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestLoginCommandNoStore(t *testing.T) {
	cmd := &LoginCommand{}
	if err := cmd.Run(core.Context{}, []string{"github"}); err == nil {
		t.Fatal("expected error")
	}
}
