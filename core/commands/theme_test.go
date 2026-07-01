// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

func TestThemeCommandList(t *testing.T) {
	store := config.NewThemeStore(t.TempDir())
	cmd := &ThemeCommand{Store: store}
	if err := cmd.Run(core.Context{}, nil); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestThemeCommandSet(t *testing.T) {
	store := config.NewThemeStore(t.TempDir())
	cmd := &ThemeCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"set", "default"}); err != nil {
		t.Fatalf("set: %v", err)
	}
}

func TestThemeCommandNoStore(t *testing.T) {
	cmd := &ThemeCommand{}
	if err := cmd.Run(core.Context{}, nil); err == nil {
		t.Fatal("expected error")
	}
}
