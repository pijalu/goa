// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/perms"
)

func TestExecutionController_RequestConfirm_PermissionRules(t *testing.T) {
	ec := newEC(internal.ExecutionYolo)
	ec.SetPermissionRules([]perms.Rule{
		{Pattern: "bash", Decision: perms.DecisionAsk},
		{Pattern: "read", Decision: perms.DecisionAllow},
		{Pattern: "rm", Decision: perms.DecisionDeny},
	})

	cases := []struct {
		toolName string
		want     internal.ConfirmResponse
	}{
		{"read", internal.ConfirmYes},  // allow rule
		{"rm", internal.ConfirmNo},     // deny rule
		{"bash", internal.ConfirmNo},   // ask rule but no consumer -> safe default
		{"write", internal.ConfirmYes}, // no rule, yolo mode
	}

	for _, tc := range cases {
		got := ec.RequestConfirm(tc.toolName, "")
		if got != tc.want {
			t.Errorf("RequestConfirm(%q) = %v, want %v", tc.toolName, got, tc.want)
		}
	}
}

func TestExecutionController_RequestConfirm_PermissionRuleAsk(t *testing.T) {
	ec := newEC(internal.ExecutionYolo)
	ec.SetPermissionRules([]perms.Rule{
		{Pattern: "bash", Decision: perms.DecisionAsk},
	})

	respCh := make(chan internal.ConfirmResponse, 1)
	ec.SetConfirmConsumer(func(_ context.Context, req internal.ConfirmRequest) error {
		req.ResponseChan <- internal.ConfirmYes
		return nil
	})

	go func() { respCh <- ec.RequestConfirm("bash", "ls") }()
	got := <-respCh
	if got != internal.ConfirmYes {
		t.Errorf("RequestConfirm with ask rule = %v, want ConfirmYes", got)
	}
}

func TestExecutionController_PermissionRules(t *testing.T) {
	ec := newEC(internal.ExecutionYolo)
	rules := []perms.Rule{{Pattern: "bash", Decision: perms.DecisionAllow}}
	ec.SetPermissionRules(rules)
	got := ec.PermissionRules()
	if len(got) != 1 || got[0].Pattern != "bash" {
		t.Errorf("PermissionRules() = %+v, want one bash rule", got)
	}
}
