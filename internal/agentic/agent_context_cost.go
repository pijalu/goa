// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"encoding/json"
)

// fixedCostTokens estimates the per-turn token cost that is present in EVERY
// request regardless of conversation length: the system prompt plus the
// serialized tool schemas. These fixed costs are counted by the provider
// against the context window but were previously excluded from the usage
// estimate, so the proactive compression threshold (and the displayed usage
// percent) systematically underestimated real usage — causing compaction to
// fire a turn too late, after a large tool result had already blown past 100%.
//
// The tool-schema component is computed once and cached: the registry's
// schemas are stable for the agent's lifetime (Schemas() is itself cached and
// sorted for prompt-cache stability). The system-prompt component is cheap and
// recomputed each call (it can carry a small, variable goal reminder).
func (a *Agent) fixedCostTokens() int {
	system := 0
	if a.cfg.SystemPrompt != "" {
		system = estimateTokens(a.cfg.SystemPrompt)
	}
	return system + a.toolSchemaCost()
}

// toolSchemaCost returns the cached token estimate of all registered tool
// schemas. Nil-safe and computed at most once per agent.
func (a *Agent) toolSchemaCost() int {
	a.toolSchemaTokensOnce.Do(func() {
		if a.reg == nil {
			return
		}
		var total int
		for _, s := range a.reg.Schemas() {
			total += estimateTokens(s.Name)
			total += estimateTokens(s.Description)
			if len(s.Schema) > 0 {
				if b, err := json.Marshal(s.Schema); err == nil {
					total += estimateTokens(string(b))
				}
			}
		}
		a.toolSchemaTokens = total
	})
	return a.toolSchemaTokens
}
