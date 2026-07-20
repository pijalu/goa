// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"errors"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// mutatingStubTool is a StateMutator (like bash/edit/write): a successful
// execution may change shared state, so it bumps the state epoch.
type mutatingStubTool struct{ BaseTool }

func (mutatingStubTool) Schema() ToolSchema             { return ToolSchema{Name: "bash", Description: "stub"} }
func (mutatingStubTool) Execute(string) (string, error) { return "ok", nil }
func (mutatingStubTool) MutatesState() bool             { return true }

// readOnlyStubTool is NOT a StateMutator (like read/search): it never bumps
// the state epoch.
type readOnlyStubTool struct{ BaseTool }

func (readOnlyStubTool) Schema() ToolSchema             { return ToolSchema{Name: "read", Description: "stub"} }
func (readOnlyStubTool) Execute(string) (string, error) { return "ok", nil }

func newLoopAgent(t *testing.T, cfg Config) *Agent {
	t.Helper()
	if cfg.Model.Api == "" {
		cfg.Model = testModel(provider.ApiOpenAICompletions)
	}
	if cfg.Tools == nil {
		cfg.Tools = []Tool{mutatingStubTool{}, readOnlyStubTool{}}
	}
	return NewAgent(cfg)
}

// bufferOne simulates one streamed tool call through the buffer guardrail and
// returns the skip message ("" = the call would execute).
func bufferOne(a *Agent, name, args string) string {
	callKey := name + "::" + args
	a.mu.Lock()
	w, c, _, _ := a.recordToolCallInBudgetWindow(callKey)
	a.mu.Unlock()
	if msg := a.budgetOrRepeatSkipMessage(w, c); msg != "" {
		return msg
	}
	return a.errorStreakSkipMessage(name)
}

// The must-have case: edit -> test(fail) -> edit -> test(fail) must NEVER trip
// the loop guardrail. Each successful edit bumps the state epoch, so the exact
// same test command is always a fresh observation, not a stall.
func TestStateAwareHorizon_EditTestCycleNeverTrips(t *testing.T) {
	a := newLoopAgent(t, Config{
		MaxToolRepeatConsecutive: 2,
		MaxToolCalls:             3,
		MaxToolErrorStreak:       4,
	})

	testArgs := `{"cmd":"go test ./..."}`
	editArgs := `{"path":"x.go"}`

	for cycle := 0; cycle < 6; cycle++ {
		// edit succeeds -> state mutation -> epoch bump.
		if msg := bufferOne(a, "bash", editArgs); msg != "" {
			t.Fatalf("cycle %d: edit must execute, got skip: %q", cycle, msg)
		}
		a.recordToolExecOutcome("bash", nil)

		// identical test command, but state changed since its last run.
		if msg := bufferOne(a, "bash", testArgs); msg != "" {
			t.Fatalf("cycle %d: test after edit must execute (state changed), got skip: %q", cycle, msg)
		}
		// test FAILS (exit code != 0). A failed run is not a state mutation.
		a.recordToolExecOutcome("bash", errors.New("exit 1"))
	}
}

// Re-running the exact same call with NO state change in between is a genuine
// stall: the consecutive guard must still fire.
func TestStateAwareHorizon_NoChangeRepeatStillTrips(t *testing.T) {
	a := newLoopAgent(t, Config{MaxToolRepeatConsecutive: 2})

	args := `{"cmd":"go test ./..."}`
	a.recordToolExecOutcome("read", nil) // read-only: no epoch change

	if msg := bufferOne(a, "bash", args); msg != "" {
		t.Fatalf("1st call must execute, got: %q", msg)
	}
	a.recordToolExecOutcome("bash", errors.New("exit 1")) // fail = no epoch bump

	if msg := bufferOne(a, "bash", args); msg != "" {
		t.Fatalf("2nd identical call must execute (limit not reached), got: %q", msg)
	}
	a.recordToolExecOutcome("bash", errors.New("exit 1"))

	if msg := bufferOne(a, "bash", args); msg == "" {
		t.Errorf("3rd identical call with no state change should trip the loop guardrail")
	}
}

// A read-only call between repeats does NOT reset the horizon (it changes
// nothing), so spaced-out duplicates are still caught by the rolling window.
// Each read uses DISTINCT args (reading different files is not itself a
// repeat), so only the repeated identical test command accumulates.
func TestStateAwareHorizon_ReadDoesNotResetHorizon(t *testing.T) {
	a := newLoopAgent(t, Config{
		MaxToolRepeatConsecutive: 2,
		MaxToolCalls:             3,
	})

	testArgs := `{"cmd":"go test ./..."}`

	// Interleave the identical test command with DISTINCT reads; the window
	// should still accumulate the test occurrences and trip at MaxToolCalls(3)
	// on the 4th.
	trips := 0
	for i := 0; i < 4; i++ {
		if msg := bufferOne(a, "bash", testArgs); msg != "" {
			trips++
		}
		a.recordToolExecOutcome("bash", errors.New("exit 1"))
		// distinct read-only call in between: no epoch bump, no self-repeat.
		readArgs := `{"path":"file` + string(rune('a'+i)) + `.go"}`
		if msg := bufferOne(a, "read", readArgs); msg != "" {
			t.Fatalf("distinct read must never be skipped, got: %q", msg)
		}
		a.recordToolExecOutcome("read", nil)
	}
	if trips == 0 {
		t.Errorf("spaced-out identical calls with only reads between should trip the window guardrail")
	}
}

