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

// sessionProviderID returns the provider selected by the live provider manager.
// Config.ActiveProvider is the startup/default value and can be stale after a
// model or provider switch, so footer data must use this session value.
func sessionProviderID(subs *subsystems) string {
	if subs == nil {
		return ""
	}
	if subs.providerMgr != nil {
		if pc, _ := subs.providerMgr.Active(); pc != nil && pc.ID != "" {
			return pc.ID
		}
	}
	if subs.cfg != nil {
		return subs.cfg.ActiveProvider
	}
	return ""
}

// activeModelName resolves the model config ID to the real API model name.
func activeModelName(subs *subsystems) string {
	if subs == nil || subs.cfg == nil {
		return ""
	}
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
	return modelDisplay(sessionProviderID(subs), activeModelName(subs))
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
func companionModelDisplay(subs *subsystems) string {
	modelID := subs.cfg.MultiAgent.CompanionModel
	if modelID == "" {
		return ""
	}
	providerID := subs.cfg.MultiAgent.CompanionProvider
	if providerID == "" {
		providerID = sessionProviderID(subs)
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
