// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package catalog

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPaginateMultiPage(t *testing.T) {
	pages := map[string]Page[string]{
		"":   {Items: []string{"a", "b"}, NextCursor: "c1"},
		"c1": {Items: []string{"c"}, NextCursor: "c2"},
		"c2": {Items: []string{"d", "e"}, NextCursor: ""},
	}
	got, err := Paginate(func(cur string) (Page[string], error) { return pages[cur], nil })
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	want := []string{"a", "b", "c", "d", "e"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestPaginateDuplicateCursor(t *testing.T) {
	pages := map[string]Page[string]{
		"":   {Items: []string{"a"}, NextCursor: "x"},
		"x":  {Items: []string{"b"}, NextCursor: "x"}, // duplicate -> loop
	}
	if _, err := Paginate(func(cur string) (Page[string], error) { return pages[cur], nil }); err == nil {
		t.Fatal("expected duplicate-cursor error")
	}
}

func TestPaginateErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	_, err := Paginate(func(cur string) (Page[string], error) { return Page[string]{}, boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"filesystem":     "filesystem",
		"my-server":      "my-server",
		"my_server":      "my_server",
		"a.b/c d":        "a_b_c_d",
		"server@2.0":     "server_2_0",
		"UPPER lower 123": "UPPER_lower_123",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolNameAndPrefix(t *testing.T) {
	if got := ToolName("my-server", "read.file"); got != "mcp__my-server__read_file" {
		t.Errorf("ToolName = %q", got)
	}
	if got := ToolPrefix("my-server"); got != "mcp__my-server__" {
		t.Errorf("ToolPrefix = %q", got)
	}
}

func TestTextConcatAndStructured(t *testing.T) {
	res := &Result{Content: []Content{{Type: "text", Text: "hello "}, {Type: "image"}, {Type: "text", Text: "world"}}}
	if got := Text(res); got != "hello world" {
		t.Errorf("Text concat = %q", got)
	}
	// structured content fallback when no text
	res2 := &Result{StructuredContent: json.RawMessage(`{"a":1}`)}
	if got := Text(res2); got != `{"a":1}` {
		t.Errorf("Text structured = %q", got)
	}
	// empty
	if got := Text(&Result{}); got != "" {
		t.Errorf("Text empty = %q", got)
	}
}

func TestErr(t *testing.T) {
	if Err(&Result{IsError: false}) != nil {
		t.Error("expected nil error for non-error result")
	}
	e := Err(&Result{IsError: true, Content: []Content{{Type: "text", Text: "bad"}}})
	if e == nil || e.Error() != "bad" {
		t.Errorf("Err = %v", e)
	}
	e2 := Err(&Result{IsError: true})
	if e2 == nil || e2.Error() == "" {
		t.Error("expected fallback error message")
	}
}
