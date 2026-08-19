// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	_ "embed"
	"strings"

	"github.com/pijalu/goa/internal/embeddoc"
)

// compactSummaryOpenTag and compactSummaryCloseTag wrap the structured
// summary inside the landed checkpoint (CX3, dsh compaction-basic). The open
// tag also marks a checkpoint to LATER compaction passes: the summarize
// instruction (compaction_instruction.md) tells the model to consolidate any
// prior <compacted-summary> block instead of copying it forward verbatim.
const (
	compactSummaryOpenTag  = "<compacted-summary>"
	compactSummaryCloseTag = "</compacted-summary>"
)

// compactSummaryInstructionRaw is the embedded 8-section checkpoint contract.
//
//go:embed compaction_instruction.md
var compactSummaryInstructionRaw string

// compactSummaryInstruction is the final user message of the summarize
// request (CX3): the 8-section Markdown checkpoint contract ported verbatim
// from dsh compaction-basic summarizer.ts COMPACTION_INSTRUCTION. It rides
// AFTER the replayed conversation prefix (system prompt + tools + history)
// rather than replacing the system prompt, so on prefix-caching providers
// (DeepSeek context caching, Anthropic, ...) the auxiliary call is a strict
// append of the last conversation request and the whole conversation prefix
// is served from the warm cache instead of being re-billed at full input
// price on the largest history of the session. The contract names the
// <compacted-summary> tag so a history already carrying a prior checkpoint is
// consolidated, not duplicated — this is how later compactions "feed the
// previous checkpoint into the summarize input": the landed checkpoint is a
// history message, so it replays into the next summarize verbatim.
//
// The SPDX HTML comment is stripped once at init so it never consumes LLM
// context (Goa embedded-prompt convention).
var compactSummaryInstruction = strings.TrimSpace(
	string(embeddoc.StripHTMLComments([]byte(compactSummaryInstructionRaw))))

// compactSummaryPreamble frames the replacement user turn in compacted
// history as established context (CX3, dsh compaction-basic
// CHECKPOINT_PREAMBLE). Paired with the wrapped assistant checkpoint reply,
// it keeps the role sequence valid for strict providers (DeepSeek, some
// OpenAI deployments) that reject an assistant-first history, and ends on an
// assistant turn so the next user input alternates correctly.
const compactSummaryPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

// frameCompactedSummary wraps a raw model summary in the durable checkpoint
// framing (dsh compaction-basic frameSummary): preamble + <compacted-summary>
// + summary + </compacted-summary>.
func frameCompactedSummary(summary string) string {
	return compactSummaryPreamble + "\n\n" + compactSummaryOpenTag + "\n" + summary + "\n" + compactSummaryCloseTag
}

// elidedToolCallArguments is the in-history marker written by elideToolMessages
// into the arguments of elided assistant tool calls. It is NOT valid JSON and
// must never reach a provider: migrateMessage converts elided calls to a
// plain-text note during provider-bound serialization (models imitate
// the placeholder as call arguments, and strict providers 400 requests whose
// function.arguments are not valid JSON — which broke /compress:summarize).
const elidedToolCallArguments = "[elided]"

// elidedToolResultContent is the placeholder written by elideToolMessages in
// place of elided tool result bodies.
const elidedToolResultContent = "[tool result elided]"

func (a *Agent) Compact(ctx context.Context) error {
	return a.compactOrdered(ctx, a.compactSlotStrategy())
}

// compactSlotStrategy resolves the strategy slot a full-window compaction
// escalates to: the hard-layer strategy (default summarize). The legacy
// whole-config strategy already maps onto hard at config build time, so this
// is the single escalation point for the main compaction path.
func (a *Agent) compactSlotStrategy() CompressionStrategy {
	return a.cfg.ContextCompression.resolveThresholds().hardStrategy
}

