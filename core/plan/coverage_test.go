// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReplay_ItemUpdatedStatusAndResult(t *testing.T) {
	// Test applyItemFields with status and result fields.
	root := t.TempDir()

	s, err := Create(root, "test item status/result update")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id, err := s.AddItem("Item", "desc", "", nil, "")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// Use the payload directly to test applyItemFields with status/result.
	fields := `{"status":"done","result":"completed successfully"}`
	s.mu.Lock()
	rawFields := []byte(fields)
	payload := marshalPayload(PayloadItemUpdated{ItemID: id, Fields: rawFields})
	s.append(Event{Type: EventItemUpdated, Payload: payload})
	s.mu.Unlock()

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	item := s2.plan.Item(id)
	if item == nil {
		t.Fatal("item not found")
	}
	if item.Status != ItemDone {
		t.Errorf("status = %q, want done", item.Status)
	}
	if item.Result != "completed successfully" {
		t.Errorf("result = %q", item.Result)
	}
}

func TestReplay_SeqGap(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "seq gap test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually corrupt the events file by writing a line with wrong seq.
	eventsPath := filepath.Join(root, s.id, "events.jsonl")
	s.Close()

	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = f.WriteString(`{"seq":42,"type":"plan_approved","plan_id":"test","timestamp":"2026-01-01T00:00:00Z","payload":{}}` + "\n")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	_, err = Open(root, s.id)
	if err == nil {
		t.Fatal("expected error for sequence gap")
	}
}

func TestStartItem_BlockedItemError(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "start blocked test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id, _ := s.AddItem("Test", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	// First block the item directly.
	s.mu.Lock()
	s.plan.Item(id).Status = ItemBlocked
	s.mu.Unlock()

	// Try to start it.
	err = s.StartItem(id, "coder", "agent")
	if err == nil {
		t.Error("expected error for blocked item")
	}
}

func TestStartItem_NotExecuting(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "start not executing")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id, _ := s.AddItem("Test", "", "", nil, "")

	err = s.StartItem(id, "coder", "agent")
	if err == nil {
		t.Error("expected error for plan not in executing status")
	}
}

func TestCompleteItem_NotInProgress(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "complete not in progress")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id, _ := s.AddItem("Test", "", "", nil, "")

	err = s.CompleteItem(id, "done")
	if err == nil {
		t.Error("expected error for item not in_progress")
	}
}

func TestCompleteItem_EmptyResult(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "complete empty result")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id, _ := s.AddItem("Test", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")
	s.StartItem(id, "coder", "agent")

	err = s.CompleteItem(id, "")
	if err == nil {
		t.Error("expected error for empty result")
	}
}

func TestBlockItem_InvalidStatus(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "block invalid status")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id, _ := s.AddItem("Test", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	// Block a pending item — this works.
	err = s.BlockItem(id, "first reason")
	if err != nil {
		t.Fatalf("BlockItem on pending: %v", err)
	}

	// Item is now blocked — cannot block again.
	err = s.BlockItem(id, "second reason")
	if err == nil {
		t.Error("expected error for blocking already-blocked item")
	}
}

func TestFinish_NotAllTerminal(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "finish not terminal")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	s.AddItem("A", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	err = s.Finish()
	if err == nil {
		t.Error("expected error when items not all terminal")
	}
}

func TestFinish_WrongStatus(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, "finish wrong status")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	err = s.Finish()
	if err == nil {
		t.Error("expected error when plan not in executing status")
	}
}

func marshalPayload(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
