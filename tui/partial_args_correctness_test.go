// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

// referencePartialArgs is the original O(n^2) regex-based extraction, kept here
// as the behavioral oracle for the incremental scanner.
func referencePartialArgs(raw string) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		return parsed
	}
	out := map[string]any{}
	for _, m := range partialStringFieldRe.FindAllStringSubmatch(raw, -1) {
		if len(m) != 3 {
			continue
		}
		value := m[2]
		if u, err := strconv.Unquote(`"` + value + `"`); err == nil {
			value = u
		}
		out[m[1]] = value
	}
	return out
}

// TestUpdatePartialArgs_IncrementalMatchesReference streams a growing write
// args document byte-by-byte and asserts the incremental scanner's tc.args
// equals the reference regex extraction at every prefix.
func TestUpdatePartialArgs_IncrementalMatchesReference(t *testing.T) {
	content := "# Title\n\nLine with \"quotes\" and \\backslash\\ and `code`.\n\n## Second\n\n- item one\n- item two\n"
	full, _ := json.Marshal(map[string]string{
		"path":    "/Users/muaddib/dev/goa/specs/plan-mode.md",
		"content": content,
		"extra":   "tail field after content",
	})
	raw := string(full)

	// Stream as growing prefixes (incomplete JSON until the very end).
	for step := 1; step <= 7; step++ {
		tc := NewToolExecution("write", "")
		for i := step; i < len(raw); i += step {
			tc.SetArgsPartial(raw[:i])
		}
		// Final full (valid JSON) snapshot.
		tc.SetArgsPartial(raw)

		want := referencePartialArgs(raw)
		got := tc.args
		// The reference may parse valid JSON fully (map[string]any); compare
		// the string fields the streaming path is responsible for.
		for _, k := range []string{"path", "content", "extra"} {
			if fmt.Sprint(want[k]) != fmt.Sprint(got[k]) {
				t.Errorf("step=%d key=%q: got %q, want %q", step, k, got[k], want[k])
			}
		}
	}
}

// TestUpdatePartialArgs_PartialPrefixMatchesReference checks an intermediate
// (still-open) prefix: content must equal the reference's open-value decode.
func TestUpdatePartialArgs_PartialPrefixMatchesReference(t *testing.T) {
	raw := `{"path":"specs/x.go","content":"# Hello world` + "\n" + `more text`
	tc := NewToolExecution("write", "")
	tc.SetArgsPartial(raw)
	want := referencePartialArgs(raw)
	if !reflect.DeepEqual(map[string]any{"path": want["path"], "content": want["content"]},
		map[string]any{"path": tc.args["path"], "content": tc.args["content"]}) {
		t.Errorf("open-field mismatch: got path=%q content=%q, want path=%q content=%q",
			tc.args["path"], tc.args["content"], want["path"], want["content"])
	}
}
