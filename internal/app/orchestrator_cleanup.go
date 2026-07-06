// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"log"
	"path/filepath"
	"time"

	"github.com/pijalu/goa/core/orchestrator"
)

const orchestratorCleanupInterval = 60 * time.Minute

// runOrchestratorCleanup deletes expired orchestrator runs and goals based on
// the configured retention policies. It is safe to run concurrently with other
// orchestrator operations because deletions are best-effort and ignore "not
// found" races.
func (s *subsystems) runOrchestratorCleanup() {
	s.runOrchestratorRunCleanup()
	s.runGoalCleanup()
}

func (s *subsystems) runOrchestratorRunCleanup() {
	cfg := s.cfg.Orchestrator.Retention
	if !cfg.Enabled || cfg.Days <= 0 {
		return
	}
	rootDir := filepath.Join(s.projectDir, ".goa", "orchestrator")
	deleted, err := orchestrator.CleanupExpiredRuns(rootDir, cfg.Days)
	if err != nil {
		log.Printf("orchestrator cleanup failed: %v", err)
		return
	}
	if deleted > 0 && s.logger != nil {
		log.Printf("orchestrator cleanup removed %d expired run(s)", deleted)
	}
}

func (s *subsystems) runGoalCleanup() {
	cfg := s.cfg.Goals.Retention
	if !cfg.Enabled || cfg.Days <= 0 {
		return
	}
	if s.goalManager == nil {
		return
	}
	if err := s.goalManager.Mode.CleanupExpired(cfg.Days); err != nil {
		log.Printf("goal cleanup failed: %v", err)
	}
	removed, err := s.goalManager.Queue.CleanupExpired(cfg.Days)
	if err != nil {
		log.Printf("goal queue cleanup failed: %v", err)
		return
	}
	if removed > 0 {
		log.Printf("goal queue cleanup removed %d expired goal(s)", removed)
	}
}

// startOrchestratorCleanup runs cleanup once and then schedules it
// periodically. The returned stop function terminates the background goroutine.
func (s *subsystems) startOrchestratorCleanup() func() {
	s.runOrchestratorCleanup()
	ticker := time.NewTicker(orchestratorCleanupInterval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				s.runOrchestratorCleanup()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}
