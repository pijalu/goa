// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"encoding/json"
	"testing"
)

func TestSafeToolArguments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"valid object passthrough", `{"a":1}`, `{"a":1}`},
		{"valid empty object", `{}`, `{}`},
		{"valid nested passthrough", `{"a":{"b":[1,2,{"c":true}]}}`, `{"a":{"b":[1,2,{"c":true}]}}`},
		{"valid scalar passthrough", `42`, `42`},
		{"valid string passthrough", `"ok"`, `"ok"`},
		{"empty string normalizes", ``, `{}`},
		{"whitespace only normalizes", "  \n\t ", `{}`},

		// Truncated containers: the poolside failure mode — stream ended with
		// finish_reason "tool_calls" before the closing brace.
		{"missing object closer", `{"a":1`, `{"a":1}`},
		{"missing nested closers", `{"a":{"b":[1,2`, `{"a":{"b":[1,2]}}`},
		{"missing array closer", `[1, 2`, `[1, 2]`},
		{"empty object opener only", `{`, `{}`},
		{"empty array opener only", `[`, `[]`},

		// Truncated strings.
		{"truncated value string", `{"a": "v`, `{"a": "v"}`},
		{"truncated value string dangling escape", `{"a": "v\`, `{"a": "v"}`},
		{"truncated key string drops key", `{"ke`, `{}`},
		{"truncated after key drops key", `{"a"`, `{}`},
		{"open string in array", `["v`, `["v"]`},
		{"bare open string", `"v`, `"v"`},

		// Dangling structure: cut back to the last complete value.
		{"truncated after colon", `{"a":`, `{}`},
		{"truncated after comma", `{"a":1,`, `{"a":1}`},
		{"array trailing comma", `[1, 2,`, `[1, 2]`},
		{"truncated second pair keeps first", `{"a":1, "b": "x`, `{"a":1, "b": "x"}`},

		// Primitives.
		{"truncated literal dropped", `{"a": tru`, `{}`},
		{"complete number kept", `{"a": 12`, `{"a": 12}`},
		{"complete literal kept", `{"a": true`, `{"a": true}`},
		{"partial number prefix kept", `{"a": 12e`, `{}`},

		// Corruption after a complete document.
		{"trailing garbage after valid doc", `{"a":1} oops`, `{"a":1}`},
		{"double document keeps first", `{} {}`, `{}`},
		{"extra closer dropped", `{"a":1}}`, `{"a":1}`},
		{"mismatched closer repaired", `{"a":1]`, `{"a":1}`},

		// Unsalvageable: degrade to empty object.
		{"garbage", `not json`, `{}`},
		{"lone closer", `}`, `{}`},
		{"lone colon", `:`, `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeToolArguments(tt.in)
			if got != tt.want {
				t.Errorf("SafeToolArguments(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSafeToolArgumentsPoolsideTruncation reproduces the exact payload from
// the poolside incident: the stream ended right after the closing quote of
// the last string value, missing the final '}'. The repair must append the
// missing closer, preserving the model's intent for conversation history.
func TestSafeToolArgumentsPoolsideTruncation(t *testing.T) {
	args := `{"path": "/Users/muaddib/dev/frigolite/plans/MASTER_PLAN.md", "old_string": "| quote | FAIL |\n| TestP4String | PASS |", "new_string": "| quote | IN PROGRESS |\n| Working tree | dirty — not committed/pushed |"`
	got := SafeToolArguments(args)
	want := args + "}"
	if got != want {
		t.Fatalf("SafeToolArguments() = %q, want %q", got, want)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("repaired arguments do not parse: %v", err)
	}
	if parsed["path"] != "/Users/muaddib/dev/frigolite/plans/MASTER_PLAN.md" {
		t.Errorf("repair lost path value: %v", parsed["path"])
	}
	if parsed["new_string"] == "" {
		t.Error("repair lost new_string value")
	}
}

// TestSafeToolArgumentsAlwaysValidJSON guards the contract every converter
// relies on: the output always marshals, including as json.RawMessage.
func TestSafeToolArgumentsAlwaysValidJSON(t *testing.T) {
	inputs := []string{
		``, ` `, `{`, `[`, `}`, `]`, `:`, `,`, `"`, `\`, `not json`,
		`{"a":`, `{"a": "v`, `{"a": "v\`, `tru`, `{"a": tru`, `12e`,
		`{"a":1}`, `{"a":1}}`, `{"a":1]`, `{} {}`, `{"a":[{"b":`,
	}
	for _, in := range inputs {
		out := SafeToolArguments(in)
		if !json.Valid([]byte(out)) {
			t.Errorf("SafeToolArguments(%q) = %q, not valid JSON", in, out)
			continue
		}
		doc := map[string]any{"input": json.RawMessage(out)}
		if _, err := json.Marshal(doc); err != nil {
			t.Errorf("SafeToolArguments(%q) = %q fails RawMessage marshal: %v", in, out, err)
		}
	}
}
