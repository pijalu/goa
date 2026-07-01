// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
)

// queuedGoalsFile is the on-disk layout for the queue store.
type queuedGoalsFile struct {
	Version int                 `json:"version"`
	Goals   []goal.UpcomingGoal `json:"goals"`
}

// GoalQueueStore persists an ordered list of upcoming goals.
type GoalQueueStore struct {
	path string
	mu   sync.Mutex
}

// NewGoalQueueStore creates a queue store backed by the given path.
func NewGoalQueueStore(path string) *GoalQueueStore {
	return &GoalQueueStore{path: path}
}

// Read returns the current queued goals in order.
func (s *GoalQueueStore) Read() ([]goal.UpcomingGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

// UsedNames returns the set of friendly names currently in use by queued
// goals. Implements goal.NamePool so the active-goal generator can avoid
// collisions when the queue is non-empty.
func (s *GoalQueueStore) UsedNames() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	goals, err := s.readLocked()
	if err != nil {
		return nil
	}
	return s.namesLocked(goals)
}

// namesLocked builds the set of friendly names from an in-memory goal slice.
// Caller must hold s.mu.
func (s *GoalQueueStore) namesLocked(goals []goal.UpcomingGoal) map[string]bool {
	taken := make(map[string]bool, len(goals))
	for _, g := range goals {
		if g.Name != "" {
			taken[g.Name] = true
		}
	}
	return taken
}

func (s *GoalQueueStore) readLocked() ([]goal.UpcomingGoal, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file queuedGoalsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Goals, nil
}

