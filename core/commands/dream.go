// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/skills"
)

// DreamCommand runs memory consolidation (dream mode).
type DreamCommand struct{}

func (c *DreamCommand) Name() string      { return "dream" }
func (c *DreamCommand) Aliases() []string { return []string{} }
func (c *DreamCommand) ShortHelp() string { return "Consolidate long-term memory files" }
func (c *DreamCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *DreamCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"apply", "apply consolidated memory immediately"},
		{"review", "review the proposed consolidated memory"},
		{"status", "show auto-dream status"},
	} {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

func (c *DreamCommand) Run(ctx core.Context, args []string) error {
	if err := validateDreamContext(ctx); err != nil {
		return err
	}

	skill, ok := loadDreamSkill(ctx.SkillRegistry)
	if !ok {
		return fmt.Errorf("dream skill not found")
	}

	if len(args) > 0 && args[0] == "status" {
		return c.showStatus(ctx)
	}

	autoApply := hasApplyFlag(args)
	reviewMode := len(args) > 0 && args[0] == "review"

	projectDir := "."
	if fi, err := os.Stat(ctx.Config.ConfigDir); err == nil && fi.IsDir() {
		projectDir = ctx.Config.ConfigDir
	}

	sessionStore := sessionStoreForDream(ctx.SessionStore)

	engine := core.NewDreamEngine(
		ctx.Config,
		ctx.ProviderManager,
		ctx.MemoryStore,
		sessionStore,
		projectDir,
		skill.Body,
	)

	if reviewMode {
		return c.runReview(ctx, engine)
	}

	result, err := engine.Run(context.Background(), autoApply)
	if err != nil {
		return fmt.Errorf("dream failed: %w", err)
	}

	if !result.Changed {
		ctx.Writef("No memories to consolidate.\n")
		return nil
	}

	ctx.Writef("Dream output written to %s\n", result.OutputPath)
	ctx.Writef("Input memories: %d | sessions: %d\n", result.InputMemories, result.InputSessions)
	if result.Consolidated {
		ctx.Writef("Consolidated memory applied.\n")
	}
	return nil
}

func (c *DreamCommand) runReview(ctx core.Context, engine *core.DreamEngine) error {
	result, err := engine.Run(context.Background(), false)
	if err != nil {
		return fmt.Errorf("dream failed: %w", err)
	}
	if !result.Changed {
		ctx.Writef("No memories to consolidate.\n")
		return nil
	}
	dd, err := engine.BuildDiff(result.OutputPath)
	if err != nil {
		return fmt.Errorf("diff failed: %w", err)
	}
	ctx.Writef("Review: %s\n", dd.HumanSummary())
	ctx.Writef("Output: %s\n", result.OutputPath)
	return nil
}

func (c *DreamCommand) showStatus(ctx core.Context) error {
	if ctx.Config == nil {
		return fmt.Errorf("config not available")
	}
	dream := ctx.Config.Memory.Dream
	ctx.Writef("Dream mode: enabled=%v auto=%v interval=%s min_sessions=%d apply=%v\n",
		dream.Enabled, dream.Auto, dream.Interval, dream.MinSessions, dream.ApplyAfterReview)
	if dream.Model != "" {
		ctx.Writef("Dream model: %s/%s\n", dream.Provider, dream.Model)
	} else {
		ctx.Writef("Dream model: (falls back to active model %s/%s)\n", ctx.Config.ActiveProvider, ctx.Config.ActiveModel)
	}
	return nil
}

func validateDreamContext(ctx core.Context) error {
	if ctx.MemoryStore == nil {
		return fmt.Errorf("memory store is not available")
	}
	if ctx.ProviderManager == nil {
		return fmt.Errorf("provider manager is not available")
	}
	if ctx.SkillRegistry == nil {
		return fmt.Errorf("skill registry is not available")
	}
	if ctx.Config == nil || !ctx.Config.Memory.Enabled {
		return fmt.Errorf("memory is disabled in configuration")
	}
	return nil
}

func loadDreamSkill(reg core.SkillRegistry) (*skills.Skill, bool) {
	if reg == nil {
		return nil, false
	}
	return reg.Get("dream")
}

func sessionStoreForDream(store core.SessionStoreAPI) *core.SessionStore {
	if store == nil {
		return nil
	}
	if s, ok := store.(*core.SessionStore); ok {
		return s
	}
	return nil
}

func hasApplyFlag(args []string) bool {
	for _, a := range args {
		if a == "apply" || a == "--apply" {
			return true
		}
	}
	return false
}
