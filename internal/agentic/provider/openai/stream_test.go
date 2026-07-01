// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestStreamOpenAICompletions_IdleTimeout(t *testing.T) {
	serverCtx, releaseServer := context.WithCancel(context.Background())
	defer releaseServer()

	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		// Hold the connection open until the test has verified the idle
		// timeout fired, then clean up without relying on time.Sleep.
		select {
		case <-serverCtx.Done():
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	model := provider.Model{
		ID:      "test-model",
		Api:     provider.ApiOpenAICompletions,
		BaseURL: server.URL + "/v1/chat/completions",
	}

	stream, err := streamOpenAICompletions(model, provider.Context{}, provider.StreamOptions{IdleTimeout: 100 * time.Millisecond}, provider.OpenAICompletionsCompat{})
	if err != nil {
		t.Fatalf("streamOpenAICompletions failed: %v", err)
	}

	done := make(chan struct{})
	var streamErr error
	go func() {
		defer close(done)
		for event := range stream.Seq() {
			if event.Type == provider.EventError {
				streamErr = event.Error
			}
		}
		if streamErr == nil {
			streamErr = stream.Err()
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not terminate within idle timeout")
	}

	if streamErr == nil {
		t.Fatal("expected a stream error, got nil")
	}
	if !errors.Is(streamErr, provider.ErrStreamIdle) {
		t.Fatalf("expected ErrStreamIdle, got %v", streamErr)
	}
}

func TestStreamOpenAICompletions_SendsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	model := provider.Model{
		ID:      "test-model",
		Api:     provider.ApiOpenAICompletions,
		BaseURL: server.URL + "/v1/chat/completions",
	}

	stream, err := streamOpenAICompletions(model, provider.Context{}, provider.StreamOptions{
		APIKey:      "sk-test-key-42",
		IdleTimeout: 100 * time.Millisecond,
	}, provider.OpenAICompletionsCompat{})
	if err != nil {
		t.Fatalf("streamOpenAICompletions failed: %v", err)
	}

	// Drain the stream so the goroutine completes and the request is sent.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream.Seq() {
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not terminate")
	}

	want := "Bearer sk-test-key-42"
	if gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}

func TestStreamOpenAICompletions_ActiveStreamDoesNotTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"x"}}]}`)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	model := provider.Model{
		ID:      "test-model",
		Api:     provider.ApiOpenAICompletions,
		BaseURL: server.URL + "/v1/chat/completions",
	}

	stream, err := streamOpenAICompletions(model, provider.Context{}, provider.StreamOptions{IdleTimeout: 100 * time.Millisecond}, provider.OpenAICompletionsCompat{})
	if err != nil {
		t.Fatalf("streamOpenAICompletions failed: %v", err)
	}

	deltas := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range stream.Seq() {
			if event.Type == provider.EventTextDelta {
				deltas++
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("active stream terminated unexpectedly or timed out")
	}

	if deltas != 3 {
		t.Fatalf("expected 3 text deltas, got %d", deltas)
	}
	if streamErr := stream.Err(); streamErr != nil {
		t.Fatalf("unexpected stream error: %v", streamErr)
	}
}
