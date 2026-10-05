// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"log"
	"time"
)

// sessionCleanupInterval is how often session logs are re-checked while goa runs.
// The window is measured in days, so the tick exists to bound the work per sweep
// (a sweep is a directory read), not to react to a deadline.
const sessionCleanupInterval = 60 * time.Minute

// runSessionLogCleanup deletes session logs older than the configured retention
// window (sessions.retention, default 7 days, counted from each file's last
// write). It never touches the session this process has open, and it is
// best-effort: a session store that cannot be read or a file that cannot be
// removed never fails the run — the logs are history, not state the agent needs.
func (s *subsystems) runSessionLogCleanup() {
	if s.sessionStore == nil {
		return
	}
	window, ok := s.cfg.Sessions.SessionWindow()
	if !ok {
		return
	}
	if removed := s.sessionStore.PruneSessions(window); removed > 0 {
		log.Printf("session cleanup removed %d expired session log(s)", removed)
	}
}

// startSessionCleanup prunes once at startup and then periodically, the same way
// the image store is pruned (internal.PruneImages). The returned stop function
// terminates the background goroutine.
func (s *subsystems) startSessionCleanup() func() {
	s.runSessionLogCleanup()
	ticker := time.NewTicker(sessionCleanupInterval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				s.runSessionLogCleanup()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}
