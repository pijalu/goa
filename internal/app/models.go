// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import "strings"

// modelDisplay formats the model name for the status bar. If the provider
// is set and the model name doesn't already include it, show "(provider) model".
func modelDisplay(providerID, modelName string) string {
	if providerID == "" || strings.Contains(modelName, providerID+"/") {
		return modelName
	}
	return "(" + providerID + ") " + modelName
}

// activeModelName resolves the model config ID to the real API model name.
func activeModelName(subs *subsystems) string {
	modelName := subs.cfg.ActiveModel
	if subs.providerMgr != nil {
		if pc, _ := subs.providerMgr.Active(); pc != nil {
			if resolved := subs.providerMgr.ResolveModelName(*pc, modelName); resolved != "" {
				modelName = resolved
			}
		}
	}
	return modelName
}

// activeModelDisplay returns the full status bar model string with provider and real model name.
func activeModelDisplay(subs *subsystems) string {
	modelName := activeModelName(subs)
	providerID := subs.cfg.ActiveProvider
	if subs.providerMgr != nil {
		if pc, _ := subs.providerMgr.Active(); pc != nil {
			providerID = pc.ID
		}
	}
	return modelDisplay(providerID, modelName)
}

func mainThinkingLevel(subs *subsystems) string {
	if subs.agentMgr != nil {
		if lvl := subs.agentMgr.GetThinkingLevel(); lvl != "" {
			return lvl
		}
	}
	return string(subs.cfg.GetThinkingLevel("main_agent"))
}

func companionThinkingLevel(subs *subsystems) string {
	return string(subs.cfg.GetThinkingLevel("companion"))
}

// teamFooterInfo returns the footer team badge fields: the effective team
// name (goal overlay wins, TEAMS.md §5.2) and the drift marker. Empty when
// no team is active.
func teamFooterInfo(subs *subsystems) (name string, drifted bool) {
	if subs == nil || subs.teamManager == nil {
		return "", false
	}
	return subs.teamManager.EffectiveTeam(), subs.teamManager.Drifted()
}

// companionModelDisplay returns the formatted companion model string.
// The companion carries its OWN provider binding (MultiAgent.CompanionProvider),
// not the active provider: resolving/prefixing against the active provider
// rendered "(opencode-go) glm-5.2" for a zai-bound companion (bugs.md Bug B).
// Configs without a companion provider keep the legacy active-provider display.
func companionModelDisplay(subs *subsystems) string {
	modelID := subs.cfg.MultiAgent.CompanionModel
	if modelID == "" {
		return ""
	}
	providerID := subs.cfg.MultiAgent.CompanionProvider
	if providerID == "" {
		providerID = subs.cfg.ActiveProvider
	}
	resolved := modelID
	if subs.providerMgr != nil {
		if pc := subs.cfg.GetProviderByID(providerID); pc != nil {
			if r := subs.providerMgr.ResolveModelName(*pc, modelID); r != "" {
				resolved = r
			}
		}
	}
	return modelDisplay(providerID, resolved)
}
