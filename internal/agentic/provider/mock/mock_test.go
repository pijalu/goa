// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mock

import (
	"errors"
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

var errTest400 = errors.New("provider 400: max_output_tokens exceeded")

// TestProvider_FailNextScriptsProviderErrors verifies FailNext: the next
// Stream call for the model returns the scripted error (a provider-400 class
// failure), consumed FIFO; once the error queue is dry the model streams its
// scripted turns normally again.
func TestProvider_FailNextScriptsProviderErrors(t *testing.T) {
	p := New(t)
	mdl := p.Model("m1")
	p.ReplyText("m1", "ok-reply")
	p.FailNext("m1", errTest400)

	if _, err := p.Stream(mdl, provider.Context{}, provider.StreamOptions{}); err == nil {
		t.Fatal("Stream should return the scripted error")
	} else if err != errTest400 {
		t.Fatalf("Stream error = %v, want the scripted %v", err, errTest400)
	}
	// Error consumed: the next stream serves the scripted turn.
	s, err := p.Stream(mdl, provider.Context{}, provider.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream after consumed error: %v", err)
	}
	if got := collectStream(t, s); got != "ok-reply" {
		t.Errorf("post-error reply = %q, want ok-reply", got)
	}
	if p.Calls("m1") != 2 {
		t.Errorf("Calls = %d, want 2 (the failed call still counts)", p.Calls("m1"))
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
