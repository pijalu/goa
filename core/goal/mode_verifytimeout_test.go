// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"context"
	"testing"
	"time"
)

// timeoutVerifier implements CommandVerifier and the optional
// SetVerifyTimeout live-update hook, recording the last bound it received.
type timeoutVerifier struct {
	fakeVerifier
	lastTimeout time.Duration
	sets        int
}

func (v *timeoutVerifier) SetVerifyTimeout(d time.Duration) {
	v.lastTimeout = d
	v.sets++
}

// TestSetVerifyTimeout_ForwardsToVerifier covers the live-update path used by
// /config:set goals.verify_timeout: the bound reaches a verifier that
// supports it, and is a no-op for verifiers that do not (startup-bound) or
// when nothing is wired.
func TestSetVerifyTimeout_ForwardsToVerifier(t *testing.T) {
	tv := &timeoutVerifier{}
	m := gatedMode(t, DoneGateEvidence)
	m.SetVerifier(tv, true)

	m.SetVerifyTimeout(10 * time.Minute)
	if tv.sets != 1 || tv.lastTimeout != 10*time.Minute {
		t.Fatalf("SetVerifyTimeout(10m): sets=%d last=%v, want 1 forward of 10m", tv.sets, tv.lastTimeout)
	}

	// A verifier without the optional method must not break the update.
	m.SetVerifier(&fakeVerifier{ok: true}, true)
	m.SetVerifyTimeout(time.Minute) // must not panic
	if tv.sets != 1 {
		t.Errorf("unsupported verifier must not receive the update, sets=%d", tv.sets)
	}

	// No verifier wired: no panic.
	empty := gatedMode(t, DoneGateEvidence)
	empty.SetVerifyTimeout(time.Minute)
}

// TestSetVerifyTimeout_ThenVerifyUsesVerifier ensures the push does not
// disturb the gate: after a live bound change the recorded verify command
// still runs through the verifier at completion.
func TestSetVerifyTimeout_ThenVerifyUsesVerifier(t *testing.T) {
	tv := &timeoutVerifier{fakeVerifier: fakeVerifier{ok: true, output: "ok"}}
	m := gatedMode(t, DoneGateEvidence)
	m.SetVerifier(tv, true)
	m.SetVerifyTimeout(30 * time.Minute)

	cmd := "go test ./..."
	if _, err := m.CreateGoal(CreateGoalInput{
		Objective:           "green build",
		CompletionCriterion: ptrString("go test ./... passes"),
		VerifyCommand:       &cmd,
		Replace:             true,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteClosed {
		t.Fatalf("verifier ok: completion must close, got %v", res.Outcome)
	}
	if len(tv.calls) != 1 || tv.calls[0] != cmd {
		t.Fatalf("verify calls = %v, want [%q]", tv.calls, cmd)
	}
}
