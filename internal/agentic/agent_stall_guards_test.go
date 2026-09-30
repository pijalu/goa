// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestStallGuards_SingleOwnerOrdering is the ordering contract of bugs.md #2:
// one silence budget is split between two guards, and they must never race on
// it. The warning leads, the event watchdog owns the window (it is the guard
// that retries), and the byte-level reader keeps the whole budget as backstop:
//   warn < event stall < byte budget
func TestStallGuards_SingleOwnerOrdering(t *testing.T) {
	a := NewAgent(Config{})
	cases := []struct {
		name        string
		opts        provider.StreamOptions
		wantByte    time.Duration
		wantStall   time.Duration
		wantWarn    time.Duration
		description string
	}{
		{
			name:        "shipped defaults",
			opts:        provider.StreamOptions{IdleTimeout: 5 * time.Minute, ActivityWarnAfter: 200 * time.Second},
			wantByte:    5 * time.Minute,
			wantStall:   225 * time.Second,
			wantWarn:    200 * time.Second,
			description: "activity_timeout 5m / activity_warn_after 3m20s",
		},
		{
			name:        "provider default budget (no configured window)",
			opts:        provider.StreamOptions{},
			wantByte:    provider.DefaultStreamIdleTimeout,
			wantStall:   provider.EventStallTimeout(provider.DefaultStreamIdleTimeout),
			wantWarn:    provider.EventStallTimeout(provider.DefaultStreamIdleTimeout) * 2 / 3,
			description: "unset keys fall back to the provider default",
		},
		{
			name:        "user-pinned provider window without a lead",
			opts:        provider.StreamOptions{IdleTimeout: 90 * time.Second},
			wantByte:    90 * time.Second,
			wantStall:   67500 * time.Millisecond,
			wantWarn:    45 * time.Second,
			description: "a configured window with no warn key derives the lead",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stall := a.effectiveEventStallTimeout(tc.opts)
			if stall != tc.wantStall {
				t.Errorf("event stall = %s, want %s (%s)", stall, tc.wantStall, tc.description)
			}
			warn := a.effectiveStallWarnAfter(tc.opts)
			if warn != tc.wantWarn {
				t.Errorf("warning lead = %s, want %s (%s)", warn, tc.wantWarn, tc.description)
			}
			if warn >= stall {
				t.Errorf("the %s warning lead must fire before the %s event stall it announces", warn, stall)
			}
			if stall >= tc.wantByte {
				t.Errorf("the %s event stall must fire strictly before the %s byte guard — one owner per silence window", stall, tc.wantByte)
			}
		})
	}
}

// TestStallGuards_SilentStreamOwnedByEventWatchdog pins the observable
// consequence: when a stream opens and delivers nothing at all, the turn must
// fail with the WATCHDOG's stall error (the one the retry path understands),
// not with the byte guard's idle-timeout error. Before the split both guards
// were armed on the same budget and whichever timer won the race decided which
// error the user saw.
func TestStallGuards_SilentStreamOwnedByEventWatchdog(t *testing.T) {
	p := &silentStreamProvider{api: provider.Api(fmt.Sprintf("test-single-owner-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	const byteBudget = 400 * time.Millisecond
	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "single-owner-test",
			Api:        p.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries:  1,
			IdleTimeout: byteBudget,
		},
	})
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, "prompt") }()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		t.Fatalf("silent stream did not end within 10s (byte budget %s)", byteBudget)
	}
	if err == nil {
		t.Fatal("a fully silent stream must end the turn with a stall error")
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Errorf("turn error = %v, want the event watchdog's stall error", err)
	}
	if strings.Contains(err.Error(), "idle timeout") {
		t.Errorf("turn error = %v, want the byte guard NOT to be the owner of the silence window", err)
	}
	if p.calls.Load() < 2 {
		t.Errorf("provider calls = %d, want the stall to engage the retry path", p.calls.Load())
	}
}
