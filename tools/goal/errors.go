// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"github.com/pijalu/goa/internal"
)

// goalToolErr wraps an error from a goal tool into the project-standard
// *internal.ToolError so the agent loop and TUI renderers can classify it
// (Type/HintText) instead of receiving a bare fmt.Errorf.
func goalToolErr(tool, typ string, err error) *internal.ToolError {
	return &internal.ToolError{
		Tool:     tool,
		Type:     typ,
		Detail:   err.Error(),
		HintText: "Check the goal tool input and the current goal state (use GetGoal).",
	}
}
