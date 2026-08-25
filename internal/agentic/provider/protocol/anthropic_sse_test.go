// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseAnthropicEventStreamMultiDataLineJoin verifies the WHATWG joining
// rule in the protocol-level Anthropic parser: consecutive "data:" lines of
// one event are joined with '\n' before the handler sees them (regression for
// payloads silently merging without a separator).
func TestParseAnthropicEventStreamMultiDataLineJoin(t *testing.T) {
	input := "event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\n" +
		"data: \"delta\":{\"type\":\"text_delta\",\"text\":\"hey\"}}\n\n"

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
	want := []call{{
		"content_block_delta",
		"{\"type\":\"content_block_delta\",\"index\":0,\n\"delta\":{\"type\":\"text_delta\",\"text\":\"hey\"}}",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v, want %v", got, want)
	}
}

// TestParseAnthropicEventStreamBlankLineDispatchUnchanged guards dispatch
// semantics: events separated by blank lines stay separate handler calls and
// single-line payloads pass through byte-identical.
func TestParseAnthropicEventStreamBlankLineDispatchUnchanged(t *testing.T) {
	input := "event: message_start\n" +
		"data: {\"type\":\"message_start\"}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"single\":true}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

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
	wantEvents := []string{"message_start", "content_block_delta", "message_stop"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events: got %v, want %v", events, wantEvents)
	}
	wantDatas := []string{
		"{\"type\":\"message_start\"}",
		"{\"single\":true}",
		"{\"type\":\"message_stop\"}",
	}
	if !reflect.DeepEqual(datas, wantDatas) {
		t.Fatalf("datas: got %v, want %v", datas, wantDatas)
	}
}
