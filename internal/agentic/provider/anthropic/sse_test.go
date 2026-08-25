// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package anthropic

import (
	"reflect"
	"strings"
	"testing"
)

// TestAnthropicSSEMultiDataLineJoin verifies the WHATWG joining rule:
// consecutive "data:" lines of one event are joined with '\n' before the
// handler sees them (regression for payloads silently merging without '\n').
func TestAnthropicSSEMultiDataLineJoin(t *testing.T) {
	input := "event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\n" +
		"data: \"delta\":{\"text\":\"hi\"}}\n\n"

	type call struct {
		event string
		data  string
	}
	var got []call
	err := parseAnthropicEventStream(strings.NewReader(input), func(eventType, data string) error {
		got = append(got, call{eventType, data})
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []call{{"content_block_delta", "{\"type\":\"content_block_delta\",\n\"delta\":{\"text\":\"hi\"}}"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v, want %v", got, want)
	}
}

// TestAnthropicSSEBlankLineDispatchUnchanged guards dispatch semantics:
// events separated by blank lines stay separate handler calls, single-line
// payloads pass through unchanged.
func TestAnthropicSSEBlankLineDispatchUnchanged(t *testing.T) {
	input := "event: message_start\n" +
		"data: {\"a\":1}\n\n" +
		"event: message_stop\n" +
		"data: {\"b\":2}\n\n"

	var events []string
	var datas []string
	err := parseAnthropicEventStream(strings.NewReader(input), func(eventType, data string) error {
		events = append(events, eventType)
		datas = append(datas, data)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantEvents := []string{"message_start", "message_stop"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events: got %v, want %v", events, wantEvents)
	}
	wantDatas := []string{"{\"a\":1}", "{\"b\":2}"}
	if !reflect.DeepEqual(datas, wantDatas) {
		t.Fatalf("datas: got %v, want %v", datas, wantDatas)
	}
}

// TestAnthropicSSEStateAppendData unit-covers the join helper: first line has
// no separator, subsequent lines are '\n'-joined, flush resets state.
func TestAnthropicSSEStateAppendData(t *testing.T) {
	var st anthropicSSEState
	st.event = "delta"
	st.appendData("line1")
	st.appendData("line2")
	st.appendData("line3")

	var got []string
	if err := st.flush(func(_, data string) error {
		got = append(got, data)
		return nil
	}); err != nil {
		t.Fatalf("flush error: %v", err)
	}
	if want := []string{"line1\nline2\nline3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("joined data: got %q, want %q", got, want)
	}

	// After flush the state is reset: a new event starts clean.
	st.event = "next"
	st.appendData("fresh")
	got = nil
	if err := st.flush(func(eventType, data string) error {
		got = append(got, eventType+"|"+data)
		return nil
	}); err != nil {
		t.Fatalf("second flush error: %v", err)
	}
	if want := []string{"next|fresh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-flush data: got %q, want %q", got, want)
	}
}
