// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// setToolCallingFooter updates the footer for a tool call in progress.
// The model spinner is suppressed because the model is not generating; the
// chat status spinner shows the tool's progress.
func (a *App) setToolCallingFooter(label string) {
	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Activity:               "tool calling",
		MainActivity:           "",
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(false)
}

// setBashTitle updates the terminal title when a bash command is invoked.
func (a *App) setBashTitle(toolName, toolInput string) {
	if toolName != "bash" || a.subs.tuiEngine == nil {
		return
	}
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(toolInput), &params); err == nil && params.Command != "" {
		cmd := params.Command
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		a.subs.tuiEngine.SetTitle(titleBrand + " - $ " + cmd)
	}
}

// toolCallProgressLabel returns "Tool calling (X/Y)" when we know how many
// calls are in the current batch and how many have been seen so far.
func (a *App) toolCallProgressLabel() string {
	total := 0
	seen := 0
	if a.subs.agentMgr != nil {
		if agent := a.subs.agentMgr.CurrentAgent(); agent != nil {
			total = agent.BufferedToolCallCount()
		}
	}
	a.statsMu.Lock()
	seen = a.toolResultsSeen
	a.statsMu.Unlock()

	if total > 1 {
		return fmt.Sprintf("Tool calling (%d/%d)", min(seen+1, total), total)
	}
	if total == 1 {
		return "Tool calling (1/1)"
	}
	return "Tool calling"
}

// clearToolBusy resets the status after a tool result.
func (a *App) clearToolBusy() {
	// After a tool result the harness sends the updated context back to the
	// LLM. Show "Sending request..." so the UI does not prematurely report
	// "Answering..." while the model is still being prepared.
	a.subs.statusMsg.Show("Sending request...")
	a.subs.footer.SetModelBusy(false)
}

// toolStatusFromResult maps a tool result text to a widget status.
func (a *App) toolStatusFromResult(text string) tui.ToolStatus {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, agentic.ToolBudgetResultPrefix) {
		return tui.ToolError
	}
	if strings.HasPrefix(trimmed, "Error:") {
		return tui.ToolError
	}
	return tui.ToolSuccess
}

// maxToolCallLevel returns the maximum of two ToolCallLevel values.
func maxToolCallLevel(a, b ToolCallLevel) ToolCallLevel {
	if a > b {
		return a
	}
	return b
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
