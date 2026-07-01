// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/trust"
)

func TestTrustCommandList(t *testing.T) {
	m := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	cmd := &TrustCommand{Manager: m}
	if err := cmd.Run(core.Context{}, []string{"allow", "plugins"}); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if err := cmd.Run(core.Context{}, []string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestTrustCommandDeny(t *testing.T) {
	m := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	cmd := &TrustCommand{Manager: m}
	if err := cmd.Run(core.Context{}, []string{"deny", "skills"}); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if m.IsTrusted("skills") {
		t.Error("expected untrusted")
	}
}

func TestTrustCommandNoManager(t *testing.T) {
	cmd := &TrustCommand{}
	if err := cmd.Run(core.Context{}, nil); err == nil {
		t.Fatal("expected error")
	}
}
