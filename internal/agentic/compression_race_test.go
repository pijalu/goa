// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"sync"
	"testing"
)

// TestCompression_NoRaceWithHistoryReaders verifies that context compression
// — which mutates a.history — does not race with concurrent off-turn history
// readers (the public ContextStats / GetHistory, which acquire the mutex).
//
// Run with `go test -race`. The previous implementation mutated history in
// compressToolElision/compressSelective/microCompactForced/enforceContextCeiling
// without the agent mutex; the first concurrent off-turn reader would turn
// this into a live data race on a.history. These entry points now hold a.mu.
func TestCompression_NoRaceWithHistoryReaders(t *testing.T) {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MaxTokens:           1000,
				Strategy:            CompressionToolElision,
				PreserveRecentTurns: 1,
			},
		},
		history: historyWithNToolResults(40, 400),
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader A: ContextStats reads history under the lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.ContextStats()
			}
		}
	}()

	// Reader B: GetHistory copies history under the lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.GetHistory()
			}
		}
	}()

	// Writer: force compression repeatedly via the public entry point. Each
	// call mutates history (elides tool results / escalates).
	for i := 0; i < 50; i++ {
		if err := a.MaybeCompressWith(ctx, CompressionToolElision, true); err != nil {
			t.Fatalf("MaybeCompressWith: %v", err)
		}
	}

	close(stop)
	wg.Wait()
}

// TestEnforceContextCeiling_NoRaceWithReaders verifies the last-resort ceiling
// enforcer (which reassigns a.history) is race-free against concurrent readers.
func TestEnforceContextCeiling_NoRaceWithReaders(t *testing.T) {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MaxTokens: 500, // tiny window so the ceiling always trims
			},
		},
		history: historyWithNToolResults(40, 400),
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.GetHistory()
			}
		}
	}()

	for i := 0; i < 50; i++ {
		// Re-populate so the ceiling has something to trim each iteration.
		a.mu.Lock()
		a.history = historyWithNToolResults(40, 400)
		a.mu.Unlock()
		a.enforceContextCeiling()
	}

	close(stop)
	wg.Wait()
}
