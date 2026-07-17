// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pijalu/goa/internal"
)

// Store manages the event-sourced persistence for a single Plan.
//
// All mutations serialize through a single mutex: mu.Lock → mutate in-memory →
// append → mu.Unlock. The store provides a single-writer invariant: exactly one
// goroutine may hold the mutex at any time.
type Store struct {
	root string   // .goa/plans
	id   string   // plan-<hex>
	mu   sync.Mutex
	plan *Plan    // in-memory state
	seq  int      // next event sequence number

	dir  string   // <root>/<id>/
	f    *os.File // events.jsonl handle (kept open for append)
}

// Create initialises a new plan store, writing the initial plan_created event.
// The plan directory is created under root/<id>.
func Create(root, objective string) (*Store, error) {
	id := internal.PrefixedHexID("plan", 4)
	name := collectUniqueName(root)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create plan dir: %w", err)
	}

	now := time.Now().UTC()
	plan := &Plan{
		ID:        id,
		Name:      name,
		Objective: objective,
		Status:    PlanDraft,
		Revision:  0,
		Items:     nil,
		Comments:  nil,
		CreatedAt: now,
		UpdatedAt: now,
	}

	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open events.jsonl: %w", err)
	}

	s := &Store{
		root: root,
		id:   id,
		plan: plan,
		dir:  dir,
		f:    f,
	}

	// Append the initial plan_created event.
	payload := PayloadPlanCreated{Objective: objective, Name: name}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("marshal plan_created payload: %w", err)
	}

	s.mu.Lock()
	s.append(Event{
		Type:    EventPlanCreated,
		PlanID:  id,
		Payload: rawPayload,
	})
	s.mu.Unlock()

	return s, nil
}

// collectUniqueName returns a unique friendly name by scanning sibling plan dirs.
func collectUniqueName(root string) string {
	taken := make(map[string]bool)
	entries, err := os.ReadDir(root)
	if err != nil {
		return internal.FriendlyNameUnique(taken)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name(), "plan.json"))
		if err != nil {
			continue
		}
		var p Plan
		if json.Unmarshal(data, &p) != nil || p.Name == "" {
			continue
		}
		taken[p.Name] = true
	}
	return internal.FriendlyNameUnique(taken)
}

// Open replays the event log for an existing plan and returns a Store handle.
func Open(root, id string) (*Store, error) {
	dir := filepath.Join(root, id)
	eventsPath := filepath.Join(dir, "events.jsonl")

	f, err := os.OpenFile(eventsPath, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open events.jsonl: %w", err)
	}

	s := &Store{
		root: root,
		id:   id,
		dir:  dir,
		f:    f,
	}

	// Replay events to rebuild state.
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("replay line %d: %w", s.seq+1, err)
		}
		if s.seq != evt.Seq-1 {
			return nil, fmt.Errorf("replay line %d: seq gap (got %d, expected %d)", s.seq+1, evt.Seq, s.seq+1)
		}
		s.seq = evt.Seq
		if err := s.applyEvent(evt); err != nil {
			return nil, fmt.Errorf("replay line %d: %w", s.seq, err)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("scan events.jsonl: %w", err)
	}

	if s.plan == nil {
		return nil, fmt.Errorf("no plan_created event found in %s", eventsPath)
	}

	return s, nil
}

// Resolve resolves a plan reference to an ID. It tries friendly name first,
// then internal ID, else returns an error.
func Resolve(root, ref string) (string, error) {
	// Try friendly name: check all plan.json files for matching name.
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("plan %q not found", ref)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name(), "plan.json"))
		if err != nil {
			continue
		}
		var p Plan
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		if p.Name == ref {
			return p.ID, nil
		}
	}

	// Try as internal ID directly.
	if _, err := os.Stat(filepath.Join(root, ref)); err == nil {
		return ref, nil
	}

	return "", fmt.Errorf("plan %q not found", ref)
}

// append writes a single event to the event log and updates the snapshot.
// The caller MUST hold s.mu.
func (s *Store) append(evt Event) {
	s.seq++
	evt.Seq = s.seq
	if evt.PlanID == "" {
		evt.PlanID = s.id
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}

	data, err := json.Marshal(evt)
	if err != nil {
		// Marshal failure of a well-typed event is a programming error.
		panic(fmt.Sprintf("plan: marshal event: %v", err))
	}

	data = append(data, '\n')
	if _, err := s.f.Write(data); err != nil {
		panic(fmt.Sprintf("plan: write event: %v", err))
	}

	if err := s.writeSnapshot(); err != nil {
		panic(fmt.Sprintf("plan: write snapshot: %v", err))
	}
}

