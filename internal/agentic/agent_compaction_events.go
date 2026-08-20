// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// Compaction provenance events (CX4, dsh compaction types.ts): every
// summarize compaction is a durable transaction recorded in the session log
// as an ordered triple sharing one compaction id:
//
//	compaction_start   — the transaction opened (carries the fresh id)
//	compaction_summary — the completed summary's inputs and model-call facts
//	                     (shadowed message range, freed tokens, provider,
//	                     model, provider-reported usage of the summarize call)
//	compaction_end     — the transaction closed; Error records a failed attempt
//
// The triple rides the normal observer pipeline, so it lands in the session
// JSONL whenever persistence is enabled (AgentManager.forwardEvent →
// SessionStore.WriteEvent) and is skipped by EventsToHistory on restore. On a
// crash between start and end the log holds a start with no matching end;
// FindOrphanedCompactions detects exactly that on the next boot.
const (
	// EventCompactionStart marks the start of a compaction transaction.
	EventCompactionStart EventType = "compaction_start"
	// EventCompactionSummary records the completed summary and the facts of
	// the model call that produced it.
	EventCompactionSummary EventType = "compaction_summary"
	// EventCompactionEnd marks the end of a compaction transaction. Error is
	// set on an unsuccessful attempt; a start with no end at all means the
	// process died mid-compaction (see FindOrphanedCompactions).
	EventCompactionEnd EventType = "compaction_end"
)

// CompactionTx is the structured payload of the compaction provenance triple
// (EventCompactionStart / EventCompactionSummary / EventCompactionEnd). The
// event Type discriminates which fields are populated: start carries only ID;
// summary carries the shadowed-range and model-call facts; end carries Error
// on failure. One transaction shares one ID across all three events.
type CompactionTx struct {
	// ID correlates the start/summary/end events of one compaction
	// transaction ("<cx>-<unix-nano>-<hex>").
	ID string `json:"id"`

	// ShadowedStart is the inclusive history index at which the compacted
	// range began (always 0 today: summarize shadows the whole history).
	ShadowedStart int `json:"shadowed_start,omitempty"`
	// ShadowedEnd is the exclusive history index at which the compacted
	// range ended — the pre-compaction history length.
	ShadowedEnd int `json:"shadowed_end,omitempty"`
	// ShadowedCount is the number of history messages the checkpoint
	// replaces (ShadowedEnd - ShadowedStart).
	ShadowedCount int `json:"shadowed_count,omitempty"`
	// ShadowedTokens is the estimated token price of the shadowed range
	// before replacement (the shadow price a consumer subtracts).
	ShadowedTokens int `json:"shadowed_tokens,omitempty"`
	// FreedTokens is the estimated token drop caused by the replacement
	// (shadowed minus checkpoint; may be small when the checkpoint is rich).
	FreedTokens int `json:"freed_tokens,omitempty"`

	// Provider is the provider route that wrote the summary.
	Provider string `json:"provider,omitempty"`
	// Model is the model that wrote the summary, so "which model wrote this
	// checkpoint" has a durable answer in the session log.
	Model string `json:"model,omitempty"`
	// Usage is the provider-reported token usage of the summarize request,
	// when the provider emitted one (nil otherwise).
	Usage *provider.Usage `json:"usage,omitempty"`

	// Error records an unsuccessful compaction attempt on compaction_end
	// (empty on success). dsh compaction/end {error?}.
	Error string `json:"error,omitempty"`
}

// newCompactionTxID returns a fresh compaction transaction id.
func newCompactionTxID() string {
	return internal.PrefixedHexID("cx", 4)
}

// emitCompactionTxStart emits the compaction_start provenance event.
func (a *Agent) emitCompactionTxStart(id string) {
	a.emitEvent(OutputEvent{
		Type:         EventCompactionStart,
		CompactionTx: &CompactionTx{ID: id},
	})
}

// emitCompactionTxSummary emits the compaction_summary provenance event with
// the shadowed-range and model-call facts of the completed summarize.
func (a *Agent) emitCompactionTxSummary(tx *CompactionTx) {
	a.emitEvent(OutputEvent{
		Type:         EventCompactionSummary,
		CompactionTx: tx,
	})
}

// emitCompactionTxEnd emits the compaction_end provenance event. A non-empty
// errMsg marks the transaction as failed; an empty string closes it cleanly.
func (a *Agent) emitCompactionTxEnd(id, errMsg string) {
	a.emitEvent(OutputEvent{
		Type:         EventCompactionEnd,
		CompactionTx: &CompactionTx{ID: id, Error: errMsg},
	})
}

// FindOrphanedCompactions scans a sequence of output events (as persisted in
// a session JSONL log) and returns the ids of compaction transactions that
// opened (compaction_start) but never closed (no compaction_end with the same
// id). An orphan means the process died mid-compaction — the crash window
// between start and end — so on next boot the log itself answers "was a
// compaction in flight when we went down?".
//
// Events are consumed in log order; a transaction whose end arrives is closed
// regardless of what lies between (summary is informational). Ids are
// reported in first-start order, deduplicated.
func FindOrphanedCompactions(events []OutputEvent) []string {
	open := make(map[string]bool)
	var order []string
	for _, ev := range events {
		if ev.CompactionTx == nil || ev.CompactionTx.ID == "" {
			continue
		}
		id := ev.CompactionTx.ID
		switch ev.Type {
		case EventCompactionStart:
			if !open[id] {
				open[id] = true
				order = append(order, id)
			}
		case EventCompactionEnd:
			delete(open, id)
		}
	}
	var orphans []string
	for _, id := range order {
		if open[id] {
			orphans = append(orphans, id)
		}
	}
	return orphans
}
