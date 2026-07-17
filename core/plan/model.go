// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import "time"

// PlanStatus represents the lifecycle state of a plan.
type PlanStatus string

const (
	PlanDraft     PlanStatus = "draft"      // planner is building
	PlanInReview  PlanStatus = "in_review"  // submitted; user annotating
	PlanApproved  PlanStatus = "approved"   // user confirmed; not yet started
	PlanExecuting PlanStatus = "executing"  // orchestrator dispatching items
	PlanDone      PlanStatus = "done"       // all items done/skipped
	PlanBlocked   PlanStatus = "blocked"    // unrecoverable item failure
	PlanFailed    PlanStatus = "failed"     // run error / abort
)

// ItemStatus represents the state of a single plan item.
type ItemStatus string

const (
	ItemPending    ItemStatus = "pending"
	ItemInProgress ItemStatus = "in_progress"
	ItemDone       ItemStatus = "done"
	ItemBlocked    ItemStatus = "blocked"
	ItemSkipped    ItemStatus = "skipped"
)

// PlanItem is one unit of work within a Plan.
type PlanItem struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	DependsOn   []string   `json:"depends_on,omitempty"`
	Role        string     `json:"role,omitempty"`
	Status      ItemStatus `json:"status"`
	Result      string     `json:"result,omitempty"`
}

// PlanComment is a user annotation anchored to a plan item or to the plan itself.
type PlanComment struct {
	ID        string    `json:"id"`
	ItemID    string    `json:"item_id"`   // empty = plan-level comment
	Content   string    `json:"content"`
	Revision  int       `json:"revision"`  // revision the comment was made on
	Resolved  bool      `json:"resolved"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Plan is a persisted, event-sourced work plan.
type Plan struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Objective string        `json:"objective"`
	Status    PlanStatus    `json:"status"`
	Revision  int           `json:"revision"`
	Items     []PlanItem    `json:"items"`
	Comments  []PlanComment `json:"comments,omitempty"`
	RunID     string        `json:"run_id,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Item returns a pointer to the item with the given ID, or nil if not found.
func (p *Plan) Item(id string) *PlanItem {
	for i := range p.Items {
		if p.Items[i].ID == id {
			return &p.Items[i]
		}
	}
	return nil
}

// Dependents returns the IDs of items whose DependsOn contains id.
func (p *Plan) Dependents(id string) []string {
	var deps []string
	for _, item := range p.Items {
		for _, dep := range item.DependsOn {
			if dep == id {
				deps = append(deps, item.ID)
				break
			}
		}
	}
	return deps
}

// AllTerminal reports whether every item is done or skipped.
func (p *Plan) AllTerminal() bool {
	for _, item := range p.Items {
		if item.Status != ItemDone && item.Status != ItemSkipped {
			return false
		}
	}
	return true
}