// writeSnapshot writes the in-memory plan as plan.json.
// The caller MUST hold s.mu.
func (s *Store) writeSnapshot() error {
	data, err := json.MarshalIndent(s.plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dir, "plan.json"), data, 0644)
}

// applyEvent mutates the in-memory plan according to the event type.
// Unknown event types are silently skipped for forward compatibility.
func (s *Store) applyEvent(evt Event) error {
	handler := eventHandlers[evt.Type]
	if handler == nil {
		return nil // forward compat
	}
	return handler(s, evt)
}

// eventHandlers maps event types to their state-mutation handlers.
var eventHandlers = map[EventType]func(s *Store, evt Event) error{
	EventPlanCreated:       (*Store).applyPlanCreated,
	EventItemAdded:         (*Store).applyItemAdded,
	EventItemRemoved:       (*Store).applyItemRemoved,
	EventPlanApproved:      (*Store).applyPlanApproved,
	EventExecutionStarted:  (*Store).applyExecutionStarted,
	EventPlanCompleted:     (*Store).applyPlanCompleted,
	EventPlanFailed:        (*Store).applyPlanFailed,
	EventItemBlocked:       (*Store).applyItemBlocked,
	EventItemSkipped:       (*Store).applyItemSkipped,
	EventItemStarted:       (*Store).applyItemStarted,
	EventItemCompleted:     (*Store).applyItemCompleted,
	EventRevisionSubmitted: (*Store).applyRevisionSubmitted,
	EventCommentAdded:      (*Store).applyCommentAdded,
	EventCommentRemoved:    (*Store).applyCommentRemoved,
	EventCommentUpdated:    (*Store).applyCommentUpdated,
	EventCommentResolved:   (*Store).applyCommentResolved,
	EventItemsReordered:    (*Store).applyItemsReordered,
	EventItemUpdated:       (*Store).applyItemUpdated,
	EventClarification:     (*Store).applyClarification,
}

func (s *Store) applyPlanCreated(evt Event) error {
	var p PayloadPlanCreated
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("plan_created payload: %w", err)
	}
	s.plan = &Plan{
		ID:        evt.PlanID,
		Name:      p.Name,
		Objective: p.Objective,
		Status:    PlanDraft,
		CreatedAt: evt.Timestamp,
		UpdatedAt: evt.Timestamp,
	}
	return nil
}