func (s *GoalQueueStore) writeLocked(goals []goal.UpcomingGoal) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file := queuedGoalsFile{Version: 1, Goals: goals}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Append adds a new goal to the end of the queue.
func (s *GoalQueueStore) Append(objective string) ([]goal.UpcomingGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	name := internal.FriendlyNameUnique(s.namesLocked(goals))
	now := time.Now()
	goals = append(goals, goal.UpcomingGoal{
		ID:        generateQueueID(),
		Name:      name,
		Objective: strings.TrimSpace(objective),
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err := s.writeLocked(goals); err != nil {
		return nil, err
	}
	return goals, nil
}

// Update replaces the objective of the goal with the given ID.
func (s *GoalQueueStore) Update(id, objective string) ([]goal.UpcomingGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	found := false
	for i := range goals {
		if goals[i].ID == id {
			goals[i].Objective = strings.TrimSpace(objective)
			goals[i].UpdatedAt = time.Now()
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("queued goal %q not found", id)
	}
	if err := s.writeLocked(goals); err != nil {
		return nil, err
	}
	return goals, nil
}

// Remove removes the goal with the given ID from the queue.
func (s *GoalQueueStore) Remove(id string) ([]goal.UpcomingGoal, *goal.UpcomingGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.readLocked()
	if err != nil {
		return nil, nil, err
	}
	var removed goal.UpcomingGoal
	haveRemoved := false
	filtered := make([]goal.UpcomingGoal, 0, len(goals))
	for i := range goals {
		if goals[i].ID == id {
			// Capture by value: a previous implementation aliased the
			// backing array (goals[:0]) and returned a pointer into it,
			// which subsequent appends silently overwrote with the wrong
			// element. See review CORE-BUG-1.
			removed = goals[i]
			haveRemoved = true
			continue
		}
		filtered = append(filtered, goals[i])
	}
	if !haveRemoved {
		return nil, nil, fmt.Errorf("queued goal %q not found", id)
	}
	if err := s.writeLocked(filtered); err != nil {
		return nil, nil, err
	}
	return filtered, &removed, nil
}

// Move shifts a goal up or down by one position.
func (s *GoalQueueStore) Move(id string, direction string) ([]goal.UpcomingGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range goals {
		if goals[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("queued goal %q not found", id)
	}

	switch direction {
	case "up":
		if idx == 0 {
			return goals, nil
		}
		goals[idx], goals[idx-1] = goals[idx-1], goals[idx]
	case "down":
		if idx == len(goals)-1 {
			return goals, nil
		}
		goals[idx], goals[idx+1] = goals[idx+1], goals[idx]
	default:
		return nil, fmt.Errorf("invalid direction %q (use up or down)", direction)
	}

	if err := s.writeLocked(goals); err != nil {
		return nil, err
	}
	return goals, nil
}

// Restore puts a goal back into the queue at the front.
func (s *GoalQueueStore) Restore(item goal.UpcomingGoal) ([]goal.UpcomingGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	goals = append([]goal.UpcomingGoal{item}, goals...)
	if err := s.writeLocked(goals); err != nil {
		return nil, err
	}
	return goals, nil
}

// ReorderByMapping reorders the queue using a mapping of positions to IDs.
// The mapping is 1-indexed positions ("1B,2C,3A" means position 1 gets item B).
// The items are referenced by their letter index (A=first, B=second, ...).
func (s *GoalQueueStore) ReorderByMapping(mapping string) ([]goal.UpcomingGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	if len(goals) == 0 {
		return nil, nil
	}

	pairs, err := parseReorderMapping(mapping, len(goals))
	if err != nil {
		return nil, err
	}

	result, err := applyReorderPairs(goals, pairs)
	if err != nil {
		return nil, err
	}

	if err := s.writeLocked(result); err != nil {
		return nil, err
	}
	return result, nil
}

func applyReorderPairs(goals []goal.UpcomingGoal, pairs []reorderPair) ([]goal.UpcomingGoal, error) {
	result := make([]goal.UpcomingGoal, len(goals))
	used := make(map[int]bool)
	for _, p := range pairs {
		if p.pos < 1 || p.pos > len(goals) {
			return nil, fmt.Errorf("invalid position %d", p.pos)
		}
		if p.idx < 0 || p.idx >= len(goals) {
			return nil, fmt.Errorf("invalid item index %d", p.idx)
		}
		if used[p.idx] {
			return nil, fmt.Errorf("item used more than once")
		}
		used[p.idx] = true
		result[p.pos-1] = goals[p.idx]
	}
	return fillRemainingPositions(result, goals, used)
}

func fillRemainingPositions(result, goals []goal.UpcomingGoal, used map[int]bool) ([]goal.UpcomingGoal, error) {
	var remaining []goal.UpcomingGoal
	for i := range goals {
		if !used[i] {
			remaining = append(remaining, goals[i])
		}
	}
	nextRemaining := 0
	for i := range result {
		if result[i].ID != "" {
			continue
		}
		if nextRemaining >= len(remaining) {
			return nil, fmt.Errorf("incomplete reorder mapping")
		}
		result[i] = remaining[nextRemaining]
		nextRemaining++
	}
	return result, nil
}

type reorderPair struct {
	pos int
	idx int
}

func parseReorderMapping(mapping string, n int) ([]reorderPair, error) {
	mapping = strings.ReplaceAll(mapping, " ", "")
	if mapping == "" {
		return nil, nil
	}
	parts := strings.Split(mapping, ",")
	var pairs []reorderPair
	for _, part := range parts {
		if len(part) < 2 {
			return nil, fmt.Errorf("invalid reorder token %q", part)
		}
		pos := 0
		for i := 0; i < len(part)-1; i++ {
			if part[i] < '0' || part[i] > '9' {
				return nil, fmt.Errorf("invalid reorder token %q", part)
			}
			pos = pos*10 + int(part[i]-'0')
		}
		letter := part[len(part)-1]
		if letter < 'A' || letter > 'Z' {
			return nil, fmt.Errorf("invalid reorder letter %q", letter)
		}
		idx := int(letter - 'A')
		pairs = append(pairs, reorderPair{pos: pos, idx: idx})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].pos < pairs[j].pos
	})
	return pairs, nil
}

func generateQueueID() string {
	return internal.PrefixedHexID("qg", 6)
}