// compactOrdered runs a full-window compaction for the given escalation slot
// with the formal Phase 2b.3 strategy ordering:
//
//	remote_compact (capability + operator gate) → fresh_window (configured:
//	gate on, or this slot named it) → local ladder (prune → micro →
//	selective → summarize) → emergency (message-drop ceiling, in the
//	reactive paths).
//
// The prune pass runs AHEAD of the transaction: when it resolves the context
// pressure on its own, no compaction of any kind is needed and no strategy
// fires (the pruned history stays as-is).
func (a *Agent) compactOrdered(ctx context.Context, slot CompressionStrategy) error {
	a.mu.Lock()
	empty := len(a.history) == 0
	before := a.computeContextStats()
	a.mu.Unlock()
	if empty {
		return nil
	}

	// CX1: pre-compaction tool-result pruning runs AHEAD of summarize range
	// selection (the micro dry-run below and summarizeHistory itself are the
	// range consumers). It rewrites over-budget historical tool results in
	// place to a bounded head + PruneMarker + tail — model-free, so when the
	// re-measured estimate drops under the escalation level we skip the
	// summarize LLM call entirely (the pruned history stays as-is).
	skip, pruneBefore, pruneRes := a.pruneToolResultsPreCompact()
	if skip {
		a.emitCompactionResult("tool_result_pruning", pruneBefore, pruneRes, "pruning resolved context pressure; summarize skipped")
		a.emitContextStats()
		return nil
	}

	// Micro compaction is an OPT-IN maintenance step, disabled by default so
	// summarize is always the default compaction path on a full window.
	//
	// Micro must NEVER fire as a first pass. When enabled it is run FIRST only
	// as a DRY-RUN (estimation only, no mutation) to validate whether it could
	// meet the required shrink — but the dry-run never mutates history and never
	// short-circuits the summarize. The summarize below ALWAYS runs on the
	// ORIGINAL, untouched history so the provider prefix cache is preserved.
	//
	// ONLY if that summarize fails with a context-overflow error (the input to
	// summarize was itself too big for the window) do we apply micro compaction
	// for real — to create enough room — then retry summarize on the shrunk
	// input. That summarize-overflow path is the single place micro mutates,
	// and it runs REGARDLESS of MicroCompaction.Enabled: it is the escape
	// hatch for a summarize that cannot fit the window, not an opt-in
	// maintenance pass.
	if a.cfg.ContextCompression.MicroCompaction.Enabled {
		a.microDryRunValidate()
	}

	// CX4: open the durable compaction transaction. The triple
	// (compaction_start → compaction_summary → compaction_end) shares one id
	// and lands in the session JSONL via the observer pipeline, so a crash
	// mid-compaction leaves a start with no end — detectable on next boot
	// (FindOrphanedCompactions). The transaction opens only once a summarize
	// is genuinely attempted: the prune-resolved early return above is a
	// model-free pass (no summary exists to record) and never starts one.
	txID := newCompactionTxID()
	a.emitCompactionTxStart(txID)

	// Phase 2b.3 ordering: remote_compact (capability + gate) → fresh_window
	// (configured) → the local summarize ladder below. Both preferred
	// strategies complete the transaction's start → summary → end triple on
	// success so observers see the standard compaction lifecycle.
	if handled, err := a.tryPreferredCompaction(ctx, txID, slot); handled {
		return err
	}

	summary, usage, err := a.summarizeHistory(ctx)
	if err != nil && isContextLengthError(err) {
		// Summarize self-overflowed: the history to summarize was too big for
		// the window. Now — and only now — apply micro compaction to create
		// enough space, then retry summarize on the reduced input.
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Compact: summarize overflowed (%v); applying micro compaction to make room and retrying", err)
		}
		a.applyMicroForSummarize()
		// Guarantee the retry fits (bug-2: summarize must always fit the
		// window): micro only truncates tool payloads, so a chat-heavy history
		// can still overflow. When the estimated summarize request is still
		// oversized, drop the oldest messages (chain-safe) until it fits.
		a.shrinkHistoryForSummarizeRetry()
		summary, usage, err = a.summarizeHistory(ctx)
	}
	if err != nil {
		// Failed attempt: close the transaction with the error so the log
		// distinguishes a failed compaction (start → end{error}) from a crash
		// (start with no end).
		a.emitCompactionTxEnd(txID, err.Error())
		return err
	}

	// Guarantee the landed summary fits (bug-2, output side): a verbose model
	// can return a summary large enough to keep the window over the ceiling,
	// which would re-trigger compression (or the destructive fallback) on the
	// very next turn. Cap the summary to the available history budget before
	// it replaces the conversation.
	summary = a.fitSummaryToWindow(summary)

	// Replace history with a valid, cache-stable role sequence. The system
	// prompt is NOT stored here: buildProviderContext sends it via
	// Context.SystemPrompt, so storing it would duplicate it on the next turn.
	// Previously Compact stored [system, assistant] (assistant-first after the
	// index-0 system skip, rejected by strict providers) and obliterated the
	// provider's prompt cache by wholesale prefix replacement.
	a.mu.Lock()
	removed := len(a.history) - 2
	if removed < 0 {
		removed = 0
	}
	shadowedEnd := len(a.history)
	a.history = []Message{
		{Type: Content, Role: User, Content: frameCompactedSummary(summary)},
		{Type: Content, Role: Assistant, Content: summary},
	}
	a.cacheGeneration++
	// History was replaced wholesale: the recorded provider prompt is stale.
	a.invalidateContextUsageLocked()
	a.mu.Unlock()

	// CX4: record the completed summary (shadowed range, freed tokens,
	// provider/model, summarize-call usage), then close the transaction. The
	// summary event fires after the replacement so its freed-tokens figure is
	// measured against the landed checkpoint, and always before end so the
	// triple keeps its start → summary → end order.
	after := a.ContextStats()
	a.emitCompactionTxSummary(&CompactionTx{
		ID:             txID,
		ShadowedStart:  0,
		ShadowedEnd:    shadowedEnd,
		ShadowedCount:  shadowedEnd,
		ShadowedTokens: before.EstimatedTokens,
		FreedTokens:    before.EstimatedTokens - after.EstimatedTokens,
		Provider:       string(a.cfg.Model.Provider),
		Model:          a.cfg.Model.ID,
		Usage:          usage,
	})
	a.emitCompactionTxEnd(txID, "")

	// Compact keeps its internal emission (public API contract +
	// TestAgent_CompactEmitsCompactEvent), enriched with the structured
	// payload. The full summary rides in Compaction.Detail; Text carries the
	// strategy label like every other compression path.
	a.emitCompaction(string(CompressionSummarize), before, after, removed, 0, summary)
	return nil
}

