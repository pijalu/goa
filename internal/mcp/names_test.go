// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"filesystem": "filesystem",
		"my-server":  "my-server",
		"my_server":  "my_server",
		"a.b/c d":    "a_b_c_d",
		"server@2.0": "server_2_0",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolNameSanitizes(t *testing.T) {
	if got := toolName("my.server", "read/file"); got != "mcp__my_server__read_file" {
		t.Errorf("toolName = %q", got)
	}
	if got := toolPrefix("my.server"); got != "mcp__my_server__" {
		t.Errorf("toolPrefix = %q", got)
	}
}

func TestNormalizeSchema(t *testing.T) {
	// nil/empty input -> object with empty properties + no additional props.
	got := normalizeSchema(nil)
	if got["type"] != "object" {
		t.Errorf("type = %v", got["type"])
	}
	if _, ok := got["properties"].(map[string]any); !ok {
		t.Errorf("properties = %v", got["properties"])
	}
	if got["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v", got["additionalProperties"])
	}

	// existing properties preserved; input not mutated.
	in := map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}}
	out := normalizeSchema(in)
	if _, ok := out["properties"].(map[string]any)["x"]; !ok {
		t.Errorf("properties lost: %v", out["properties"])
	}
	if _, mutated := in["additionalProperties"]; mutated {
		t.Error("input schema was mutated")
	}
}
