// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"time"

	"github.com/pijalu/goa/core/goal"
)

// CleanupExpired removes queued goals whose UpdatedAt is older than retentionDays.
// A retentionDays value of 0 or negative means "keep forever".
func (s *GoalQueueStore) CleanupExpired(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.readLocked()
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	filtered := make([]goal.UpcomingGoal, 0, len(goals))
	removed := 0
	for _, g := range goals {
		if g.UpdatedAt.Before(cutoff) {
			removed++
			continue
		}
		filtered = append(filtered, g)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := s.writeLocked(filtered); err != nil {
		return 0, err
	}
	return removed, nil
}
