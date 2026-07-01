// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestAgentPool_GetOrCreate_Concurrent creates the same role from many
// goroutines. The callback must not deadlock with the pool mutex.
func TestAgentPool_GetOrCreate_Concurrent(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)

	var called int
	var mu sync.Mutex
	p.OnAgentCreated = func(role string, agent *agentic.Agent) {
		mu.Lock()
		called++
		mu.Unlock()
		// Deliberately call back into the pool while the callback is running.
		// This would deadlock if GetOrCreate held the mutex during OnAgentCreated.
		_ = p.Get(role)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.GetOrCreate("reviewer")
			if err != nil {
				t.Errorf("GetOrCreate failed: %v", err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	if called != 1 {
		t.Errorf("OnAgentCreated called %d times, want 1", called)
	}
	mu.Unlock()
}
