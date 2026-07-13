// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

// TestRuntime_DelegateAsync_ReturnsImmediately verifies that DelegateAsync
// returns a placeholder without waiting for the specialist turn.
func TestRuntime_DelegateAsync_ReturnsImmediately(t *testing.T) {
	var runs atomic.Int32
	cfg := config.OrchestratorConfig{
		Roles:    map[string]config.OrchestratorRole{"coder": {Model: "m"}},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(_, _ string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", "coder", "m")
		h.Run = func(ctx context.Context, _ string) error {
			time.Sleep(50 * time.Millisecond)
			runs.Add(1)
			return nil
		}
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	start := time.Now()
	out, err := rt.DelegateAsync(context.Background(), "coder", "task", AcquireOptions{})
	if err != nil {
		t.Fatalf("DelegateAsync: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("DelegateAsync blocked for %v, want immediate return", elapsed)
	}
	if !strings.Contains(out, "delegated") {
		t.Fatalf("expected placeholder with 'delegated', got %q", out)
	}
	if runs.Load() != 0 {
		t.Fatalf("specialist should not have finished yet, runs=%d", runs.Load())
	}

	rt.WaitForDelegations()
	if runs.Load() != 1 {
		t.Fatalf("expected 1 specialist run after wait, got %d", runs.Load())
	}
}

// TestRuntime_WaitForDelegations_ResumesAfterPending verifies that
// WaitForDelegations returns immediately when no async delegations are in
// flight and blocks until the last pending one finishes otherwise.
func TestRuntime_WaitForDelegations_ResumesAfterPending(t *testing.T) {
	var runs atomic.Int32
	cfg := config.OrchestratorConfig{
		Roles:    map[string]config.OrchestratorRole{"coder": {Model: "m"}},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(_, _ string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", "coder", "m")
		h.Run = func(ctx context.Context, _ string) error {
			time.Sleep(50 * time.Millisecond)
			runs.Add(1)
			return nil
		}
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		rt.WaitForDelegations()
	}()
	select {
	case <-done:
		// good: no pending delegations, wait returns immediately
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WaitForDelegations blocked with no pending delegations")
	}

	if _, err := rt.DelegateAsync(context.Background(), "coder", "task", AcquireOptions{}); err != nil {
		t.Fatalf("DelegateAsync: %v", err)
	}

	done = make(chan struct{})
	go func() {
		defer close(done)
		rt.WaitForDelegations()
	}()
	select {
	case <-done:
		if runs.Load() != 1 {
			t.Fatalf("expected specialist to have run, got %d", runs.Load())
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("WaitForDelegations did not unblock after delegate finished")
	}
}

// TestRuntime_HubConversationStyleRunsSynthesisEvenIfOrchestratorSpoke verifies
// that the hub always runs a synthesis turn when specialist outputs exist,
// even if the orchestrator's planning turn produced a message.
func TestRuntime_HubConversationStyleRunsSynthesisEvenIfOrchestratorSpoke(t *testing.T) {
	var rtRef *Runtime
	var orchRuns atomic.Int32
	var synthPrompt string
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		switch role {
		case "orchestrator":
			h.Run = func(ctx context.Context, prompt string) error {
				orchRuns.Add(1)
				if strings.Contains(prompt, "Specialist outputs:") {
					synthPrompt = prompt
					rtRef.RecordAgentMessage(h, "synthesis: done")
					return nil
				}
				rtRef.RecordAgentMessage(h, "orchestrator planning message")
				if _, err := rtRef.DelegateAsync(ctx, "coder", "do work", AcquireOptions{}); err != nil {
					return err
				}
				return nil
			}
		case "coder":
			h.Run = func(ctx context.Context, _ string) error {
				rtRef.RecordAgentMessage(h, "coder output")
				return nil
			}
		}
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rtRef = rt

	go func() { _ = rt.Run(context.Background(), "build feature") }()
	drainEvents(rt.Events())

	if orchRuns.Load() != 2 {
		t.Fatalf("expected 2 orchestrator turns (plan + synthesis), got %d", orchRuns.Load())
	}
	if synthPrompt == "" {
		t.Fatal("synthesis prompt was not produced")
	}
	if !strings.Contains(synthPrompt, "coder output") {
		t.Fatalf("synthesis prompt missing coder output: %s", synthPrompt)
	}
}

// TestRuntime_HubLoop_PauseForUserAnswer verifies that the orchestrator can
// ask the user a question, the loop pauses, and the conversation continues
// after the user answers via SteerOrchestrator.
func TestRuntime_HubLoop_PauseForUserAnswer(t *testing.T) {
	rt, orchRuns, final := setupPauseRuntime(t)

	done := make(chan error, 1)
	go func() { done <- rt.Run(context.Background(), "objective") }()

	waitForAskUserEvent(t, rt)

	if !rt.SteerOrchestrator("blue") {
		t.Fatal("SteerOrchestrator should consume the user answer")
	}

	drainEvents(rt.Events())
	if err := <-done; err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if orchRuns.Load() != 2 {
		t.Fatalf("expected 2 orchestrator turns (ask + answer), got %d", orchRuns.Load())
	}
	if final.value == "" {
		t.Fatal("final message was not produced after user answer")
	}
}

type stringHolder struct{ value string }

func setupPauseRuntime(t *testing.T) (*Runtime, *atomic.Int32, *stringHolder) {
	var rtRef *Runtime
	var orchRuns atomic.Int32
	final := &stringHolder{}
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		if role == "orchestrator" {
			h.Run = func(ctx context.Context, prompt string) error {
				orchRuns.Add(1)
				if orchRuns.Load() == 1 {
					rtRef.AskUser("what is your favorite color?")
					return nil
				}
				rtRef.RecordAgentMessage(h, "final answer")
				final.value = "final answer"
				return nil
			}
		}
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rtRef = rt
	return rt, &orchRuns, final
}

func waitForAskUserEvent(t *testing.T, rt *Runtime) {
	askUserSeen := make(chan struct{})
	go func() {
		for ev := range rt.Events() {
			if ev.Type == EventAskUser {
				close(askUserSeen)
				return
			}
		}
	}()

	select {
	case <-askUserSeen:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("ask_user event was not emitted")
	}
}

// TestRuntime_HubLoop_Rework verifies that the orchestrator can delegate,
// then request a rework, and the loop continues with the revised output.
func TestRuntime_HubLoop_Rework(t *testing.T) {
	rt, orchRuns, coderRuns, final := setupReworkRuntime(t)

	go func() { _ = rt.Run(context.Background(), "build feature") }()
	drainEvents(rt.Events())

	if coderRuns.Load() != 2 {
		t.Fatalf("expected coder to run twice (delegate + rework), got %d", coderRuns.Load())
	}
	if orchRuns.Load() != 3 {
		t.Fatalf("expected 3 orchestrator turns (delegate + rework + final), got %d", orchRuns.Load())
	}
	if final.value == "" {
		t.Fatal("final message was not produced after rework")
	}
}

func setupReworkRuntime(t *testing.T) (*Runtime, *atomic.Int32, *atomic.Int32, *stringHolder) {
	var rtRef *Runtime
	var orchRuns atomic.Int32
	var coderRuns atomic.Int32
	final := &stringHolder{}
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		if role == "orchestrator" {
			h.Run = makeReworkOrchestratorRun(rtRef, &orchRuns, final, h)
		} else if role == "coder" {
			h.Run = makeReworkCoderRun(rtRef, &coderRuns, h)
		}
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rtRef = rt
	return rt, &orchRuns, &coderRuns, final
}

func makeReworkOrchestratorRun(rtRef *Runtime, orchRuns *atomic.Int32, final *stringHolder, h *AgentHandle) func(context.Context, string) error {
	return func(ctx context.Context, prompt string) error {
		orchRuns.Add(1)
		switch orchRuns.Load() {
		case 1:
			_, err := rtRef.DelegateAsync(ctx, "coder", "write code", AcquireOptions{})
			return err
		case 2:
			_, err := rtRef.ReworkAsync(ctx, "coder", "add more tests", AcquireOptions{})
			return err
		default:
			rtRef.RecordAgentMessage(h, "final answer")
			final.value = "final answer"
			return nil
		}
	}
}

func makeReworkCoderRun(rtRef *Runtime, coderRuns *atomic.Int32, h *AgentHandle) func(context.Context, string) error {
	return func(ctx context.Context, _ string) error {
		coderRuns.Add(1)
		rtRef.RecordAgentMessage(h, fmt.Sprintf("coder output v%d", coderRuns.Load()))
		return nil
	}
}

// drainEvents consumes the runtime event bus until it closes.
func drainEvents(events <-chan Event) {
	for range events {
	}
}