// A FAILED mutating call changes nothing, so it must NOT reset the horizon:
// the repeated identical call keeps accumulating in the rolling window.
func TestStateAwareHorizon_FailedMutationDoesNotReset(t *testing.T) {
	a := newLoopAgent(t, Config{
		MaxToolRepeatConsecutive: 2,
		MaxToolCalls:             3,
	})

	testArgs := `{"cmd":"go test ./..."}`
	editArgs := `{"path":"x.go"}`

	// Run the identical test 3 times, each preceded by a FAILED edit (no state
	// change). Because the failed edits never reset the horizon, the window
	// keeps accumulating test occurrences and trips at MaxToolCalls(3) on the
	// 4th.
	trips := 0
	for i := 0; i < 4; i++ {
		bufferOne(a, "bash", editArgs)
		a.recordToolExecOutcome("bash", errors.New("edit failed: old_string not found"))
		if msg := bufferOne(a, "bash", testArgs); msg != "" {
			trips++
		}
		a.recordToolExecOutcome("bash", errors.New("exit 1"))
	}
	if trips == 0 {
		t.Errorf("failed edits must not reset the horizon; repeated identical call should trip the window guardrail")
	}
}

// The error-streak guardrail catches the exact export failure: a model
// retrying ONE tool with ever-changing inputs that all fail.
func TestErrorStreak_ChangingArgsFailureSpiralTrips(t *testing.T) {
	a := newLoopAgent(t, Config{MaxToolErrorStreak: 4})

	// 4 consecutive failures with DIFFERENT args each time.
	for i := 0; i < 4; i++ {
		args := `{"code":"attempt ` + string(rune('a'+i)) + `"}`
		if msg := bufferOne(a, "python", args); msg != "" {
			t.Fatalf("attempt %d: python must execute before streak limit, got: %q", i, msg)
		}
		a.recordToolExecOutcome("python", errors.New("AttributeError: no attribute DOTALL"))
	}

	// The 5th call (different args again) trips the streak guardrail.
	if msg := bufferOne(a, "python", `{"code":"attempt e"}`); msg == "" {
		t.Errorf("5th consecutive failing python call (changing args) should trip the error-streak guardrail")
	}
}

// The streak nudge fires ONCE per episode; after the hint the tool may run
// again (so the model can apply a genuinely new approach), and a single
// success resets the streak.
func TestErrorStreak_NudgeOnceThenSuccessResets(t *testing.T) {
	a := newLoopAgent(t, Config{MaxToolErrorStreak: 3})

	for i := 0; i < 3; i++ {
		bufferOne(a, "python", `{"code":"x"}`)
		a.recordToolExecOutcome("python", errors.New("boom"))
	}
	// First trip: nudge.
	if msg := bufferOne(a, "python", `{"code":"y"}`); msg == "" {
		t.Fatalf("streak at limit should nudge")
	}
	// A further call in the same episode is NOT nudged again (one per episode).
	if msg := bufferOne(a, "python", `{"code":"z"}`); msg != "" {
		t.Errorf("second call in the same episode must not nudge again, got: %q", msg)
	}
	// ... and when it finally succeeds, the streak resets.
	a.recordToolExecOutcome("python", nil)
	if msg := bufferOne(a, "python", `{"code":"w"}`); msg != "" {
		t.Errorf("after a success the streak must reset, got: %q", msg)
	}
}

// Switching tools breaks the consecutive-error streak.
func TestErrorStreak_ToolSwitchResets(t *testing.T) {
	a := newLoopAgent(t, Config{MaxToolErrorStreak: 3})

	for i := 0; i < 3; i++ {
		bufferOne(a, "python", `{"code":"x"}`)
		a.recordToolExecOutcome("python", errors.New("boom"))
	}
	// A successful different tool resets the streak.
	bufferOne(a, "bash", `{"cmd":"ls"}`)
	a.recordToolExecOutcome("bash", nil)

	if msg := bufferOne(a, "python", `{"code":"new"}`); msg != "" {
		t.Errorf("switching to a successful tool must reset the python streak, got: %q", msg)
	}
}

// MutatesState classification: read-only tools must not bump the epoch,
// mutating tools must.
func TestToolMutatesStateClassification(t *testing.T) {
	a := newLoopAgent(t, Config{})
	if a.toolMutatesState("read") {
		t.Errorf("read-only tool must not be classified as mutating")
	}
	if !a.toolMutatesState("bash") {
		t.Errorf("bash (StateMutator) must be classified as mutating")
	}
	if a.toolMutatesState("nonexistent") {
		t.Errorf("unknown tool must not be classified as mutating")
	}
}
