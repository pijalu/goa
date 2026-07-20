// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"sort"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
)

// handleOpenModeSelector opens the major-mode selector, equivalent to /mode.
func (a *App) handleOpenModeSelector() {
	subs := a.subs
	if subs.cmdRouter == nil {
		return
	}
	result := subs.cmdRouter.Parse("/mode")
	if result == nil || result.Command == nil {
		return
	}
	ctx := coreContextForCommand(subs, a)
	_, _ = subs.cmdRouter.Execute(ctx, result)
}

// handleChangeMode cycles the active major mode forward.
func (a *App) handleChangeMode() {
	subs := a.subs
	if subs.agentMgr == nil || subs.modeRegistry == nil {
		return
	}
	current := subs.agentMgr.CurrentMode()
	majors := sortedMajors(subs.modeRegistry)
	if len(majors) == 0 {
		return
	}
	next := nextInCycle(string(current.Major), majors)
	if next == "" {
		return
	}
	newMode := subs.modeRegistry.DefaultForMajor(internal.MajorMode(next))
	if newMode.Major == "" {
		return
	}
	subs.agentMgr.SetMode(newMode)
}

func sortedMajors(reg *core.ModeRegistry) []string {
	var majors []string
	for _, m := range reg.Majors() {
		majors = append(majors, string(m))
	}
	sort.Strings(majors)
	return majors
}

func nextInCycle(current string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	idx := 0
	for i, v := range values {
		if v == current {
			idx = i + 1
			break
		}
	}
	if idx >= len(values) {
		idx = 0
	}
	return values[idx]
}

// handleCycleThinkingLevel cycles the main-agent thinking level forward and
// flashes the new value.
func (a *App) handleCycleThinkingLevel() {
	subs := a.subs
	if subs.agentMgr == nil {
		return
	}
	current := subs.agentMgr.GetThinkingLevel()
	if current == "" {
		current = string(subs.cfg.GetThinkingLevel("main_agent"))
	}
	next := nextThinkingLevel(current)
	if err := subs.agentMgr.SetThinkingLevel(next); err != nil {
		a.flash("Failed to set thinking level: " + err.Error())
		return
	}
	a.flash(fmt.Sprintf("Thinking level: %s", next))
}

func nextThinkingLevel(current string) string {
	levels := internal.AllThinkingLevels()
	idx := 0
	for i, l := range levels {
		if string(l) == current {
			idx = i + 1
			break
		}
	}
	if idx >= len(levels) {
		idx = 0
	}
	return string(levels[idx])
}

// handleCycleAutonomy cycles the autonomy level forward and persists it to
// the project config, flashing the new value.
func (a *App) handleCycleAutonomy() {
	subs := a.subs
	if subs.agentMgr == nil {
		return
	}
	current := subs.agentMgr.CurrentMode()
	if current.Major == "" {
		current.Major = internal.MajorCoder
	}
	next := nextAutonomy(current.Autonomy)
	if next == "" {
		return
	}
	subs.agentMgr.SetMode(current.WithAutonomy(next))
	if subs.cfg != nil {
		subs.cfg.Mode.Defaults = ensureModeDefaults(subs.cfg)
		subs.cfg.Mode.Defaults[current.Major] = next
		if err := saveProjectConfig(subs.cfg, subs.loader); err != nil {
			a.flash("Failed to save mode: " + err.Error())
			return
		}
	}
}

var autonomyCycle = []internal.AutonomyLevel{
	internal.AutonomyYolo,
	internal.AutonomySolo,
	internal.AutonomyConfirm,
	internal.AutonomyReview,
}

func nextAutonomy(current internal.AutonomyLevel) internal.AutonomyLevel {
	for i, a := range autonomyCycle {
		if a == current {
			return autonomyCycle[(i+1)%len(autonomyCycle)]
		}
	}
	return internal.AutonomySolo
}

func ensureModeDefaults(cfg *config.Config) map[internal.MajorMode]internal.AutonomyLevel {
	if cfg.Mode.Defaults == nil {
		return make(map[internal.MajorMode]internal.AutonomyLevel)
	}
	return cfg.Mode.Defaults
}

func saveProjectConfig(cfg *config.Config, saver config.ConfigSaver) error {
	if saver == nil {
		return nil
	}
	return saver.SaveProjectConfig(cfg)
}

// handleChangeModel opens the model selector by executing the /model command,
// so the picker gets the full sentinel handling of runModelCommand ('+' add,
// '-' delete with confirmation, custom model) instead of a parallel
// sentinel-blind implementation.
func (a *App) handleChangeModel() {
	subs := a.subs
	if subs.cmdRouter == nil {
		return
	}
	result := subs.cmdRouter.Parse("/model")
	if result == nil || result.Command == nil {
		return
	}
	ctx := coreContextForCommand(subs, a)
	_, _ = subs.cmdRouter.Execute(ctx, result)
}

// handleToggleThinkingBlocks toggles the thinking-blocks collapsed state and
// persists the change to the home config.
func (a *App) handleToggleThinkingBlocks() {
	subs := a.subs
	if subs.cfg == nil {
		return
	}
	next := !subs.cfg.TUI.Transparency.ThinkingCollapsed
	subs.cfg.TUI.Transparency.ThinkingCollapsed = next
	if err := saveHomeField(subs.loader, []string{"tui", "transparency", "thinking_collapsed"}, next); err != nil {
		a.flash("Failed to save thinking blocks setting: " + err.Error())
		return
	}
	label := "expanded"
	if next {
		label = "collapsed"
	}
	a.flash("Thinking blocks: " + label)
}

func saveHomeField(saver config.ConfigSaver, path []string, value any) error {
	if saver == nil {
		return nil
	}
	return saver.SaveHomeField(path, value)
}

func (a *App) flash(text string) {
	subs := a.subs
	if subs.chat != nil {
		subs.chat.AddFlashMessage("⚡ " + text)
	}
	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}
}
