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

// TestGoalTools_AllErrorsAreToolErrors asserts (AP-04) that every error path
// from the goal tools' Execute/ExecuteWithResult returns a *internal.ToolError.
func TestGoalTools_AllErrorsAreToolErrors(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)

	cases := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{"create invalid json", func(t *testing.T) error {
			_, err := (&CreateGoalTool{Mode: mode}).Execute(`not-json`)
			return err
		}},
		{"create empty objective", func(t *testing.T) error {
			_, err := (&CreateGoalTool{Mode: mode}).Execute(`{"objective":""}`)
			return err
		}},
		{"update invalid json", func(t *testing.T) error {
			_, err := (&UpdateGoalTool{Mode: mode}).ExecuteWithResult(`not-json`)
			return err
		}},
		{"update invalid status", func(t *testing.T) error {
			_, err := (&UpdateGoalTool{Mode: mode}).ExecuteWithResult(`{"status":"bogus"}`)
			return err
		}},
		{"update no goal", func(t *testing.T) error {
			_, err := (&UpdateGoalTool{Mode: mode}).ExecuteWithResult(`{"status":"paused"}`)
			return err
		}},
		{"set_budget invalid json", func(t *testing.T) error {
			_, err := (&SetGoalBudgetTool{Mode: mode}).Execute(`not-json`)
			return err
		}},
		{"set_budget no goal", func(t *testing.T) error {
			_, err := (&SetGoalBudgetTool{Mode: mode}).Execute(`{"value":5,"unit":"turns"}`)
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
