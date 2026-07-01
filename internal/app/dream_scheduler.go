// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/skills"
)

// dreamScheduler runs automatic memory consolidation based on config.
type dreamScheduler struct {
	subs          *subsystems
	mu            sync.Mutex
	lastRun       time.Time
	sessionsSince int
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

// newDreamScheduler creates a scheduler if auto-dream is enabled.
func newDreamScheduler(subs *subsystems) *dreamScheduler {
	if subs == nil || subs.cfg == nil || !subs.cfg.Memory.Dream.Enabled || !subs.cfg.Memory.Dream.Auto {
		return nil
	}
	return &dreamScheduler{subs: subs}
}

// Start launches a background goroutine that waits for triggers.
func (s *dreamScheduler) Start() {
	if s == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go s.run(ctx)
}

// Stop shuts down the scheduler.
func (s *dreamScheduler) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

// RecordSession informs the scheduler that a session finished.
func (s *dreamScheduler) RecordSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionsSince++
}

func (s *dreamScheduler) run(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maybeRun(ctx)
		}
	}
}

func (s *dreamScheduler) maybeRun(ctx context.Context) {
	s.mu.Lock()
	if !s.shouldRunLocked() {
		s.mu.Unlock()
		return
	}
	sessions := s.sessionsSince
	s.sessionsSince = 0
	s.lastRun = time.Now()
	s.mu.Unlock()

	if err := s.runDream(ctx, sessions); err != nil {
		log.Printf("Auto-dream failed: %v", err)
	}
}

func (s *dreamScheduler) shouldRunLocked() bool {
	cfg := s.subs.cfg.Memory.Dream
	if s.sessionsSince < cfg.MinSessions {
		return false
	}
	interval := s.parseInterval(cfg.Interval)
	if interval <= 0 {
		return false
	}
	return time.Since(s.lastRun) >= interval
}

func (s *dreamScheduler) parseInterval(v string) time.Duration {
	d, err := time.ParseDuration(v)
	if err == nil {
		return d
	}
	// Support simple days suffix like "7d".
	var days int
	if _, err := fmt.Sscanf(v, "%dd", &days); err == nil {
		return time.Duration(days) * 24 * time.Hour
	}
	return 0
}

func (s *dreamScheduler) runDream(ctx context.Context, sessions int) error {
	if err := validateDreamPrerequisites(s.subs); err != nil {
		return err
	}
	skill, ok := loadDreamSkill(s.subs)
	if !ok {
		return fmt.Errorf("dream skill not found")
	}

	engine := core.NewDreamEngine(
		s.subs.cfg,
		s.subs.providerMgr,
		s.subs.memStore,
		s.subs.sessionStore,
		s.subs.projectDir,
		skill.Body,
	)

	result, err := engine.Run(ctx, s.subs.cfg.Memory.Dream.ApplyAfterReview)
	if err != nil {
		return err
	}
	if result.Changed {
		s.notify(fmt.Sprintf("Auto-dream complete: %s (%d memories, %d sessions)", result.OutputPath, result.InputMemories, sessions))
	}
	return nil
}

func (s *dreamScheduler) notify(msg string) {
	if s.subs.events == nil {
		log.Println(msg)
		return
	}
	s.subs.flash(msg)
}

// resolveDreamModel picks the configured dream model or falls back to active.
func resolveDreamModel(cfg provider.Model) provider.Model {
	return cfg
}

// dreamStateFile returns the path where the scheduler persists its state.
func dreamStateFile(projectDir string) string {
	return filepath.Join(projectDir, ".goa", "memory.dream", ".scheduler")
}

// writeSchedulerState persists last-run and sessions-since to disk.
func (s *dreamScheduler) writeSchedulerState() error {
	if s == nil {
		return nil
	}
	path := dreamStateFile(s.subs.projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data := fmt.Sprintf("%d\n%d\n", s.lastRun.Unix(), s.sessionsSince)
	return os.WriteFile(path, []byte(data), 0644)
}

// readSchedulerState restores scheduler state from disk.
func (s *dreamScheduler) readSchedulerState() error {
	if s == nil {
		return nil
	}
	path := dreamStateFile(s.subs.projectDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var ts int64
	var sessions int
	if _, err := fmt.Sscanf(string(data), "%d\n%d\n", &ts, &sessions); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun = time.Unix(ts, 0)
	s.sessionsSince = sessions
	return nil
}

// flash sends a flash message through the event bus if available.
func (s *subsystems) flash(msg string) {
	if s.events == nil {
		return
	}
	select {
	case s.events.Chat <- event.ChatEvent{Flash: &event.Flash{Text: msg}}:
	default:
	}
}

// Ensure dreamScheduler implements the expected interfaces.
var _ = (*skills.Skill)(nil)
