// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"testing"
)

func TestPlan_Item(t *testing.T) {
	tests := []struct {
		name string
		plan *Plan
		id   string
		want string // expected item ID; empty means nil expected
	}{
		{
			name: "hit",
			plan: &Plan{Items: []PlanItem{
				{ID: "item-1", Title: "First"},
				{ID: "item-2", Title: "Second"},
			}},
			id:   "item-1",
			want: "item-1",
		},
		{
			name: "miss",
			plan: &Plan{Items: []PlanItem{
				{ID: "item-1"},
			}},
			id:   "item-42",
			want: "",
		},
		{
			name: "empty",
			plan: &Plan{},
			id:   "item-1",
			want: "",
		},
		{
			name: "last_item",
			plan: &Plan{Items: []PlanItem{
				{ID: "item-1"},
				{ID: "item-2"},
				{ID: "item-3"},
			}},
			id:   "item-3",
			want: "item-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.plan.Item(tt.id)
			if tt.want == "" {
				if got != nil {
					t.Errorf("Item(%q) = %v, want nil", tt.id, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Item(%q) = nil, want non-nil", tt.id)
			}
			if got.ID != tt.want {
				t.Errorf("Item(%q).ID = %q, want %q", tt.id, got.ID, tt.want)
			}
		})
	}
}

func TestPlan_Dependents(t *testing.T) {
	tests := []struct {
		name string
		plan *Plan
		id   string
		want []string
	}{
		{
			name: "chain",
			plan: &Plan{Items: []PlanItem{
				{ID: "item-1"},
				{ID: "item-2", DependsOn: []string{"item-1"}},
				{ID: "item-3", DependsOn: []string{"item-2"}},
			}},
			id:   "item-1",
			want: []string{"item-2"},
		},
		{
			name: "diamond",
			plan: &Plan{Items: []PlanItem{
				{ID: "root"},
				{ID: "left", DependsOn: []string{"root"}},
				{ID: "right", DependsOn: []string{"root"}},
				{ID: "leaf", DependsOn: []string{"left", "right"}},
			}},
			id:   "root",
			want: []string{"left", "right"},
		},
		{
			name: "no_dependents",
			plan: &Plan{Items: []PlanItem{
				{ID: "item-1"},
				{ID: "item-2"},
			}},
			id:   "item-1",
			want: []string{},
		},
		{
			name: "unknown_id",
			plan: &Plan{Items: []PlanItem{
				{ID: "item-1"},
			}},
			id:   "item-99",
			want: []string{},
		},
		{
			name: "multiple_dependents_on_same",
			plan: &Plan{Items: []PlanItem{
				{ID: "shared"},
				{ID: "a", DependsOn: []string{"shared"}},
				{ID: "b", DependsOn: []string{"shared"}},
				{ID: "c", DependsOn: []string{"shared"}},
			}},
			id:   "shared",
			want: []string{"a", "b", "c"},
		},
		{
			name: "empty_plan",
			plan: &Plan{},
			id:   "item-1",
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.plan.Dependents(tt.id)
			if !stringSliceEqual(got, tt.want) {
				t.Errorf("Dependents(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

func TestPlan_AllTerminal(t *testing.T) {
	tests := []struct {
		name string
		plan *Plan
		want bool
	}{
		{
			name: "all_done",
			plan: &Plan{Items: []PlanItem{
				{Status: ItemDone},
				{Status: ItemDone},
			}},
			want: true,
		},
		{
			name: "all_skipped",
			plan: &Plan{Items: []PlanItem{
				{Status: ItemSkipped},
				{Status: ItemSkipped},
			}},
			want: true,
		},
		{
			name: "mixed_terminal",
			plan: &Plan{Items: []PlanItem{
				{Status: ItemDone},
				{Status: ItemSkipped},
			}},
			want: true,
		},
		{
			name: "one_pending",
			plan: &Plan{Items: []PlanItem{
				{Status: ItemDone},
				{Status: ItemPending},
			}},
			want: false,
		},
		{
			name: "one_in_progress",
			plan: &Plan{Items: []PlanItem{
				{Status: ItemDone},
				{Status: ItemInProgress},
			}},
			want: false,
		},
		{
			name: "one_blocked",
			plan: &Plan{Items: []PlanItem{
				{Status: ItemDone},
				{Status: ItemBlocked},
			}},
			want: false,
		},
		{
			name: "empty_plan",
			plan: &Plan{},
			want: true,
		},
		{
			name: "single_done",
			plan: &Plan{Items: []PlanItem{
				{Status: ItemDone},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.plan.AllTerminal()
			if got != tt.want {
				t.Errorf("AllTerminal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlan_ItemMutate(t *testing.T) {
	// Verify that Item returns a pointer that can be used to mutate the item.
	p := &Plan{Items: []PlanItem{
		{ID: "item-1", Status: ItemPending},
	}}
	item := p.Item("item-1")
	if item == nil {
		t.Fatal("Item returned nil")
	}
	item.Status = ItemDone
	if p.Items[0].Status != ItemDone {
		t.Error("mutating returned pointer did not affect plan")
	}
}

func TestPlan_DependentsOrder(t *testing.T) {
	// Verify that Dependents preserves item order.
	p := &Plan{Items: []PlanItem{
		{ID: "a"},
		{ID: "b", DependsOn: []string{"root"}},
		{ID: "c", DependsOn: []string{"root"}},
		{ID: "d"},
		{ID: "e", DependsOn: []string{"root"}},
	}}
	deps := p.Dependents("root")
	want := []string{"b", "c", "e"}
	if !stringSliceEqual(deps, want) {
		t.Errorf("Dependents = %v, want %v", deps, want)
	}
}

// stringSliceEqual reports whether two string slices have the same elements in order.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

