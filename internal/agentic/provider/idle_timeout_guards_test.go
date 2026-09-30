// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// TestEventStallTimeout_AlwaysStrictlyInsideByteBudget is the core of the
// single-owner split (bugs.md #2): the event watchdog must fire strictly
// before the byte-level guard for the same silence interval, for every budget
// the product can produce — including the shipped default and the
// provider-pinned windows a user configures.
func TestEventStallTimeout_AlwaysStrictlyInsideByteBudget(t *testing.T) {
	budgets := []time.Duration{
		10 * time.Millisecond,
		45 * time.Second,
		90 * time.Second,
		DefaultStreamIdleTimeout,
		30 * time.Minute,
	}
	for _, b := range budgets {
		stall := EventStallTimeout(b)
		if stall >= b {
			t.Errorf("EventStallTimeout(%s) = %s: the event watchdog must fire strictly before the byte guard", b, stall)
		}
		if stall <= 0 {
			t.Errorf("EventStallTimeout(%s) = %s: must be positive", b, stall)
		}
	}
}

// TestEventStallTimeout_UnsetBudgetUsesProviderDefault keeps the fallback in
// step with the reader: an unset budget must resolve to the same window
// DefaultStreamIdleTimeout arms, not to a separate constant.
func TestEventStallTimeout_UnsetBudgetUsesProviderDefault(t *testing.T) {
	want := DefaultStreamIdleTimeout * 3 / 4
	for _, b := range []time.Duration{0, -time.Second} {
		if got := EventStallTimeout(b); got != want {
			t.Errorf("EventStallTimeout(%s) = %s, want %s (the provider default's share)", b, got, want)
		}
	}
}

// TestEventStallTimeout_OutlastsALongReasoningTurn guards the shipped default
// against regressing to a window that kills healthy reasoning turns: the
// captured z.ai export-A request ran 80.7s of pure silence before its first
// token, and the default byte budget (5m) must clear that with margin.
func TestEventStallTimeout_OutlastsALongReasoningTurn(t *testing.T) {
	const observedReasoningSilence = 80700 * time.Millisecond
	stall := EventStallTimeout(DefaultStreamIdleTimeout)
	if stall < 2*observedReasoningSilence {
		t.Errorf("default event stall %s leaves under 2x margin over the observed %s reasoning silence", stall, observedReasoningSilence)
	}
}

// TestEventStallTimeout_BackstopFiresFirst is the timing half of the same
// guarantee, measured rather than derived: with a byte guard armed on a
// blocking reader, the reader must still be waiting at the moment the event
// stall elapses. Before the split, both timers shared one budget and the byte
// guard could win the race — deciding the error the user saw.
func TestEventStallTimeout_BackstopFiresFirst(t *testing.T) {
	const budget = 400 * time.Millisecond
	stall := EventStallTimeout(budget)
	r := NewIdleTimeoutReader(io.NopCloser(&neverReader{}), budget)
	defer func() { _ = r.Close() }()

	errCh := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 8))
		errCh <- err
	}()

	select {
	case err := <-errCh:
		t.Fatalf("byte guard fired at the %s event stall (err=%v): it must not pre-empt the watchdog", stall, err)
	case <-time.After(stall):
		// The event watchdog's deadline passed with the byte guard still
		// waiting — the watchdog owns the silence window.
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrStreamIdle) {
			t.Fatalf("byte guard error = %v, want ErrStreamIdle once the full %s budget elapses", err, budget)
		}
	case <-time.After(4 * budget):
		t.Fatalf("byte guard never fired within %s of total silence", 4*budget)
	}
}

// TestWebSocketBackstop_UsesTheByteBudget extends the single-owner guarantee to
// the WebSocket transport: its message-idle guard used to sit at a fixed 2m
// while the agent's event watchdog waited 3/4 of the (default 5m) byte budget
// — the backstop expired first and pre-empted the warn-then-retry path. The
// transport now carries the same byte budget the SSE reader resolves.
func TestWebSocketBackstop_UsesTheByteBudget(t *testing.T) {
	cases := []struct {
		name string
		opts schema.StreamOptions
		want time.Duration
	}{
		{
			name: "unset budget resolves to the provider default",
			opts: schema.StreamOptions{Transport: schema.TransportWebSocket},
			want: DefaultStreamIdleTimeout,
		},
		{
			name: "configured budget is carried through",
			opts: schema.StreamOptions{Transport: schema.TransportWebSocket, IdleTimeout: 90 * time.Second},
			want: 90 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws, ok := selectTransport(tc.opts).(*transport.WebSocketTransport)
			if !ok {
				t.Fatalf("selectTransport returned %T, want *transport.WebSocketTransport", selectTransport(tc.opts))
			}
			if ws.IdleTimeout != tc.want {
				t.Errorf("WebSocketTransport.IdleTimeout = %s, want the byte budget %s", ws.IdleTimeout, tc.want)
			}
			if stall := EventStallTimeout(ws.IdleTimeout); stall >= ws.IdleTimeout {
				t.Errorf("event stall %s must stay strictly inside the %s WebSocket backstop", stall, ws.IdleTimeout)
			}
		})
	}
}
