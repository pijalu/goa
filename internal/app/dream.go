// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"os"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/skills"
)

// runDream executes a memory consolidation run and exits.
func runDream(subs *subsystems, opts RuntimeOptions) {
	if err := validateDreamPrerequisites(subs); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	skill, ok := loadDreamSkill(subs)
	if !ok {
		fmt.Fprintln(os.Stderr, "Error: dream skill not found")
		os.Exit(1)
	}

	ctx := context.Background()
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	engine := core.NewDreamEngine(
		subs.cfg,
		subs.providerMgr,
		subs.memStore,
		subs.sessionStore,
		subs.projectDir,
		skill.Body,
	)

	result, err := engine.Run(ctx, opts.DreamApply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Dream failed: %v\n", err)
		os.Exit(1)
	}

	if !result.Changed {
		fmt.Println("No memories to consolidate.")
		return
	}

	fmt.Printf("Dream output written to %s\n", result.OutputPath)
	fmt.Printf("Input memories: %d | sessions: %d\n", result.InputMemories, result.InputSessions)
	if result.Consolidated {
		fmt.Println("Consolidated memory applied.")
	}
}

func validateDreamPrerequisites(subs *subsystems) error {
	if subs.memStore == nil {
		return fmt.Errorf("memory store is not available")
	}
	if !subs.cfg.Memory.Enabled {
		return fmt.Errorf("memory is disabled in configuration")
	}
	return nil
}

func loadDreamSkill(subs *subsystems) (*skills.Skill, bool) {
	if subs.skillRegistry != nil {
		if s, ok := subs.skillRegistry.Get("dream"); ok {
			return s, true
		}
	}
	return nil, false
}
