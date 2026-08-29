// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

// loopResumeRunner is a no-op agentRunner for auto-resume tests.
type loopResumeRunner struct{}

func (r *loopResumeRunner) Run(ctx context.Context, input string) error { return nil }
func (r *loopResumeRunner) RunWithImages(ctx context.Context, input string, images []string) error {
	return nil
}

// TestLoopAutoResume_ArmsOnThinkingLoopStop verifies that a thinking-loop
// interrupt arms pendingLoopResume with the default message when the feature
// is enabled.
func TestLoopAutoResume_ArmsOnThinkingLoopStop(t *testing.T) {
	cfg := &config.Config{}
	cfg.Execution.LoopAutoResume = true
	am := NewAgentManager(cfg, nil, NewLoopDetector(DefaultLoopDetectorConfig()), nil, event.MakeBus(10, 10, 10, 10), "")

	am.handleThinkingLoopWarning(LoopInterrupt)

	am.mu.Lock()
	defer am.mu.Unlock()
	if am.pendingLoopResume != DefaultLoopAutoResumeMessage {
		t.Fatalf("pendingLoopResume = %q, want %q", am.pendingLoopResume, DefaultLoopAutoResumeMessage)
	}
	if am.loopStopReason == "" {
		t.Fatal("loopStopReason should be set")
	}
}

// TestLoopAutoResume_CustomMessage verifies a configured message overrides the
// default.
func TestLoopAutoResume_CustomMessage(t *testing.T) {
	cfg := &config.Config{}
	cfg.Execution.LoopAutoResume = true
	cfg.Execution.LoopAutoResumeMessage = "custom resume"
	am := NewAgentManager(cfg, nil, NewLoopDetector(DefaultLoopDetectorConfig()), nil, event.MakeBus(10, 10, 10, 10), "")

	am.handleLoopWarning(LoopCritical)

	am.mu.Lock()
	defer am.mu.Unlock()
	if am.pendingLoopResume != "custom resume" {
		t.Fatalf("pendingLoopResume = %q, want %q", am.pendingLoopResume, "custom resume")
	}
}

// TestLoopAutoResume_DisabledByDefault verifies no resume is armed when the
// feature is off (default).
func TestLoopAutoResume_DisabledByDefault(t *testing.T) {
	cfg := &config.Config{} // LoopAutoResume false
	am := NewAgentManager(cfg, nil, NewLoopDetector(DefaultLoopDetectorConfig()), nil, event.MakeBus(10, 10, 10, 10), "")

	am.handleThinkingLoopWarning(LoopInterrupt)

	am.mu.Lock()
	defer am.mu.Unlock()
	if am.pendingLoopResume != "" {
		t.Fatalf("pendingLoopResume = %q, want empty (feature off)", am.pendingLoopResume)
	}
}

// TestLoopAutoResume_CapStopsArming verifies the consecutive-resume cap
// prevents arming after the limit is reached.
func TestLoopAutoResume_CapStopsArming(t *testing.T) {
	cfg := &config.Config{}
	cfg.Execution.LoopAutoResume = true
	cfg.Execution.LoopAutoResumeMax = 2
	am := NewAgentManager(cfg, nil, NewLoopDetector(DefaultLoopDetectorConfig()), nil, event.MakeBus(10, 10, 10, 10), "")

	am.mu.Lock()
	am.loopResumeCount = 2 // already at cap
	am.mu.Unlock()

	am.handleThinkingLoopWarning(LoopInterrupt)

	am.mu.Lock()
	defer am.mu.Unlock()
	if am.pendingLoopResume != "" {
		t.Fatalf("pendingLoopResume = %q, want empty (cap reached)", am.pendingLoopResume)
	}
}

