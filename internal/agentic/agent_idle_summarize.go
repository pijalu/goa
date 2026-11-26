// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// IdleSummarizeConfig configures a single summarize attempt after a completed
// exchange has remained idle. A zero Delay disables the SDK feature.
type IdleSummarizeConfig struct {
	// TriggerPercent is the minimum projected context-window occupancy required
	// when the timer fires. Zero permits any non-empty history.
	TriggerPercent int
	// Delay is the inactivity period after a completed stop. Zero disables the
	// timer; negative values are treated as disabled for direct SDK callers.
	Delay time.Duration
}

type idleSummarizeResult struct {
	attempted bool
	duplicate bool
	cancelled bool
	err       error
}

type idleSummarizeAfterFunc func(time.Duration, func()) *time.Timer

// idleSummarizeScheduler owns timer lifetime independently from Agent.mu.
// Every activity/reset boundary advances generation, making already-queued
// timer callbacks harmless. A generation permits at most one callback attempt.
type idleSummarizeScheduler struct {
	mu         sync.Mutex
	runFn      func(context.Context) error
	afterFunc  idleSummarizeAfterFunc
	config     IdleSummarizeConfig
	timer      *time.Timer
	generation uint64
	attempted  bool
	idle       bool
	running    bool
	cancel     context.CancelFunc
	closed     bool
}

func newIdleSummarizeScheduler(runFn func(context.Context) error) *idleSummarizeScheduler {
	return newIdleSummarizeSchedulerWithAfterFunc(runFn, time.AfterFunc)
}

func newIdleSummarizeSchedulerWithAfterFunc(runFn func(context.Context) error, afterFunc idleSummarizeAfterFunc) *idleSummarizeScheduler {
	return &idleSummarizeScheduler{runFn: runFn, afterFunc: afterFunc}
}

// Configure replaces the settings and invalidates pending work. When the agent
// is idle, the new delay is armed immediately; active agents arm at their next
// completed stop instead.
func (s *idleSummarizeScheduler) Configure(cfg IdleSummarizeConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = cfg
	s.invalidateLocked()
	if s.idle {
		s.armLocked()
	}
}

// Started marks a new exchange/reset boundary. It stops a pending timer and
// cancels an in-flight provider request. The activity lock in Agent provides
// the subsequent wait for that request to unwind.
func (s *idleSummarizeScheduler) Started() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateLocked()
	s.idle = false
}

// Stopped marks the processing owner's completed stop and arms exactly one
// timer. Queued Run calls never invoke this method.
func (s *idleSummarizeScheduler) Stopped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateLocked()
	s.idle = true
	s.armLocked()
}

// Close permanently disables the scheduler and cancels outstanding work.
func (s *idleSummarizeScheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.invalidateLocked()
	s.idle = false
}

func (s *idleSummarizeScheduler) invalidateLocked() {
	s.generation++
	s.attempted = false
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *idleSummarizeScheduler) armLocked() {
	if s.closed || !s.idle || s.config.Delay <= 0 {
		return
	}
	generation := s.generation
	s.timer = s.afterFunc(s.config.Delay, func() {
		s.runAtGeneration(context.Background(), generation)
	})
}

func (s *idleSummarizeScheduler) run(ctx context.Context, runFn func(context.Context) error) idleSummarizeResult {
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	return s.runAtGenerationWith(ctx, generation, runFn)
}

func (s *idleSummarizeScheduler) runAtGeneration(ctx context.Context, generation uint64) idleSummarizeResult {
	return s.runAtGenerationWith(ctx, generation, s.runFn)
}

func (s *idleSummarizeScheduler) runAtGenerationWith(ctx context.Context, generation uint64, runFn func(context.Context) error) idleSummarizeResult {
	s.mu.Lock()
	if s.closed || !s.idle || generation != s.generation {
		cancelled := generation != s.generation || s.closed
		s.mu.Unlock()
		return idleSummarizeResult{cancelled: cancelled}
	}
	if s.attempted || s.running {
		s.mu.Unlock()
		return idleSummarizeResult{duplicate: true}
	}
	s.attempted = true
	s.running = true
	s.timer = nil
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	err := runFn(runCtx)
	cancelled := runCtx.Err() != nil || errors.Is(err, context.Canceled)

	s.mu.Lock()
	if generation != s.generation || s.closed {
		cancelled = true
	}
	s.running = false
	s.cancel = nil
	cancel()
	s.mu.Unlock()
	return idleSummarizeResult{attempted: true, cancelled: cancelled, err: err}
}

// runIdleSummarize executes the same explicit summarize path as
// /compact:summarize. TryLock makes an active turn or another exclusive history
// operation win without waiting; the consumed generation is not rearmed.
func (a *Agent) runIdleSummarize(ctx context.Context) error {
	if !a.activityMu.TryLock() {
		return nil
	}
	defer a.activityMu.Unlock()

	a.mu.Lock()
	if a.processing || len(a.history) == 0 {
		a.mu.Unlock()
		return nil
	}
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		a.mu.Unlock()
		return nil
	}
	cfg := a.cfg.ContextCompression.IdleSummarize
	stats := a.computeContextStatsForMax(maxTokens)
	a.mu.Unlock()
	if stats.UsagePercent < cfg.TriggerPercent {
		return nil
	}

	if err := a.compressHistoryWith(ctx, CompressionSummarize, true); err != nil {
		if ctx.Err() == nil {
			a.emitIdleSummarizeFailure(err)
		}
		return err
	}
	a.emitContextStats()
	return nil
}

func (a *Agent) emitIdleSummarizeFailure(err error) {
	msg := Message{
		Type:     Content,
		Role:     System,
		Content:  fmt.Sprintf("[goa-system] Idle summarize failed: %v", err),
		Metadata: map[string]string{"category": "system-notification"},
	}
	a.mu.Lock()
	a.history = append(a.history, msg)
	a.mu.Unlock()
	a.emitMessage(msg)
}

func (a *Agent) idleSummarizeStarted() {
	if a.idleSummarize != nil {
		a.idleSummarize.Started()
	}
}

func (a *Agent) idleSummarizeStopped() {
	if a.idleSummarize != nil {
		a.idleSummarize.Stopped()
	}
}

// lockHistoryActivity starts an exclusive history operation. Invalidation is
// repeated after acquiring the lock: a turn that was finishing while we waited
// may have armed its post-stop timer in the interim.
func (a *Agent) lockHistoryActivity() {
	a.idleSummarizeStarted()
	a.activityMu.Lock()
	a.idleSummarizeStarted()
}
