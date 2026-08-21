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
			name: "stream_end framing is not viewable",
			in:   multiagent.OrchestratorMessage{From: "coder", To: "stream_end", Kind: "content", Content: "full"},
			ok:   false,
		},
		{
			name: "thinking_start framing is not viewable",
			in:   multiagent.OrchestratorMessage{From: "coder", To: "stream_start", Kind: "thinking_start"},
			ok:   false,
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