// tryPreferredCompaction runs the Phase 2b.3 preferred strategies — the ones
// that beat the local summarize ladder — in their formal order. handled=true
// means a strategy ran (or definitively failed after running) and the caller
// must return its result; handled=false means neither strategy was selected
// and the local ladder proceeds.
//
// Order:
//
//  1. remote_compact: when the operator gate and the provider capability
//     both allow, the server-side /responses/compact strategy runs. On
//     failure it returns a distinct errRemoteCompactFailed with history
//     untouched, and we fall THROUGH to fresh_window / the local ladder.
//  2. fresh_window: when configured (the gate is on, or this escalation slot
//     named it), the zero-LLM window reset runs — the cheapest full
//     compaction, still completing the transaction triple.
func (a *Agent) tryPreferredCompaction(ctx context.Context, txID string, slot CompressionStrategy) (handled bool, err error) {
	if a.remoteCompactionAvailable() {
		applied, rerr := a.compactRemote(ctx, txID)
		if applied {
			return true, rerr
		}
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Compact: remote compaction unavailable/failed (%v); falling back", rerr)
		}
	}
	if a.freshWindowSelected(slot) {
		return true, a.compactFreshWindow(txID)
	}
	return false, nil
}

// microDryRunValidate runs the micro-compaction dry-run (estimation only — it
// never mutates history) and logs whether micro could meet the required shrink
// on its own. This is purely diagnostic: it validates micro's feasibility up
// front WITHOUT letting micro become the first-pass action. The summarize that
// follows always runs on the original history; micro is only ever applied for
// real on the summarize-overflow fallback (applyMicroForSummarize).
func (a *Agent) microDryRunValidate() {
	cfg := a.cfg.ContextCompression.MicroCompaction
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 || a.cfg.Logger == nil {
		return
	}
	// The required shrink target: micro would need to bring estimated usage
	// under the escalation level (effectiveHard−5), the same headroom the
	// reactive paths reserve so the next request goes out with margin.
	target := maxTokens * a.cfg.ContextCompression.resolveThresholds().effectiveHard() / 100

	a.mu.Lock()
	before := a.computeContextStats()
	changed, freed := a.microCompactionDryRun(cfg, true)
	meets := changed > 0 && before.EstimatedTokens-freed <= target
	a.mu.Unlock()

	if meets {
		a.cfg.Logger.Log(Info, "Compact: micro dry-run COULD meet required shrink (would free ~%d tokens to reach %d); still summarizing original history to preserve cache", freed, target)
	} else {
		a.cfg.Logger.Log(Info, "Compact: micro dry-run cannot meet required shrink (would free ~%d tokens, need to reach %d); summarizing original history", freed, target)
	}
}

