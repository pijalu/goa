// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DeleteRun removes a run's persisted directory from disk. It does not stop a
// running runtime; callers must cancel the active runtime first if needed.
func DeleteRun(rootDir, internalID string) error {
	if internalID == "" {
		return fmt.Errorf("orchestrator: cannot delete run with empty id")
	}
	dir := filepath.Join(rootDir, internalID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("orchestrator: delete run %s: %w", internalID, err)
	}
	return nil
}

// DeleteAllRuns removes every run directory under rootDir. It returns a count
// of deleted directories and any error from the directory walk.
func DeleteAllRuns(rootDir string) (int, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("orchestrator: list runs for delete: %w", err)
	}
	var deleted int
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(rootDir, ent.Name())
		if err := os.RemoveAll(dir); err != nil {
			return deleted, fmt.Errorf("orchestrator: delete run %s: %w", ent.Name(), err)
		}
		deleted++
	}
	return deleted, nil
}

// StopActiveRun cancels and waits for the active runtime if its internal ID
// matches the one being deleted. It clears the holder so the run is no longer
// surfaced as active. The timeout bounds how long we wait for graceful exit.
func StopActiveRun(active *ActiveRuntime, internalID string, timeout time.Duration) {
	if active == nil || internalID == "" {
		return
	}
	rt := active.Get()
	if rt == nil || rt.RunID() != internalID {
		return
	}
	active.Clear(rt)
	rt.Cancel()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-rt.Done():
	case <-ctx.Done():
		// Give up waiting; directory deletion will proceed.
	}
}

// ExpiredRuns returns the internal IDs of runs whose UpdatedAt is older than
// retentionDays. A retentionDays value of 0 or negative means "never expire".
func ExpiredRuns(rootDir string, retentionDays int) ([]string, error) {
	if retentionDays <= 0 {
		return nil, nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	summaries, err := ListRuns(rootDir)
	if err != nil {
		return nil, err
	}
	var expired []string
	for _, s := range summaries {
		if !s.Finished {
			continue
		}
		if s.UpdatedAt.Before(cutoff) {
			expired = append(expired, s.RunID)
		}
	}
	return expired, nil
}

// CleanupExpiredRuns deletes all finished runs older than retentionDays. It
// returns the number of runs deleted.
func CleanupExpiredRuns(rootDir string, retentionDays int) (int, error) {
	ids, err := ExpiredRuns(rootDir, retentionDays)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := DeleteRun(rootDir, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