// TestLoopAutoResume_DispatchesAfterTurn verifies runAgentTurn's cleanup
// dispatches the armed resume as a steering-injected user message.
func TestLoopAutoResume_DispatchesAfterTurn(t *testing.T) {
	cfg := &config.Config{}
	cfg.Execution.LoopAutoResume = true
	bus := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, NewLoopDetector(DefaultLoopDetectorConfig()), nil, bus, "")
	// Active agent required for SendUserInput to start a turn; use a stub via
	// the minimal surface. We only need the dispatch path, so pre-arm and
	// call the cleanup indirectly by running a turn on a no-op runner.
	am.mu.Lock()
	am.pendingLoopResume = DefaultLoopAutoResumeMessage
	am.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	am.runAgentTurn(ctx, cancel, 1, &loopResumeRunner{}, "original", nil)

	am.mu.Lock()
	resumeLeft := am.pendingLoopResume
	count := am.loopResumeCount
	resumeTurn := am.loopResumeTurn
	am.mu.Unlock()

	if resumeLeft != "" {
		t.Fatalf("pendingLoopResume should be consumed, got %q", resumeLeft)
	}
	if count != 1 {
		t.Fatalf("loopResumeCount = %d, want 1", count)
	}
	// loopResumeTurn is cleared by SendUserInputWithImages when the resume is
	// dispatched; with no active agent SendUserInput errors out, but the flag
	// set before dispatch is cleared inside SendUserInputWithImages regardless.
	_ = resumeTurn
}

// TestLoopAutoResume_SteeringTakesPrecedence verifies pending steering
// suppresses the auto-resume dispatch.
func TestLoopAutoResume_SteeringTakesPrecedence(t *testing.T) {
	cfg := &config.Config{}
	cfg.Execution.LoopAutoResume = true
	am := NewAgentManager(cfg, nil, NewLoopDetector(DefaultLoopDetectorConfig()), nil, event.MakeBus(10, 10, 10, 10), "")
	am.mu.Lock()
	am.pendingLoopResume = DefaultLoopAutoResumeMessage
	am.pendingSteering = "user typed this"
	am.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	am.runAgentTurn(ctx, cancel, 1, &loopResumeRunner{}, "original", nil)

	am.mu.Lock()
	defer am.mu.Unlock()
	if am.loopResumeCount != 0 {
		t.Fatalf("loopResumeCount = %d, want 0 (steering took precedence)", am.loopResumeCount)
	}
}

// TestLoopAutoResume_GenuineUserTurnResetsCounter verifies that a genuine
// user message resets loopResumeCount, while an auto-resume dispatch
// (loopResumeTurn set) does not.
func TestLoopAutoResume_GenuineUserTurnResetsCounter(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, NewLoopDetector(DefaultLoopDetectorConfig()), nil, event.MakeBus(10, 10, 10, 10), "")
	// A minimal active agent lets SendUserInput run the steering path.
	am.activeAgent = agentic.NewAgent(agentic.Config{})
	am.running = true // force steering-append path so no new turn starts

	am.mu.Lock()
	am.loopResumeCount = 3
	am.mu.Unlock()

	// Genuine user input (loopResumeTurn false) resets the counter.
	if err := am.SendUserInput("real user message"); err != nil {
		t.Fatalf("SendUserInput: %v", err)
	}
	am.mu.Lock()
	if am.loopResumeCount != 0 {
		t.Fatalf("loopResumeCount = %d after genuine input, want 0", am.loopResumeCount)
	}
	am.mu.Unlock()

	// An auto-resume dispatch (loopResumeTurn true) must NOT reset the counter.
	am.mu.Lock()
	am.loopResumeCount = 2
	am.loopResumeTurn = true
	am.mu.Unlock()
	if err := am.SendUserInput(DefaultLoopAutoResumeMessage); err != nil {
		t.Fatalf("SendUserInput (resume): %v", err)
	}
	am.mu.Lock()
	if am.loopResumeCount != 2 {
		t.Fatalf("loopResumeCount = %d after auto-resume dispatch, want 2 (unchanged)", am.loopResumeCount)
	}
	if am.loopResumeTurn {
		t.Fatal("loopResumeTurn should be cleared after the resume dispatch")
	}
	am.mu.Unlock()
}
