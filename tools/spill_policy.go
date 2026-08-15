// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/common"
)

// SpillPolicy is the tool-result spill policy (dsh spill-policy parity,
// gap CX2): a post-execution bound on the FINAL plain-text tool result. Any
// result over MaxInlineBytes is saved verbatim to the session-scoped
// SpillStore and replaced by a budgeted head/tail preview plus an omission
// notice whose byte cost is reserved out of the cap, so the model-facing
// replacement never exceeds MaxInlineBytes.
//
// The policy is deliberately narrow:
//   - MaxInlineBytes <= 0 disables it entirely.
//   - read results are never spilled (a read → spill → read-again loop).
//   - Storage is best-effort: no store or a failed save keeps the original
//     inline — a spill failure never hides the result.
//   - When even the notice alone would exceed the cap (a tiny cap or a long
//     locator), the original stays inline: the policy never emits a
//     replacement over the cap.
//
// The agent loop skips error results before invoking the policy, so only
// successful plain-text results reach ApplySpill.
type SpillPolicy struct {
	// MaxInlineBytes is the model-facing context cap for one plain-text tool
	// result, in bytes. Zero or negative disables the policy.
	MaxInlineBytes int
	// Store is the session-scoped spill backend. Nil keeps results inline.
	Store *common.SpillStore
}

// ApplySpill returns the model-facing content for a tool result: the original
// when the policy does not apply, or a bounded head/tail preview + notice when
// the result was spilled to the store.
func (p *SpillPolicy) ApplySpill(toolName, result string) string {
	if p == nil || p.MaxInlineBytes <= 0 || p.Store == nil {
		return result
	}
	if toolName == "read" {
		return result
	}
	if len(result) <= p.MaxInlineBytes {
		return result
	}
	path, err := p.Store.Save(toolName+".txt", result)
	if err != nil {
		return result
	}
	return p.buildReplacement(toolName, result, path)
}

// spillNotice renders the omission notice for a saved result.
func spillNotice(omitted int, path string) string {
	return fmt.Sprintf("(Omitted %d bytes. Full result stored at: %s. Use read with offset/limit, or grep this path to search within it.)",
		omitted, path)
}

// buildReplacement composes a category-aware preview + "\n\n" + notice within
// the cap. The preview uses agentic.SemanticBudgetResult so the spill keeps the
// semantically relevant middle (test failures, per-file search matches) instead
// of a uniform head/tail cut; the notice cost is reserved out of the budget
// (priced at the worst-case omission count — the full byte total — so the real
// notice is never longer than reserved). When no within-cap replacement exists,
// the original is returned.
func (p *SpillPolicy) buildReplacement(toolName, result, path string) string {
	limit := p.MaxInlineBytes
	reserve := len(spillNotice(len(result), path)) + 2 // "\n\n" join
	budget := limit - reserve
	if budget < 0 {
		budget = 0
	}
	preview := agentic.SemanticBudgetResult(toolName, result, budget)
	omitted := len(result) - len(preview)
	notice := spillNotice(omitted, path)
	replaced := notice
	if preview != "" {
		replaced = preview + "\n\n" + notice
	}
	if len(replaced) > limit {
		return result
	}
	return replaced
}

// cutStringHead keeps the first maxBytes of s, backing off to a rune boundary
// so the result is always valid UTF-8.
func cutStringHead(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && s[end]&0xC0 == 0x80 {
		end--
	}
	return s[:end]
}

// truncateStringTail keeps the last maxBytes of s, advancing to a rune
// boundary so the result is always valid UTF-8.
func truncateStringTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && s[start]&0xC0 == 0x80 {
		start++
	}
	return s[start:]
}
