// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"errors"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
)

// TestGoalTool_AllErrorsAreToolErrors asserts (AP-04) that every error path
// from the goal tool's Execute/ExecuteWithResult returns a *internal.ToolError.
func TestGoalTool_AllErrorsAreToolErrors(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &GoalTool{Mode: mode}

	cases := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{"invalid json", func(t *testing.T) error {
			_, err := tool.Execute(`not-json`)
			return err
		}},
		{"invalid action", func(t *testing.T) error {
			_, err := tool.Execute(`{"action":"bogus"}`)
			return err
		}},
		{"create empty objective", func(t *testing.T) error {
			_, err := tool.Execute(`{"action":"create","objective":""}`)
			return err
		}},
		{"create gated off, no active goal", func(t *testing.T) error {
			gated := &GoalTool{Mode: mode, CreateAllowed: func() bool { return false }}
			_, err := gated.Execute(`{"action":"create","objective":"x"}`)
			return err
		}},
		{"update invalid status", func(t *testing.T) error {
			_, err := tool.Execute(`{"action":"update","status":"bogus"}`)
			return err
		}},
		{"update no goal", func(t *testing.T) error {
			_, err := tool.Execute(`{"action":"update","status":"paused"}`)
			return err
		}},
		{"set_budget missing fields", func(t *testing.T) error {
			_, err := tool.Execute(`{"action":"set_budget"}`)
			return err
		}},
		{"set_budget no goal", func(t *testing.T) error {
			_, err := tool.Execute(`{"action":"set_budget","value":5,"unit":"turns"}`)
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if err == nil {
				t.Fatalf("expected an error")
			}
			var te *internal.ToolError
			if !errors.As(err, &te) {
				t.Errorf("expected *internal.ToolError, got %T: %v", err, err)
			}
		})
	}
}
