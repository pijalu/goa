// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

func poolCfg(total int, perModel map[string]int, roles ...[2]string) config.OrchestratorConfig {
	roleMap := map[string]config.OrchestratorRole{}
	for _, r := range roles {
		roleMap[r[0]] = config.OrchestratorRole{Model: r[1]}
	}
	return config.OrchestratorConfig{
		Roles: roleMap,
		Pool:  config.OrchestratorPoolConfig{MaxTotalAgents: total, MaxAgentsPerModel: perModel},
	}
}

func TestBoundedAgentPool_TotalCap(t *testing.T) {
	cfg := poolCfg(2, nil, [2]string{"coder", "m1"})
	var created atomic.Int32
	p := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		created.Add(1)
		return h, nil
	})

	ctx := context.Background()
	h1, err := p.Acquire(ctx, "coder", AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	h2, err := p.Acquire(ctx, "coder", AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}

	// Third acquire must block until a release.
	acquired := make(chan error, 1)
	go func() {
		_, err := p.Acquire(ctx, "coder", AcquireOptions{})
		acquired <- err
	}()
	select {
	case <-acquired:
		t.Fatalf("third acquire returned before release")
	case <-time.After(50 * time.Millisecond):
	}

	p.Release(h1)
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("acquire 3 after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("acquire 3 did not unblock after release")
	}

	p.Release(h2)
	if got := created.Load(); got != 3 {
		t.Errorf("factory invoked %d times, want 3", got)
	}
}

func TestBoundedAgentPool_PerModelCap(t *testing.T) {
	cfg := poolCfg(0, map[string]int{"m1": 1},
		[2]string{"coder", "m1"},
		[2]string{"planner", "m1"},
		[2]string{"reviewer", "m2"},
	)
	p := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("", role, model), nil
	})

	ctx := context.Background()
	h1, err := p.Acquire(ctx, "coder", AcquireOptions{})
	if err != nil {
		t.Fatalf("coder: %v", err)
	}
	defer p.Release(h1)

	// planner uses the same model — must block.
	acquireCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	acquired := make(chan error, 1)
	go func() {
		_, err := p.Acquire(acquireCtx, "planner", AcquireOptions{})
		acquired <- err
	}()
	select {
	case <-acquired:
		t.Fatalf("planner (same model) acquired despite per-model cap")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-acquired:
		// expected cancellation
	case <-time.After(time.Second):
		t.Fatal("blocked acquire did not return after cancel")
	}

	// reviewer uses a different model — must succeed immediately.
	if _, err := p.Acquire(ctx, "reviewer", AcquireOptions{}); err != nil {
		t.Fatalf("reviewer (different model): %v", err)
	}

	total, perModel := p.Counts()
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if perModel["m1"] != 1 || perModel["m2"] != 1 {
		t.Errorf("perModel = %v, want m1=1 m2=1", perModel)
	}
}

func TestBoundedAgentPool_CancelWhileBlocked(t *testing.T) {
	cfg := poolCfg(1, nil, [2]string{"coder", "m1"})
	p := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("", role, model), nil
	})
	ctx := context.Background()
	if _, err := p.Acquire(ctx, "coder", AcquireOptions{}); err != nil {
		t.Fatalf("acquire 1: %v", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Acquire(cctx, "coder", AcquireOptions{})
		errCh <- err
	}()
	<-time.After(30 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("blocked acquire did not return after cancel")
	}
}

func TestBoundedAgentPool_FactoryErrorRollsBack(t *testing.T) {
	cfg := poolCfg(1, nil, [2]string{"coder", "m1"})
	calls := atomic.Int32{}
	p := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		calls.Add(1)
		return nil, errors.New("boom")
	})
	if _, err := p.Acquire(context.Background(), "coder", AcquireOptions{}); err == nil {
		t.Fatalf("expected factory error to propagate")
	}
	total, _ := p.Counts()
	if total != 0 {
		t.Errorf("total = %d after factory error, want 0 (reservation rolled back)", total)
	}
}

func TestBoundedAgentPool_UnknownRole(t *testing.T) {
	p := NewBoundedAgentPool(poolCfg(1, nil, [2]string{"coder", "m1"}), func(_, _ string, _ AcquireOptions) (*AgentHandle, error) {
		t.Fatalf("factory must not be called for unknown role")
		return nil, nil
	})
	if _, err := p.Acquire(context.Background(), "ghost", AcquireOptions{}); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("expected ErrUnknownRole, got %v", err)
	}
}

func TestBoundedAgentPool_ReleaseIdempotent(t *testing.T) {
	cfg := poolCfg(1, nil, [2]string{"coder", "m1"})
	p := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("", role, model), nil
	})
	h, err := p.Acquire(context.Background(), "coder", AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	p.Release(h)
	p.Release(h) // must not double-decrement
	total, _ := p.Counts()
	if total != 0 {
		t.Errorf("total = %d after double release, want 0", total)
	}
}

// TestBoundedAgentPool_Concurrent exercises the pool under -race with many
// concurrent acquirers stressing both caps.
func TestBoundedAgentPool_Concurrent(t *testing.T) {
	cfg := poolCfg(4, map[string]int{"m1": 2, "m2": 2},
		[2]string{"a", "m1"},
		[2]string{"b", "m1"},
		[2]string{"c", "m2"},
		[2]string{"d", "m2"},
	)
	p := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("", role, model), nil
	})

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	ctx := context.Background()
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			role := "a"
			switch i % 4 {
			case 1:
				role = "b"
			case 2:
				role = "c"
			case 3:
				role = "d"
			}
			h, err := p.Acquire(ctx, role, AcquireOptions{})
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			// Hold briefly so caps actually bite.
			time.Sleep(time.Millisecond)
			p.Release(h)
		}(i)
	}
	wg.Wait()
	total, perModel := p.Counts()
	if total != 0 {
		t.Errorf("total = %d after all released, want 0", total)
	}
	if perModel["m1"] != 0 || perModel["m2"] != 0 {
		t.Errorf("perModel = %v, want all zero", perModel)
	}
}
