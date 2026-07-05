// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/orchestrator"
)

func TestOrchestrateCommand_Help(t *testing.T) {
	cmd := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime()}
	if cmd.ShortHelp() == "" {
		t.Error("ShortHelp should not be empty")
	}
	if cmd.LongHelp() == "" {
		t.Error("LongHelp should not be empty")
	}
	if cmd.Name() != "orchestrate" {
		t.Errorf("Name = %q, want orchestrate", cmd.Name())
	}
	if len(cmd.Aliases()) == 0 {
		t.Error("expected at least one alias (orch)")
	}
}

func TestOrchestrateCommand_Completion(t *testing.T) {
	cmd := &OrchestrateCommand{}
	comps := cmd.CompleteArgs(core.Context{}, "")
	if len(comps) == 0 {
		t.Fatal("expected completion entries")
	}
	haveNew := false
	for _, c := range comps {
		if c.Value == "new" {
			haveNew = true
		}
	}
	if !haveNew {
		t.Errorf("completion missing 'new'; got %+v", comps)
	}
}