// applyMicroForSummarize applies micro compaction unconditionally (the
// summarize request just overflowed, so we need room regardless of any gate)
// and emits the resulting EventCompact. This is the ONLY path on which micro
// compaction mutates history during Compact — a last resort after summarize
// itself failed with a context-overflow error. It runs regardless of
// MicroCompaction.Enabled (the summarize-overflow escape hatch), so the
// truncation settings are defaulted field-wise: SDK callers that never
// configured micro still get a sane pass instead of wiping old tool results
// with an empty marker.
func (a *Agent) applyMicroForSummarize() {
	cfg := microFallbackConfig(a.cfg.ContextCompression.MicroCompaction)
	a.mu.Lock()
	before := a.computeContextStats()
	res := a.applyMicroLocked(cfg)
	a.mu.Unlock()
	a.emitCompactionResult(string(CompressionMicro), before, res, "summarize overflow fallback")
}

// summarizeInstructionTokens estimates the token cost the compaction
// instruction adds to the summarize request (the final user message).
func summarizeInstructionTokens() int { return estimateTokens(compactSummaryInstruction) }

// summarizeInputBudget is the token budget the summarize REQUEST input must
// fit: the full window minus the fixed per-turn cost (system prompt + tool
// schemas, already inside the estimate) minus a reserve for the summary
// output. Summarizing a ~95%-full window otherwise requests more than the
// window can hold — the summarize itself is then rejected (context length),
// which is exactly the "summarize did not fit the window" failure.
func (a *Agent) summarizeInputBudget() int {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		return 0
	}
	// Reserve room for the summary output and the instruction. The output
	// reserve is a fraction of the window (summaries are short relative to
	// the conversation), bounded below so a tiny window still leaves output
	// room and above so a huge window never reserves an absurd amount.
	reserve := maxTokens / 8 // 12.5%
	if reserve < 2048 {
		reserve = 2048
	}
	if reserve > 32768 {
		reserve = 32768
	}
	budget := maxTokens - a.fixedCostTokens() - summarizeInstructionTokens() - reserve
	if budget < 0 {
		budget = 0
	}
	return budget
}

// shrinkHistoryForSummarizeRetry drops the oldest messages (chain-safe) until
// the estimated summarize request fits the window's input budget. It runs
// only on the summarize-overflow retry path, after micro truncation proved
// insufficient — the guarantee that the retried summarize always fits the
// window (bug-2). Emits a selective-style EventCompact for the cut.
func (a *Agent) shrinkHistoryForSummarizeRetry() {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		return
	}
	budget := a.summarizeInputBudget()
	a.mu.Lock()
	before := a.computeContextStats()
	res := a.dropOldestToFitLocked(budget)
	a.mu.Unlock()
	if res.removed > 0 {
		a.emitCompactionResult(string(CompressionSelective), before, res, "shrinking summarize input to fit the window")
	}
}

