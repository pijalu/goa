// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

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
// The model ID comes from the LIVE config (providerMgr's current config, hot
// reloads applied, session picks intact) — never the static boot cfg, which
// goes stale the moment the config watcher swaps in a reloaded profile.
func activeModelName(subs *subsystems) string {
	if subs == nil {
		return ""
	}
	cfg := subs.liveConfig()
	if cfg == nil {
		return ""
	}
	modelName := cfg.ActiveModel
	if subs.providerMgr != nil {
		if pc, _ := subs.providerMgr.Active(); pc != nil {
			if resolved := subs.providerMgr.ResolveModelName(*pc, modelName); resolved != "" {
				modelName = resolved
			}
		}
	}
	return modelName
}

// activeModelDisplay returns the full status bar model string with provider
// and real model name.
//
// Source-of-truth order — the status line must show what the session USES,
// not merely what was selected last:
//
//  1. The model bound to the running session's active agent: exactly what the
//     next turn sends. Its provider profile label is recovered from the bound
//     BaseURL, so a divergent selection (e.g. a hot reload carrying another
//     instance's persisted default) never surfaces as a mixed pair.
//  2. Fallback when no model is bound (early boot / no agent): the
//     selection-derived pair, as before.
func activeModelDisplay(subs *subsystems) string {
	if mdl, ok := boundSessionModel(subs); ok {
		display := mdl.Name
		if display == "" {
			display = mdl.ID
		}
		return modelDisplay(boundProviderID(subs, mdl), display)
	}
	return modelDisplay(sessionProviderID(subs), activeModelName(subs))
}

// boundSessionModel returns the model bound to the running session's active
// agent — the one the NEXT turn will actually use. ok is false when there is
// no manager or no bound model (early boot, bare managers).
func boundSessionModel(subs *subsystems) (agenticprovider.Model, bool) {
	if subs == nil || subs.agentMgr == nil {
		return agenticprovider.Model{}, false
	}
	mdl := subs.agentMgr.ActiveModel()
	if mdl.ID == "" && mdl.Name == "" {
		return agenticprovider.Model{}, false
	}
	return mdl, true
}

// boundProviderID resolves the provider-profile label for a bound session
// model: exact endpoint match first, then the live selection ONLY when it
// agrees on the resolved model name (so the pair can never mix a stale model
// with a freshly switched provider). Returns "" when neither holds, which
// renders the model without a provider label — no label beats a wrong one.
func boundProviderID(subs *subsystems, mdl agenticprovider.Model) string {
	name := mdl.Name
	if name == "" {
		name = mdl.ID
	}
	if id := providerIDForBoundModel(subs.cfg, mdl.BaseURL); id != "" {
		return id
	}
	if sel := sessionProviderID(subs); sel != "" && activeModelName(subs) == name {
		return sel
	}
	return ""
}

// providerIDForBoundModel maps a bound session model back to its provider
// PROFILE (the user-facing config ID like "kimi-code") by endpoint. The bound
// model carries the resolved BaseURL, which for chat-completions APIs is the
// profile endpoint plus a "/chat/completions" suffix; matching strips that
// suffix from both sides so the same profile matches for either API flavor.
// Returns "" when cfg or baseURL is empty, or no profile matches.
func providerIDForBoundModel(cfg *config.Config, baseURL string) string {
	if cfg == nil || baseURL == "" {
		return ""
	}
	bound := comparableEndpoint(baseURL)
	for _, pc := range cfg.Providers {
		if pc.Endpoint != "" && comparableEndpoint(pc.Endpoint) == bound {
			return pc.ID
		}
	}
	return ""
}

// comparableEndpoint normalizes an endpoint for equality matching: trim
// whitespace and trailing slashes, then drop a trailing "/chat/completions"
// (the resolved BaseURL appends it; raw profile endpoints do not).
func comparableEndpoint(ep string) string {
	ep = strings.TrimRight(strings.TrimSpace(ep), "/")
	return strings.TrimSuffix(ep, "/chat/completions")
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
