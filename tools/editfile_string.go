// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"

	"github.com/pijalu/goa/internal"
)

func isStringEdit(p editFileParams) bool {
	switch EditOperation(p.Operation) {
	case OpReplace, OpDelete:
		return true
	}
	return p.Operation == "" && p.OldString != ""
}

func validateStringEdit(p editFileParams) error {
	if p.Occurrence < 0 || (p.Occurrence > 0 && p.ReplaceAll) {
		return &internal.ToolError{Tool: "edit", Type: "invalid_input",
			Detail:   "occurrence must be positive and cannot be combined with replace_all",
			HintText: "Use occurrence:N for one match, replace_all:true for every match, or neither to require a unique match."}
	}
	if EditOperation(p.Operation) == OpDelete {
		if p.OldString == "" {
			return &internal.ToolError{Tool: "edit", Type: "missing_parameter",
				Detail: "operation 'delete' requires a non-empty old_string", HintText: "Identify the text to remove in old_string; do not use empty new_string for deletion."}
		}
		if p.NewString != "" || p.NewContent != "" || p.NewContentSet {
			return &internal.ToolError{Tool: "edit", Type: "invalid_input",
				Detail: "operation 'delete' does not accept replacement content", HintText: "Remove new_string/new_content; old_string alone selects the content to delete."}
		}
		return nil
	}
	return validateReplacePair(p.OldString, p.NewString)
}

func validateSingleFileAtomic(p editFileParams) error {
	if len(p.Files) > 0 || p.Glob != "" {
		return nil
	}
	invalid := p.Atomic != nil
	for _, edit := range p.Edits {
		invalid = invalid || edit.Atomic != nil
	}
	if !invalid {
		return nil
	}
	return &internal.ToolError{Tool: "edit", Type: "invalid_input",
		Detail:   "atomic is only valid with the multi-file files or glob forms",
		HintText: "Single-file edits and single-file batches are already atomic; omit atomic, including from edits[]."}
}

func validateNoReplaceAllForLineOp(p editFileParams) error {
	if !p.ReplaceAll || isStringEdit(p) {
		return nil
	}
	return &internal.ToolError{Tool: "edit", Type: "invalid_input",
		Detail:   fmt.Sprintf("replace_all is not supported for operation %q", p.Operation),
		HintText: "Use replace_all with old_string-based replace or delete; replace_pattern keeps its occurrence-based line semantics."}
}
