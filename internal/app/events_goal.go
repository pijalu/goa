// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/goal"
)

type promotePauseCause int

const (
	// promotePauseCancel parks the successor after an explicit cancel.
	promotePauseCancel promotePauseCause = iota
	// promotePauseComplete parks the successor after a pause-armed completion.
	promotePauseComplete
)

// pauseReason is the terminal reason recorded on the parked goal.
func (c promotePauseCause) pauseReason() string {
	if c == promotePauseComplete {
		return "Previous goal completed with pause-after-completion armed (/goal:pause:next) — /goal:resume to start this goal"
	}
	return "Previous goal was cancelled — /goal:resume to start this goal"
}

// chatMessage renders the system chat line explaining the parked promotion.
func (c promotePauseCause) chatMessage(objective string) string {
	if c == promotePauseComplete {
		return fmt.Sprintf(
			"[goal] pause-after-completion armed (/goal:pause:next) — queued goal promoted paused (/goal:resume to start): %s",
			objective)
	}
	return fmt.Sprintf(
		"[goal] queued goal promoted paused (a cancel never auto-starts the next goal — /goal:resume to start): %s",
		objective)
}

// promoteQueuedGoalPaused promotes the queue head like promoteNextQueuedGoal
// but leaves it PAUSED and never kicks the driver: autonomous work stops
// until the user resumes explicitly (/goal:resume).
func (a *App) promoteQueuedGoalPaused(cause promotePauseCause) {
	a.promoteQueuedGoal(&cause)
}

// promoteQueuedGoal removes the head of the goal queue and activates it.
// pauseCause nil auto-starts the promoted goal; non-nil parks it PAUSED with
// the cause-specific reason and message.
func (a *App) promoteQueuedGoal(pauseCause *promotePauseCause) {
	queue, err := a.subs.goalManager.Queue.Read()
	if err != nil || len(queue) == 0 {
		a.goalCompletionHandoff = nil
		return
	}
	next := queue[0]
	_, removed, err := a.subs.goalManager.Queue.Remove(next.ID)
	if err != nil || removed == nil {
		return
	}
	// Consume the stashed completion handover (if any) into the promoted
	// goal; the queue carries criterion + verify command forward itself.
	// Explicit caller handover wins: a handover stored on the queued goal
	// (set by its creator) takes precedence over the predecessor's terminal
	// evidence; otherwise the predecessor's TerminalReason is inherited.
	handoff := a.goalCompletionHandoff
	a.goalCompletionHandoff = nil
	if removed.Handoff != nil {
		handoff = removed.Handoff
	}
	if _, err := a.subs.goalManager.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           removed.Objective,
		Name:                removed.Name,
		CompletionCriterion: removed.CompletionCriterion,
		VerifyCommand:       removed.VerifyCommand,
		FreshContext:        removed.FreshContext,
		Team:                removed.Team,
		Handoff:             handoff,
	}, goal.GoalActorUser); err != nil {
		_, _ = a.subs.goalManager.Queue.Restore(*removed)
		return
	}
	if pauseCause != nil {
		a.pausePromotedGoal(*removed, *pauseCause)
		return
	}
	a.subs.chat.AddSystemMessage(fmt.Sprintf("[goal] auto-promoted queued goal: %s", removed.Objective))
	// The promotion runs on the event-forwarder goroutine, after the clear
	// event crossed the async bus: the post-turn hook and the previous drive
	// loop both already observed "no active goal" and stood down. Kick the
	// driver here — the same way /goal create and /goal:resume do — or the
	// promoted goal would sit active with 0 turns forever. Start is a no-op
	// when a drive loop is still running (Drive dedups concurrent loops).
	if a.subs.goalDriver != nil {
		a.subs.goalDriver.Start(context.Background())
	}
}

// pausePromotedGoal parks a just-promoted queued goal in the paused state:
// the successor waits for the user to resume it explicitly. The runtime actor
// marks this as a framework pause, not a user pause. On pause failure the
// goal is restored to the queue so it is never silently lost.
func (a *App) pausePromotedGoal(removed goal.UpcomingGoal, cause promotePauseCause) {
	reason := cause.pauseReason()
	if _, err := a.subs.goalManager.Mode.PauseGoal(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorRuntime); err != nil {
		_, _ = a.subs.goalManager.Queue.Restore(removed)
		return
	}
	a.subs.chat.AddSystemMessage(cause.chatMessage(removed.Objective))
}

// showPanicError displays a rendering panic in the chat and creates an export
// so the error can be investigated. Safe to call from deferred recover().
func (a *App) showPanicError(source string, r any, stack []byte) {
	subs := a.subs

	// Show the error in the chat UI
	if subs.chat != nil {
		// Extract first 3 meaningful stack frames (skip runtime/plugin)
		stackLines := strings.Split(string(stack), "\n")
		var brief []string
		for _, sl := range stackLines {
			if strings.Contains(sl, "/github.com/pijalu/goa/") &&
				!strings.Contains(sl, "_test.go") &&
				len(brief) < 4 {
				brief = append(brief, strings.TrimSpace(sl))
			}
		}
		msg := fmt.Sprintf("⚠️  Internal %s error: %v", source, r)
		if len(brief) > 0 {
			msg += "\n  " + strings.Join(brief, "\n  ")
		}
		subs.chat.AddSystemMessage(msg)
	}

	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}

	// Create an export snapshot for debugging (async, don't block restart)
	go func() {
		issue := fmt.Sprintf("panic: %s error: %v\n\nFull stack:\n%s", source, r, string(stack))
		exportDir := filepath.Join(subs.projectDir, ".goa", "exports")
		_ = os.MkdirAll(exportDir, 0o755)
		outputPath := filepath.Join(exportDir,
			fmt.Sprintf("goa-panic-%s-%s.zip", source, time.Now().Format("20060102-150405")))

		var sessionID string
		if subs.sessionStore != nil {
			sessionID = subs.sessionStore.SessionID()
		}

		ctx := coreContextForCommand(subs, nil)
		cmd := &commands.ExportSessionCommand{
			ProjectDir:  subs.projectDir,
			Issue:       issue,
			OutputPath:  outputPath,
			SessionID:   sessionID,
			IncludeLogs: true,
		}
		if err := cmd.Run(ctx); err != nil {
			log.Printf("[events] failed to create panic export: %v", err)
		}
	}()
}
