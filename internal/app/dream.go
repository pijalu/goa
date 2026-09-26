// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/skills"
)

// runDream executes a memory consolidation run and exits on failure. It is the
// thin CLI shell around executeDream, which holds the logic and returns errors so
// the diagnostics (e.g. the "dream is disabled — enable it" message) are testable
// without os.Exit killing the test binary.
func runDream(subs *subsystems, opts RuntimeOptions) {
	if err := executeDream(subs, opts); err != nil {
		fmt.Fprintf(os.Stderr, "Dream failed: %v\n", err)
		os.Exit(1)
	}
}

// executeDream performs one memory consolidation run and reports its outcome on
// stdout. It returns an error for a missing prerequisite or a disabled dream
// skill; the caller decides how to surface it.
func executeDream(subs *subsystems, opts RuntimeOptions) error {
	if err := validateDreamPrerequisites(subs); err != nil {
		return err
	}

	skill, ok := loadDreamSkill(subs)
	if !ok {
		return errors.New(commands.DreamSkillDisabledMessage)
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
		return err
	}

	if !result.Changed {
		fmt.Println("No memories to consolidate.")
		return nil
	}

	fmt.Printf("Dream output written to %s\n", result.OutputPath)
	fmt.Printf("Input memories: %d | sessions: %d\n", result.InputMemories, result.InputSessions)
	if result.Consolidated {
		fmt.Println("Consolidated memory applied.")
	}
	return nil
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
