// SPDX-License-Identifier: GPL-3.0-or-later
//
// Stale tool-output pruning (P4, dsh prune parity): zero-cost model-free
// reclaim of old large tool-result bodies before the LLM summarizer.
//
// The pre-compaction threshold pruner (CX1) rewrites EVERY over-budget tool
// result, recent or not, to head+marker+tail — but a long dump-heavy session
// still carries the entire recent window's worth of large bodies. This pass
// walks history newest-first, protects the trailing recent window (the last N
// messages plus the most recent ~40K tokens), and replaces older tool-result
// bodies of ≥200 estimated tokens with a one-line placeholder. It runs only
// when the reclaimable total reaches the gate (≥20K tokens), so short sessions
// are untouched, and it is idempotent: an already-pruned body is never pruned
// again, non-tool messages are never touched, and the tool-call/result pairing
// (ToolCallID, ToolName) is preserved so provider replay stays valid.

package agentic

import (
	"fmt"
	"strings"
)

// Stale-pruning budget constants (dsh prune parity; zero exposes the same
// defaults in prune.go):
//
//	stalePruneProtectMessages — the most recent N messages are always kept.
//	stalePruneProtectTokens   — the trailing recent-token window (≈40K).
//	stalePruneMinBodyTokens   — bodies below this size are not worth pruning.
//	stalePruneGateTokens      — the pass only runs when ≥ this much is
//	                            reclaimable, so short sessions are untouched.
const (
	stalePruneProtectMessages = 10
	stalePruneProtectTokens   = 40000
	stalePruneMinBodyTokens   = 200
	stalePruneGateTokens      = 20000
)

// stalePrunePrefix is the leading marker of a pruned body. It doubles as the
// idempotency check: a body already starting with it is never pruned again.
const stalePrunePrefix = "[goa-system] pruned "

// stalePrunePlaceholderFmt renders the placeholder for a pruned body: the tool
// name and the estimated token count of the original, so the model knows what
// was dropped and can re-run the tool if needed.
const stalePrunePlaceholderFmt = stalePrunePrefix + "%s output (~%d tokens) — re-run the tool if needed"

// stalePruneCandidate is one eligible body recorded during the scan.
type stalePruneCandidate struct {
	idx    int // message index in history
	tokens int // estimated tokens of the original body
}

// pruneStaleToolOutput replaces older large tool-result bodies in history with
// a placeholder, protecting the recent window (the last N messages plus the
// trailing ~40K tokens). It returns the number of bodies replaced and the
// estimated tokens reclaimed. The pass is a no-op when the reclaimable total
// is below the gate. Idempotent: a second invocation replaces nothing (every
// pruned body now starts with stalePrunePrefix and is far below the minimum
// body size). Never drops messages and never touches non-tool messages.
func pruneStaleToolOutput(history []Message) (changed, reclaimed int) {
	cands := scanStaleCandidates(history)
	for _, c := range cands {
		placeholder := stalePlaceholder(history[c.idx].ToolName, c.tokens)
		if d := c.tokens - estimateTokens(placeholder); d > 0 {
			reclaimed += d
		}
	}
	if reclaimed < stalePruneGateTokens {
		return 0, 0
	}
	for _, c := range cands {
		history[c.idx].Content = stalePlaceholder(history[c.idx].ToolName, c.tokens)
		changed++
	}
	return changed, reclaimed
}

// scanStaleCandidates walks history newest-first and records the eligible
// bodies outside the protected recent window (last N messages plus the trailing
// token budget) that are ≥ the minimum body size and not already pruned.
func scanStaleCandidates(history []Message) []stalePruneCandidate {
	var cands []stalePruneCandidate
	recentTokens := 0
	for i := len(history) - 1; i >= 0; i-- {
		msg := &history[i]
		// Protect the recent window: the last N messages unconditionally, and
		// everything else while the trailing token window is not exhausted.
		if recentTokens < stalePruneProtectTokens || len(history)-i <= stalePruneProtectMessages {
			recentTokens += estimateTokens(msg.Content)
			continue
		}
		if msg.Role != ToolRole || msg.ToolName == "" || strings.HasPrefix(msg.Content, stalePrunePrefix) {
			continue
		}
		bodyTokens := estimateTokens(msg.Content)
		if bodyTokens < stalePruneMinBodyTokens {
			continue
		}
		cands = append(cands, stalePruneCandidate{idx: i, tokens: bodyTokens})
	}
	return cands
}

// stalePlaceholder renders the placeholder for a pruned body: the tool name and
// the estimated token count of the original, so the model knows what was
// dropped and can re-run the tool if needed.
func stalePlaceholder(toolName string, tokens int) string {
	return fmt.Sprintf(stalePrunePlaceholderFmt, toolName, tokens)
}
