// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mock

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// collectStream drains a mock stream and returns the final message.
func collectStream(t *testing.T, s *provider.AssistantMessageEventStream) string {
	t.Helper()
	for range s.Seq() {
	}
	res := s.Result()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty final message")
	}
	return res.Content[0].Text
}

func TestProvider_ScriptedFIFOAndReplay(t *testing.T) {
	p := New(t)
	mdl := p.Model("m1")
	p.ReplyText("m1", "first")
	p.ReplyText("m1", "second")

	s1, err := p.Stream(mdl, provider.Context{}, provider.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}
	if got := collectStream(t, s1); got != "first" {
		t.Errorf("turn 1 = %q, want first", got)
	}
	s2, _ := p.Stream(mdl, provider.Context{}, provider.StreamOptions{})
	if got := collectStream(t, s2); got != "second" {
		t.Errorf("turn 2 = %q, want second", got)
	}
	// Queue exhausted: last scripted turn replays (tool-looping agents must
	// never deadlock).
	s3, _ := p.Stream(mdl, provider.Context{}, provider.StreamOptions{})
	if got := collectStream(t, s3); got != "second" {
		t.Errorf("turn 3 (replay) = %q, want second", got)
	}
	if p.Calls("m1") != 3 {
		t.Errorf("Calls = %d, want 3", p.Calls("m1"))
	}
}

func TestProvider_DefaultReplyWhenUnscripted(t *testing.T) {
	p := New(t)
	s, err := p.Stream(p.Model("m-x"), provider.Context{}, provider.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := collectStream(t, s); got != "mock reply from m-x" {
		t.Errorf("default reply = %q", got)
	}
}

func TestProvider_GateBlocksUntilClosed(t *testing.T) {
	p := New(t)
	mdl := p.Model("gated")
	p.ReplyText("gated", "released")
	gate := make(chan struct{})
	p.SetGate("gated", gate)

	type res struct{ text string }
	done := make(chan res, 1)
	go func() {
		s, err := p.Stream(mdl, provider.Context{}, provider.StreamOptions{})
		if err != nil {
			t.Errorf("Stream: %v", err)
			return
		}
		done <- res{collectStream(t, s)}
	}()

	select {
	case <-done:
		t.Fatal("stream completed before the gate opened")
	case <-time.After(50 * time.Millisecond):
	}
	close(gate)
	select {
	case r := <-done:
		if r.text != "released" {
			t.Errorf("text = %q, want released", r.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not complete after gate closed")
	}
}

func TestProvider_ThinkingTurn(t *testing.T) {
	p := New(t)
	mdl := p.Model("thinker")
	p.Script("thinker", ThinkingTextTurn("pondering", "answer"))

	s, _ := p.Stream(mdl, provider.Context{}, provider.StreamOptions{})
	var sawThinking bool
	for ev := range s.Seq() {
		if ev.Type == "thinking_delta" && ev.Delta == "pondering" {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Error("no thinking_delta streamed")
	}
	res := s.Result()
	if len(res.Content) != 2 || res.Content[1].Text != "answer" {
		t.Errorf("final content = %+v", res.Content)
	}
}