func (s *Store) applyItemAdded(evt Event) error {
	var p PayloadItemAdded
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("item_added payload: %w", err)
	}
	if s.plan != nil {
		s.plan.Items = append(s.plan.Items, p.Item)
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyItemRemoved(evt Event) error {
	var p PayloadItemRemoved
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("item_removed payload: %w", err)
	}
	if s.plan != nil {
		for i, item := range s.plan.Items {
			if item.ID == p.ItemID {
				s.plan.Items = append(s.plan.Items[:i], s.plan.Items[i+1:]...)
				break
			}
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyPlanApproved(evt Event) error {
	if s.plan != nil {
		s.plan.Status = PlanApproved
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyExecutionStarted(evt Event) error {
	var p PayloadExecutionStarted
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("execution_started payload: %w", err)
	}
	if s.plan != nil {
		s.plan.Status = PlanExecuting
		s.plan.RunID = p.RunID
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyPlanCompleted(evt Event) error {
	if s.plan != nil {
		s.plan.Status = PlanDone
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyPlanFailed(evt Event) error {
	var p PayloadPlanFailed
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("plan_failed payload: %w", err)
	}
	if s.plan != nil {
		s.plan.Status = PlanFailed
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyItemBlocked(evt Event) error {
	var p PayloadItemBlocked
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("item_blocked payload: %w", err)
	}
	if s.plan != nil {
		if item := s.plan.Item(p.ItemID); item != nil {
			item.Status = ItemBlocked
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyItemSkipped(evt Event) error {
	var p PayloadItemSkipped
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("item_skipped payload: %w", err)
	}
	if s.plan != nil {
		if item := s.plan.Item(p.ItemID); item != nil {
			item.Status = ItemSkipped
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyItemStarted(evt Event) error {
	var p PayloadItemStarted
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("item_started payload: %w", err)
	}
	if s.plan != nil {
		if item := s.plan.Item(p.ItemID); item != nil {
			item.Status = ItemInProgress
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyItemCompleted(evt Event) error {
	var p PayloadItemCompleted
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("item_completed payload: %w", err)
	}
	if s.plan != nil {
		if item := s.plan.Item(p.ItemID); item != nil {
			item.Status = ItemDone
			item.Result = p.Result
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyRevisionSubmitted(evt Event) error {
	var p PayloadRevisionSubmitted
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("revision_submitted payload: %w", err)
	}
	if s.plan != nil {
		s.plan.Revision = p.Revision
		s.plan.Status = PlanInReview
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyCommentAdded(evt Event) error {
	var p PayloadCommentAdded
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("comment_added payload: %w", err)
	}
	if s.plan != nil {
		s.plan.Comments = append(s.plan.Comments, p.Comment)
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyCommentRemoved(evt Event) error {
	var p PayloadCommentRemoved
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("comment_removed payload: %w", err)
	}
	if s.plan != nil {
		for i, c := range s.plan.Comments {
			if c.ID == p.CommentID {
				s.plan.Comments = append(s.plan.Comments[:i], s.plan.Comments[i+1:]...)
				break
			}
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyCommentUpdated(evt Event) error {
	var p PayloadCommentUpdated
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("comment_updated payload: %w", err)
	}
	if s.plan != nil {
		for i, c := range s.plan.Comments {
			if c.ID == p.CommentID {
				s.plan.Comments[i].Content = p.Content
				s.plan.Comments[i].UpdatedAt = evt.Timestamp
				break
			}
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyCommentResolved(evt Event) error {
	var p PayloadCommentResolved
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("comment_resolved payload: %w", err)
	}
	if s.plan != nil {
		for i, c := range s.plan.Comments {
			if c.ID == p.CommentID {
				s.plan.Comments[i].Resolved = true
				s.plan.Comments[i].UpdatedAt = evt.Timestamp
				break
			}
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyItemsReordered(evt Event) error {
	var p PayloadItemsReordered
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("items_reordered payload: %w", err)
	}
	if s.plan != nil {
		itemMap := make(map[string]PlanItem, len(s.plan.Items))
		for _, item := range s.plan.Items {
			itemMap[item.ID] = item
		}
		ordered := make([]PlanItem, 0, len(p.IDs))
		for _, id := range p.IDs {
			if item, ok := itemMap[id]; ok {
				ordered = append(ordered, item)
			}
		}
		s.plan.Items = ordered
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyItemUpdated(evt Event) error {
	var p PayloadItemUpdated
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("item_updated payload: %w", err)
	}
	if s.plan != nil {
		if item := s.plan.Item(p.ItemID); item != nil {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(p.Fields, &fields); err != nil {
				return fmt.Errorf("item_updated fields: %w", err)
			}
			if err := applyItemFields(item, fields); err != nil {
				return fmt.Errorf("item_updated apply: %w", err)
			}
		}
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

func (s *Store) applyClarification(evt Event) error {
	// Clarifications are recorded but don't change item status.
	if s.plan != nil {
		s.plan.UpdatedAt = evt.Timestamp
	}
	return nil
}

// applyItemFields applies partial field updates to a PlanItem from a raw JSON map.
func applyItemFields(item *PlanItem, fields map[string]json.RawMessage) error {
	// Mapping of JSON field names to setter functions.
	setters := map[string]func(json.RawMessage) error{
		"title": func(v json.RawMessage) error { return json.Unmarshal(v, &item.Title) },
		"description": func(v json.RawMessage) error { return json.Unmarshal(v, &item.Description) },
		"depends_on": func(v json.RawMessage) error { return json.Unmarshal(v, &item.DependsOn) },
		"role": func(v json.RawMessage) error { return json.Unmarshal(v, &item.Role) },
		"status": func(v json.RawMessage) error { return json.Unmarshal(v, &item.Status) },
		"result": func(v json.RawMessage) error { return json.Unmarshal(v, &item.Result) },
	}
	for key, val := range fields {
		setter, ok := setters[key]
		if !ok {
			continue
		}
		if err := setter(val); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

// ID returns the store's plan ID.
func (s *Store) ID() string {
	return s.id
}

// Plan returns a snapshot of the in-memory plan state.
// The caller receives a shallow copy; deep-copy references (Items, Comments)
// are safe for reads only.
func (s *Store) Plan() *Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plan
}

// Snapshot returns a deep copy of the plan suitable for rendering by the TUI
// without holding the store mutex.
func (s *Store) Snapshot() (*Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(s.plan)
	if err != nil {
		return nil, fmt.Errorf("snapshot marshal: %w", err)
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("snapshot unmarshal: %w", err)
	}
	return &p, nil
}

// Close closes the underlying events file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		return s.f.Close()
	}
	return nil
}
