// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	gorole "github.com/pijalu/goa/internal/role"
)

// T0: DelegateTool must mint a unique dlg-<role>-<NN> id per call and return
// it in the ack JSON, so concurrent same-role delegations are distinguishable.
func TestDelegateTool_MintDelegationID_UniquePerCall(t *testing.T) {
	tool := &DelegateTool{}
	id1 := tool.mintDelegationID(gorole.Coder)
	id2 := tool.mintDelegationID(gorole.Coder)
	if id1 == id2 {
		t.Fatalf("two mints for the same role returned the same id %q", id1)
	}
	if !strings.HasPrefix(id1, "dlg-coder-") {
		t.Errorf("id %q does not follow dlg-<role>-<NN>", id1)
	}
	// Per-role sequence: first coder id is 01, second is 02.
	if !strings.HasSuffix(id1, "-01") || !strings.HasSuffix(id2, "-02") {
		t.Errorf("expected per-role sequence 01 then 02, got %q then %q", id1, id2)
	}
	// Different roles have independent sequences.
	other := tool.mintDelegationID(gorole.Planner)
	if !strings.HasPrefix(other, "dlg-planner-01") && other != "dlg-planner-01" {
		t.Errorf("planner sequence should start at 01 independently, got %q", other)
	}
}

// T0: concurrent mints across roles must never collide.
func TestDelegateTool_MintDelegationID_Concurrent(t *testing.T) {
	tool := &DelegateTool{}
	const perRole = 50
	roles := []string{gorole.Coder, gorole.Planner, gorole.Companion}

	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for _, role := range roles {
		for i := 0; i < perRole; i++ {
			wg.Add(1)
			go func(r string) {
				defer wg.Done()
				id := tool.mintDelegationID(r)
				mu.Lock()
				if seen[id] {
					t.Errorf("duplicate delegation id %q", id)
				}
				seen[id] = true
				mu.Unlock()
			}(role)
		}
	}
	wg.Wait()
	if len(seen) != perRole*len(roles) {
		t.Errorf("expected %d unique ids, got %d", perRole*len(roles), len(seen))
	}
}

// ackID parses the delegate_to ack JSON and returns its id field.
func ackID(t *testing.T, out string) string {
	t.Helper()
	var ack struct {
		Status string `json:"status"`
		Agent  string `json:"agent"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &ack); err != nil {
		t.Fatalf("ack is not valid JSON: %v (%q)", err, out)
	}
	return ack.ID
}

// drainRoleMessages drains the orchestrator's buffered events and returns
// those emitted by the given role.
func drainRoleMessages(orch *ForegroundOrchestrator, role string) []OrchestratorMessage {
	var out []OrchestratorMessage
	for {
		select {
		case m := <-orch.Events():
			if m.From == role {
				out = append(out, m)
			}
		default:
			return out
		}
	}
}

// T0: end-to-end — delegate_to returns the minted id in the ack JSON and the
// orchestrator stamps that id onto the delegation's streamed messages.
func TestDelegateTool_Execute_ReturnsDelegationID(t *testing.T) {
	pool := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	tool := &DelegateTool{Orchestrator: orch, Pool: pool, Enabled: true}

	out, err := tool.Execute(fmt.Sprintf(`{"agent":%q,"task":"do work"}`, gorole.Coder))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	id := ackID(t, out)
	if !strings.HasPrefix(id, "dlg-coder-") {
		t.Errorf("ack.id %q is not a dlg-coder-* id", id)
	}

	// The delegated run streamed through the orchestrator; every message the
	// coder role emitted must carry the minted delegation id.
	msgs := drainRoleMessages(orch, gorole.Coder)
	if len(msgs) == 0 {
		t.Fatal("no coder stream messages observed; DelegationID propagation untested")
	}
	for _, m := range msgs {
		if m.DelegationID != id {
			t.Errorf("stream message DelegationID=%q, want %q", m.DelegationID, id)
		}
	}

	// A second delegation mints a DIFFERENT id.
	out2, err := tool.Execute(fmt.Sprintf(`{"agent":%q,"task":"more work"}`, gorole.Coder))
	if err != nil {
		t.Fatalf("second Execute failed: %v", err)
	}
	if id2 := ackID(t, out2); id2 == id {
		t.Errorf("two delegations returned the same id %q", id)
	}
}
