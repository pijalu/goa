// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/pijalu/goa/multiagent"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// T0: translateDelegationMsg maps delegation-source OrchestratorMessages to
// neutral AgentViewEvents, passing DelegationID/From through, and rejects
// unknown or framing-only kinds with ok=false.
func TestTranslateDelegationMsg(t *testing.T) {
	ts := time.Now()
	tests := []struct {
		name string
		in   multiagent.OrchestratorMessage
		want orchpanel.AgentViewEvent
		ok   bool
	}{
		{
			name: "content stream chunk → agent message delta",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "stream_chunk", Kind: "content",
				Content: "hello", DelegationID: "dlg-coder-01", Timestamp: ts,
			},
			want: orchpanel.AgentViewEvent{
				Kind: orchpanel.EvAgentMessage, AgentID: "coder", Role: "coder",
				DelegationID: "dlg-coder-01", Text: "hello", IsDelta: true,
			},
			ok: true,
		},
		{
			name: "thinking chunk → agent thinking delta",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "stream_chunk", Kind: "thinking_chunk",
				Content: "hmm", DelegationID: "dlg-coder-02", Timestamp: ts,
			},
			want: orchpanel.AgentViewEvent{
				Kind: orchpanel.EvAgentThinking, AgentID: "coder", Role: "coder",
				DelegationID: "dlg-coder-02", Text: "hmm", IsDelta: true,
			},
			ok: true,
		},
		{
			name: "stream_start framing is not viewable",
			in:   multiagent.OrchestratorMessage{From: "coder", To: "stream_start", Kind: "content"},
			ok:   false,
		},
		{
			// T4: stream_end carries the authoritative full text — the chunk
			// fanout is lossy, so the terminal frame is a non-delta reconcile.
			name: "stream_end → full-text reconcile message",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "stream_end", Kind: "content",
				Content: "full", DelegationID: "dlg-coder-01", Timestamp: ts,
			},
			want: orchpanel.AgentViewEvent{
				Kind: orchpanel.EvAgentMessage, AgentID: "coder", Role: "coder",
				DelegationID: "dlg-coder-01", Text: "full",
			},
			ok: true,
		},
		{
			name: "thinking_start framing is not viewable",
			in:   multiagent.OrchestratorMessage{From: "coder", To: "stream_start", Kind: "thinking_start"},
			ok:   false,
		},
		{
			name: "thinking_end → full-text thinking reconcile",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "thinking_end", Kind: "thinking_end",
				Content: "all my thinking", DelegationID: "dlg-coder-01", Timestamp: ts,
			},
			want: orchpanel.AgentViewEvent{
				Kind: orchpanel.EvAgentThinking, AgentID: "coder", Role: "coder",
				DelegationID: "dlg-coder-01", Text: "all my thinking",
			},
			ok: true,
		},
		{
			name: "delegation_state running → agent started",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "delegation", Kind: "delegation_state",
				Content: "running|", DelegationID: "dlg-coder-01", Timestamp: ts,
			},
			want: orchpanel.AgentViewEvent{
				Kind: orchpanel.EvAgentStarted, AgentID: "coder", Role: "coder",
				DelegationID: "dlg-coder-01",
			},
			ok: true,
		},
		{
			name: "delegation_state completed → agent finished",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "delegation", Kind: "delegation_state",
				Content: "completed|", DelegationID: "dlg-coder-01", Timestamp: ts,
			},
			want: orchpanel.AgentViewEvent{
				Kind: orchpanel.EvAgentFinished, AgentID: "coder", Role: "coder",
				DelegationID: "dlg-coder-01", Status: "completed",
			},
			ok: true,
		},
		{
			name: "delegation_state failed → agent finished with error detail",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "delegation", Kind: "delegation_state",
				Content: "failed|provider 400: bad request", DelegationID: "dlg-coder-01", Timestamp: ts,
			},
			want: orchpanel.AgentViewEvent{
				Kind: orchpanel.EvAgentFinished, AgentID: "coder", Role: "coder",
				DelegationID: "dlg-coder-01", Status: "failed", Text: "provider 400: bad request",
			},
			ok: true,
		},
		{
			name: "delegation_state unknown state → false",
			in: multiagent.OrchestratorMessage{
				From: "coder", To: "delegation", Kind: "delegation_state",
				Content: "mystery|", DelegationID: "dlg-coder-01", Timestamp: ts,
			},
			ok: false,
		},
		{
			name: "unknown kind → false",
			in:   multiagent.OrchestratorMessage{From: "coder", To: "user", Kind: "totally_unknown"},
			ok:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := translateDelegationMsg(tc.in)
			if ok != tc.ok {
				t.Fatalf("translateDelegationMsg ok=%v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("translateDelegationMsg = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// T0: the adapter must thread the delegation identity so two concurrent
// same-role delegations stay distinguishable after translation.
func TestTranslateDelegationMsg_PassesDelegationID(t *testing.T) {
	a, okA := translateDelegationMsg(multiagent.OrchestratorMessage{
		From: "coder", To: "stream_chunk", Kind: "content", Content: "x", DelegationID: "dlg-coder-01",
	})
	b, okB := translateDelegationMsg(multiagent.OrchestratorMessage{
		From: "coder", To: "stream_chunk", Kind: "content", Content: "x", DelegationID: "dlg-coder-02",
	})
	if !okA || !okB {
		t.Fatal("both content chunks should translate")
	}
	if a.DelegationID != "dlg-coder-01" || b.DelegationID != "dlg-coder-02" {
		t.Errorf("DelegationID not threaded: a=%q b=%q", a.DelegationID, b.DelegationID)
	}
	if a.DelegationID == b.DelegationID {
		t.Error("same-role delegations collapsed to one id")
	}
}
