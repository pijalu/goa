// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStoreCreate(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "build auth system")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	// events.jsonl has 1 line (plan_created).
	data, err := os.ReadFile(filepath.Join(s.dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	lineCount := 0
	for _, b := range data {
		if b == '\n' {
			lineCount++
		}
	}
	if lineCount != 1 {
		t.Fatalf("expected 1 line in events.jsonl, got %d", lineCount)
	}

	// plan.json matches in-memory plan.
	var snapshot Plan
	snapData, err := os.ReadFile(filepath.Join(s.dir, "plan.json"))
	if err != nil {
		t.Fatalf("read plan.json: %v", err)
	}
	if err := json.Unmarshal(snapData, &snapshot); err != nil {
		t.Fatalf("unmarshal plan.json: %v", err)
	}
	if snapshot.ID != s.id {
		t.Errorf("plan.json ID = %q, want %q", snapshot.ID, s.id)
	}
	if snapshot.Objective != "build auth system" {
		t.Errorf("plan.json Objective = %q, want %q", snapshot.Objective, "build auth system")
	}
	if snapshot.Status != PlanDraft {
		t.Errorf("plan.json Status = %q, want %q", snapshot.Status, PlanDraft)
	}

	// Verify in-memory plan matches.
	if s.plan.Objective != "build auth system" {
		t.Errorf("in-memory Objective = %q", s.plan.Objective)
	}
	if s.plan.Status != PlanDraft {
		t.Errorf("in-memory Status = %q", s.plan.Status)
	}
}

func TestStoreCreateUniqueName(t *testing.T) {
	root := t.TempDir()

	// Create three plans and ensure all have unique names.
	names := make(map[string]bool)
	for i := 0; i < 3; i++ {
		s, err := Create(root, "objective")
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		if names[s.plan.Name] {
			t.Errorf("duplicate name %q", s.plan.Name)
		}
		names[s.plan.Name] = true
		s.Close()
	}
	if len(names) != 3 {
		t.Errorf("expected 3 unique names, got %d", len(names))
	}
}

func TestStoreAppendConcurrent(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "concurrent test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.mu.Lock()
			// Simulate a mutation: we directly call append with a plan_approved event.
			s.append(Event{
				Type:    EventPlanApproved,
				PlanID:  s.id,
				Payload: []byte("{}"),
			})
			s.mu.Unlock()
		}()
	}
	wg.Wait()

	// Read events.jsonl — each line must be valid JSON.
	data, err := os.ReadFile(filepath.Join(s.dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}

	lines := 0
	for _, line := range splitLines(data) {
		line = trimSpace(line)
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			t.Errorf("invalid JSON at line %d: %v", lines+1, err)
		}
		lines++
	}

	// 1 (created) + 8 (concurrent) = 9 lines.
	if lines != 9 {
		t.Errorf("expected 9 events, got %d", lines)
	}
}

func TestStoreOpenAndReplay(t *testing.T) {
	root := t.TempDir()

	// Create a plan and add some events.
	s1, err := Create(root, "test plan")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate some mutations (we'll add them directly via append for now).
	events := []Event{
		{
			Type:   EventPlanApproved,
			PlanID: s1.id,
			Payload: mustMarshal(t, PayloadPlanApproved{}),
		},
		{
			Type:   EventPlanFailed,
			PlanID: s1.id,
			Payload: mustMarshal(t, PayloadPlanFailed{Reason: "test"}),
		},
	}
	for _, evt := range events {
		s1.mu.Lock()
		s1.append(evt)
		s1.mu.Unlock()
	}
	s1.Close()

	// Open and replay.
	s2, err := Open(root, s1.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	if s2.plan.Status != PlanFailed {
		t.Errorf("after replay Status = %q, want %q", s2.plan.Status, PlanFailed)
	}
	if s2.plan.Objective != "test plan" {
		t.Errorf("after replay Objective = %q, want %q", s2.plan.Objective, "test plan")
	}
}

func TestStoreResolve(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "resolve test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.Close()

	// Resolve by name.
	id, err := Resolve(root, s.plan.Name)
	if err != nil {
		t.Fatalf("Resolve by name: %v", err)
	}
	if id != s.id {
		t.Errorf("Resolve by name: got %q, want %q", id, s.id)
	}

	// Resolve by ID.
	id2, err := Resolve(root, s.id)
	if err != nil {
		t.Fatalf("Resolve by ID: %v", err)
	}
	if id2 != s.id {
		t.Errorf("Resolve by ID: got %q, want %q", id2, s.id)
	}

	// Resolve not found.
	_, err = Resolve(root, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plan")
	}
}

func TestStoreSnapshot(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "snapshot test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if snap.ID != s.id {
		t.Errorf("Snapshot ID = %q, want %q", snap.ID, s.id)
	}
	if snap.Objective != "snapshot test" {
		t.Errorf("Snapshot Objective = %q", snap.Objective)
	}

	// Verify snapshot is independent by modifying it.
	snap.Objective = "modified"
	if s.plan.Objective != "snapshot test" {
		t.Error("modifying snapshot modified in-memory plan")
	}
}

// mustMarshal marshals v to JSON, panicking on error.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// splitLines splits a byte slice into lines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// trimSpace removes leading and trailing whitespace from a byte slice.
func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}
