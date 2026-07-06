// SPDX-License-Identifier: GPL-3.0-or-later

package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

// TestRuntime_DelegateIsolatesConcurrentSameRole is the RED→GREEN test for
// the per-handle message accumulator. Two delegate(coder, …) calls running
// concurrently must each return ONLY their own streamed answer. The previous
// design accumulated on a shared r.msgs[role] buffer, so the two answers
// clobbered/interleaved — and with a fresh agent per delegation that buffer
// would have been catastrophically wrong (both delegates writing the same key).
func TestRuntime_DelegateIsolatesConcurrentSameRole(t *testing.T) {
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}

	var rt *Runtime
	factory := func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			// Simulate a streamed answer that echoes this delegation's task,
			// then sleep briefly to guarantee the two delegations overlap.
			rt.RecordAgentMessage(h, "answer:"+prompt)
			time.Sleep(30 * time.Millisecond)
			rt.RecordAgentMessage(h, "+tail")
			return nil
		}
		return h, nil
	}
	pool := NewBoundedAgentPool(cfg, factory)
	var err error
	rt, err = NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rt.SetIDGenerator(func() string { return "run-iso" })

	ctx := context.Background()
	var ans1, ans2 string
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ans1, err1 = rt.Delegate(ctx, "coder", "taskA") }()
	go func() { defer wg.Done(); ans2, err2 = rt.Delegate(ctx, "coder", "taskB") }()
	wg.Wait()

	if err1 != nil {
		t.Errorf("delegate 1 err: %v", err1)
	}
	if err2 != nil {
		t.Errorf("delegate 2 err: %v", err2)
	}
	want1, want2 := "answer:taskA+tail", "answer:taskB+tail"
	if ans1 != want1 {
		t.Errorf("delegate(taskA) = %q, want %q (concurrent same-role clobber)", ans1, want1)
	}
	if ans2 != want2 {
		t.Errorf("delegate(taskB) = %q, want %q (concurrent same-role clobber)", ans2, want2)
	}
	// Sanity: the two answers must not contain each other's task marker.
	if strings.Contains(ans1, "taskB") || strings.Contains(ans2, "taskA") {
		t.Errorf("answers cross-contaminated: ans1=%q ans2=%q", ans1, ans2)
	}
}
