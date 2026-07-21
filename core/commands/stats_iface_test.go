package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
)

// TestStatsCommand_ImplementsArgCompleter locks the interface assertion the
// app-layer arg completer relies on (buildArgCompleter type-asserts
// core.ArgCompleter before delegating to CompleteArgs).
func TestStatsCommand_ImplementsArgCompleter(t *testing.T) {
	var _ core.ArgCompleter = (*StatsCommand)(nil)
	var ac core.ArgCompleter = &StatsCommand{}
	if ac.CompleteArgs(core.Context{}, "pro")[0].Value != "project" {
		t.Fatal("project completion broken")
	}
}
