// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

// recordedRequest captures one chat-completions request as seen by the mock
// LLM: the wire messages and the prompt_cache_key the agent stamped on it.
type recordedRequest struct {
	messagesJSON []string // each message re-marshaled for substring checks
	cacheKey     string
}

// text returns every captured message joined, so assertions can substring-match.
func (r recordedRequest) text() string { return strings.Join(r.messagesJSON, "\n") }

// llmRecorder is the mock LLM state: thread-safe request log.
type llmRecorder struct {
	mu   sync.Mutex
	reqs []recordedRequest
	done chan struct{} // closed once 3 requests arrived
}

func (l *llmRecorder) add(r recordedRequest) int {
	l.mu.Lock()
	l.reqs = append(l.reqs, r)
	n := len(l.reqs)
	l.mu.Unlock()
	if n == 3 {
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
	return n
}

func (l *llmRecorder) snapshot() []recordedRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]recordedRequest, len(l.reqs))
	copy(out, l.reqs)
	return out
}

// newMockLLM starts an OpenAI-completions SSE endpoint that records every
// request and answers a single "ok" delta. It doubles as the pacing signal:
// after the third request the test cancels the drive.
func newMockLLM(t *testing.T) (*llmRecorder, string) {
	t.Helper()
	rec := &llmRecorder{done: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages       []map[string]interface{} `json:"messages"`
			PromptCacheKey string                   `json:"prompt_cache_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
			return
		}
		msgs := make([]string, 0, len(body.Messages))
		for _, m := range body.Messages {
			b, err := json.Marshal(m)
			if err != nil {
				t.Errorf("marshal message: %v", err)
			}
			msgs = append(msgs, string(b))
		}
		n := rec.add(recordedRequest{messagesJSON: msgs, cacheKey: body.PromptCacheKey})

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, `data: {"choices":[{"index":0,"delta":{"content":"reply-%d"},"finish_reason":"stop"}]}`+"\n\n", n)
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	t.Cleanup(srv.Close)
	return rec, srv.URL + "/v1/chat/completions"
}

// TestGoalFreshContext_EndToEnd_ResetsConversationAndID drives the full chain
// GoalDriver → agentManagerRunner.RunFresh → agentic.Agent → provider against
// a mock LLM, and validates that switching to a fresh-context goal:
//
//  1. clears the prior conversation out of the provider request (the model
//     must not see pre-goal messages),
//  2. rotates the conversation identity (StreamOptions.SessionID AND the
//     prompt_cache_key stamped on the request),
//  3. keeps appending WITHIN the clean context on subsequent goal turns
//     without rotating again (append-only inside the new conversation).
//
// Regression guard for the fresh-context begin path (Issue 8 semantics).
func TestGoalFreshContext_EndToEnd_ResetsConversationAndID(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end mock-LLM test")
	}
	rec, endpoint := newMockLLM(t)

	cfg := &config.Config{}
	dir := t.TempDir()
	ss := core.NewSessionStore(dir)
	sessionState := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	bus := event.MakeBus(64, 64, 64, 64)
	am := core.NewAgentManager(cfg, ss, nil, sessionState, bus, dir)

	mdl := agenticprovider.Model{
		ID:         "mock-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    endpoint,
		InputTypes: []string{"text"},
	}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{APIKey: "test-key"}, "You are goa test.", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	sessionBeforeReset := am.CurrentAgent().StreamOptions().SessionID

	runner := &agentManagerRunner{agentMgr: am}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Turn 0: an ordinary pre-goal turn — the mock LLM records the FULL
	// conversation including the old question, establishing baseline key K0.
	if err := runner.Run(ctx, "old question about quantum widgets"); err != nil {
		t.Fatalf("pre-goal Run: %v", err)
	}

	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:    "self-contained fresh-context task",
		FreshContext: true,
	}, goal.GoalActorUser); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	driver := &core.GoalDriver{Mode: mode, Agent: runner}
	driveErr := make(chan error, 1)
	go func() { driveErr <- driver.Drive(ctx) }()

	// Wait for two continuation turns (fresh begin + fresh continue), then stop.
	select {
	case <-rec.done:
	case err := <-driveErr:
		t.Fatalf("Drive ended early: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("timeout waiting for 3 recorded requests")
	}
	cancel()
	select {
	case err := <-driveErr:
		_ = err // post-cancel pause of the active goal is expected
	case <-time.After(10 * time.Second):
		t.Fatal("Drive did not exit after cancel")
	}

	reqs := rec.snapshot()
	if len(reqs) < 3 {
		t.Fatalf("recorded %d requests, want >= 3", len(reqs))
	}
	r0, r1, r2 := reqs[0], reqs[1], reqs[2]

	// --- Request 0: the pre-goal turn carries the old conversation.
	if !strings.Contains(r0.text(), "old question about quantum widgets") {
		t.Errorf("request 0 missing pre-goal user input:\n%s", r0.text())
	}
	if r0.cacheKey == "" {
		t.Error("request 0 carried no prompt_cache_key; rotation cannot be observed")
	}

	// --- Request 1 (fresh begin): clean context, NEW identity.
	if strings.Contains(r1.text(), "old question about quantum widgets") {
		t.Errorf("fresh-context request still contains the PRE-GOAL conversation:\n%s", r1.text())
	}
	if strings.Contains(r1.text(), "reply-1") {
		t.Errorf("fresh-context request still contains the PRE-GOAL assistant reply:\n%s", r1.text())
	}
	const continuationMark = "Continue working toward the active goal"
	if !strings.Contains(r1.text(), continuationMark) {
		t.Errorf("fresh-context request missing the continuation prompt:\n%s", r1.text())
	}
	if !strings.Contains(r1.text(), "Context reset") {
		t.Errorf("fresh-context request missing the visible reset boundary note:\n%s", r1.text())
	}
	if r1.cacheKey == "" || r1.cacheKey == r0.cacheKey {
		t.Errorf("prompt_cache_key did not rotate on fresh begin: before=%q after=%q", r0.cacheKey, r1.cacheKey)
	}

	// --- Conversation ID rotated at the manager level too (Hard Rule 7).
	if sessionAfter := am.CurrentAgent().StreamOptions().SessionID; sessionAfter == "" || sessionAfter == sessionBeforeReset {
		t.Errorf("SessionID = %q, want a new non-empty id distinct from %q", sessionAfter, sessionBeforeReset)
	}

	// --- Request 2 (fresh continue): append-only INSIDE the clean context,
	// same identity (no re-rotation), includes the first goal turn's reply.
	if strings.Contains(r2.text(), "old question about quantum widgets") {
		t.Errorf("second goal turn resurrected pre-goal content:\n%s", r2.text())
	}
	if !strings.Contains(r2.text(), `"reply-2"`) {
		t.Errorf("second goal turn lost the first goal turn's assistant reply:\n%s", r2.text())
	}
	if strings.Contains(r2.text(), `"reply-1"`) {
		t.Errorf("second goal turn resurrected a PRE-GOAL assistant reply:\n%s", r2.text())
	}
	if r2.cacheKey != r1.cacheKey {
		t.Errorf("prompt_cache_key rotated mid-conversation: %q -> %q (must stay stable within one conversation)", r1.cacheKey, r2.cacheKey)
	}
	if len(r2.messagesJSON) <= len(r1.messagesJSON) {
		t.Errorf("clean context is not append-only across goal turns: len(r1)=%d len(r2)=%d", len(r1.messagesJSON), len(r2.messagesJSON))
	}
}

// Compile-time guards: the adapter under test must keep satisfying both
// runner interfaces the driver relies on.
var (
	_ core.AgentRunner      = (*agentManagerRunner)(nil)
	_ core.FreshAgentRunner = (*agentManagerRunner)(nil)
)
