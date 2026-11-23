// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// editFileParams holds the parsed input for EditFileTool. It describes both a
// single edit (flat fields) and a batch of edits (Edits); a batch element uses
// the same fields, minus Edits (nested batches are not supported). An entry's
// Path is only a fallback for an omitted top-level path (see batchEntryPath)
// and must agree with it (see checkEntryPaths).
type editFileParams struct {
	Path         string           `json:"path"`
	Operation    string           `json:"operation"`
	OldString    string           `json:"old_string"`
	NewString    string           `json:"new_string"`
	StartLine    int              `json:"start_line"`
	EndLine      int              `json:"end_line"`
	Pattern      string           `json:"pattern"`
	PatternFlags string           `json:"pattern_flags"`
	Occurrence   int              `json:"occurrence"`
	ReplaceAll   bool             `json:"replace_all"`
	Atomic       *bool            `json:"atomic"`
	NewContent   string           `json:"new_content"`
	IndentMode   string           `json:"indent_mode"`
	Edits        []editFileParams `json:"edits"`
	// Multi-file batch form (bugs.md B40): either explicit per-file targets
	// or a glob scope sharing the top-level edits. Mutually exclusive with
	// each other, with "path" and with the single-file "edits" batch.
	Files []editFileTarget `json:"files"`
	Glob  string           `json:"glob"`

	// NewContentSet records whether the caller SENT "new_content", as opposed
	// to sending it empty or omitting it. It is not serialized: the wire
	// format stays a plain string. Only UnmarshalJSON sets it, so a value
	// decoded from JSON carries presence information while a struct built in
	// Go keeps the zero value (see resolveOpContent).
	NewContentSet bool `json:"-"`
}

// editFileAlias is editFileParams minus its methods: embedding it in the wire
// type below makes every other field decode exactly as before (including the
// Edits recursion, whose elements are editFileParams) while leaving room for
// a presence-tracking new_content pointer at the shallower depth, which wins
// over the alias's own new_content field.
type editFileAlias editFileParams

// editFileWire decodes editFileParams while tracking field presence. The
// embedded alias contributes every field except new_content (shadowed by the
// pointer, which sits at a shallower depth); the pointer then distinguishes an
// explicit "" from an omitted field. A batch element is an editFileParams, so
// nested edits recurse through UnmarshalJSON automatically.
type editFileWire struct {
	editFileAlias
	NewContent *string         `json:"new_content"`
	Atomic     json.RawMessage `json:"atomic"`
	Edits      json.RawMessage `json:"edits"`
}

// UnmarshalJSON implements json.Unmarshaler for editFileParams: an explicitly
// empty "new_content": "" is a deliberate write of one empty line (Issue 5)
// and must survive decoding, while an omitted field must stay distinguishable
// from it so the d3f8416 deletion guard still rejects lost payloads.
func (p *editFileParams) UnmarshalJSON(data []byte) error {
	var w editFileWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*p = editFileParams(w.editFileAlias)
	if w.NewContent != nil {
		p.NewContent = *w.NewContent
		p.NewContentSet = true
	}
	if w.Edits != nil {
		edits, err := decodeEditFileBatch(w.Edits)
		if err != nil {
			return err
		}
		p.Edits = edits
	}
	if w.Atomic != nil {
		var atomic *bool
		if err := json.Unmarshal(w.Atomic, &atomic); err != nil || atomic == nil {
			return fmt.Errorf("atomic must be true or false, not null or another type")
		}
		p.Atomic = atomic
	}
	return nil
}

// decodeEditFileBatch accepts native arrays and one compatibility form used
// by some OpenAI-compatible models: a string containing one JSON array. Nested
// string encoding remains invalid; no generic repair or recursive decoding is
// performed.
func decodeEditFileBatch(raw json.RawMessage) ([]editFileParams, error) {
	var edits []editFileParams
	arrayErr := json.Unmarshal(raw, &edits)
	if arrayErr == nil {
		return edits, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, arrayErr
	}
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("edits string must contain one JSON array")
	}
	if err := json.Unmarshal([]byte(trimmed), &edits); err != nil {
		return nil, fmt.Errorf("edits string must contain one JSON array: %w", err)
	}
	return edits, nil
}
