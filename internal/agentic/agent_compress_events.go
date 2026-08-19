// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

type compactionResult struct {
	removed     int  // messages dropped from history
	freedTokens int  // estimated tokens freed (0 = unknown)
	changed     int  // messages mutated in place (elision / micro truncation)
	escalated   bool // a cheaper step escalated into a harder one (elision→selective)
}

// didWork reports whether the pass changed history in any observable way.
func (r compactionResult) didWork() bool {
	return r.removed > 0 || r.changed > 0 || r.freedTokens > 0
}

// emitCompactionResult emits a single structured EventCompact after a
// compression pass, measuring the post-pass usage off-lock. It emits nothing
// when the pass did no work so observers never see a phantom compaction.
func (a *Agent) emitCompactionResult(strategy string, before ContextStats, res compactionResult, detail string) {
	if !res.didWork() {
		return
	}
	a.emitCompaction(strategy, before, a.ContextStats(), res.removed, res.freedTokens, detail)
}

// emitCompaction emits the structured EventCompact shared by every
// compression path. The Text label mirrors Compaction.Strategy so legacy
// consumers keyed on Text (session JSONL greps, the footer classifier) keep
// working while new consumers read the structured payload.
//
// Every emission implies history was just mutated (emitCompactionResult
// only fires on didWork), so the sticky-instruction dedup state is reset
// here: the previously persisted sticky message may have been elided or
// dropped, and the next turn must re-persist it. Centralizing the reset at
// this choke point means no compression path can forget it.
func (a *Agent) emitCompaction(strategy string, before, after ContextStats, removed, freed int, detail string) {
	a.InvalidateStickyInstructions()
	a.emitEvent(OutputEvent{
		Type: EventCompact,
		Text: strategy,
		Compaction: &CompactionInfo{
			Strategy:    strategy,
			BeforePct:   before.UsagePercent,
			AfterPct:    after.UsagePercent,
			FreedTokens: freed,
			Removed:     removed,
			Detail:      detail,
		},
	})
}

// runElision executes compressToolElision under a.mu and returns the
// pre-pass stats plus the work done. The emission happens after unlock in
// the caller.