// dropOldestToFitLocked drops the oldest non-system messages until the
// history's estimated tokens fit the given budget (chain-safe: never leaves a
// leading tool result orphaned). Returns the work record. The caller must
// hold a.mu.
func (a *Agent) dropOldestToFitLocked(budget int) compactionResult {
	if len(a.history) <= 1 {
		return compactionResult{}
	}
	tok := make([]int, len(a.history))
	total := 0
	for i := range a.history {
		tok[i] = messageTokenCount(&a.history[i])
		total += tok[i]
	}
	if total <= budget {
		return compactionResult{}
	}
	// Find the smallest front cut keeping system + tail within budget.
	cut := len(a.history)
	dropped := 0
	system := tok[0]
	for k := 1; k < len(a.history); k++ {
		if system+(total-dropped) <= budget {
			cut = k
			break
		}
		dropped += tok[k]
	}
	// Advance past orphaned tool results (strict providers reject a leading
	// tool message with no preceding tool_calls).
	for cut < len(a.history) && a.history[cut].Role == ToolRole {
		dropped += tok[cut]
		cut++
	}
	a.history = append(a.history[:1:1], a.history[cut:]...)
	a.cacheGeneration++
	a.invalidateContextUsageLocked()
	return compactionResult{removed: cut - 1, freedTokens: dropped}
}

// summaryTruncationNote marks a summary that was capped to fit the window.
const summaryTruncationNote = "\n\n[summary truncated to fit the context window]"

// fitSummaryToWindow caps the produced summary so the compacted history (the
// [summary-request, summary] pair) fits the history budget under the hard
// ceiling. The pair stores the summary TWICE (the framed request message wraps
// the summary in the preamble + tags, and the assistant reply carries it
// verbatim), plus per-message overhead — so the budget each copy must fit is
// roughly half the history budget. The cap is measured against the real token
// estimator (not a chars→tokens guess) and trimmed iteratively, so the landed
// pair is guaranteed under the ceiling. This prevents the next turn from
// re-firing compression — or the destructive fallback — on a just-compacted
// conversation (bug-2, output side).
func (a *Agent) fitSummaryToWindow(summary string) string {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		return summary
	}
	hard := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	// Budget for the summary-bearing history (the pair), excluding fixed cost.
	budget := maxTokens*hard/100 - a.fixedCostTokens()
	if budget < 0 {
		budget = 0
	}

	// pairTokens measures the landed pair's real token cost for a given
	// summary body: the framed user request (preamble + tagged summary) plus
	// the assistant reply, with per-message overhead.
	pairTokens := func(body string) int {
		req := Message{Type: Content, Role: User, Content: frameCompactedSummary(body)}
		rep := Message{Type: Content, Role: Assistant, Content: body}
		return messageTokenCount(&req) + messageTokenCount(&rep)
	}

	if pairTokens(summary) <= budget {
		return summary
	}

	// Iteratively trim on a rune boundary until the measured pair fits. Start
	// from a chars-per-token estimate of the target and halve the overage each
	// pass — converges in a handful of iterations without a rune-at-a-time
	// walk. Always appends the truncation note so the model (and the user) can
	// see the summary was cut.
	runes := []rune(summary)
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Warn, "summarize produced an oversized summary (%d tokens est. for the pair); truncating to fit the window budget %d", pairTokens(summary), budget)
	}
	// Initial guess: budget tokens ≈ budget*3.3 chars, but the pair doubles
	// the body, so start near half. Clamp to the current length.
	guess := budget * 33 / 10 / 2
	if guess > len(runes) {
		guess = len(runes)
	}
	for n := guess; n >= 0; {
		candidate := string(runes[:n]) + summaryTruncationNote
		if pairTokens(candidate) <= budget {
			return candidate
		}
		// Shrink by 10% of the current length each miss (fast convergence).
		next := n - (n/10 + 1)
		if next < 0 {
			next = 0
		}
		if next == n {
			break
		}
		n = next
	}
	// Pathological: even the note alone exceeds the budget (window smaller
	// than the fixed cost). Return the note only — never an unbounded summary.
	return summaryTruncationNote
}

// applyMicroLocked performs the real in-place micro compaction pass under a.mu
// (forced: bypasses the ratio/cache gates — Compact already decided to shrink).
// Returns the work record for the caller's EventCompact. The caller must hold
// a.mu.
