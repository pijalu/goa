// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"fmt"

	"github.com/pijalu/goa/core/goal"
)

// maxConsecutiveUnblockSpawns caps auto-unblock spawns with no goal
// completion or explicit resume in between. The kind guard makes the
// investigation chain terminal; this cap covers the remaining cycle where a
// STANDARD goal re-blocks on the same external blocker after each user
// resume. Two strikes, then the flow stops and asks the user.
const maxConsecutiveUnblockSpawns = 2

// unblockCompletionCriterion is the done-condition recorded on every
// auto-spawned investigation goal (review finding F2): without it the
// done-gate is unarmed and an investigation can "complete" on prose advice
// that no execution goal and no resumed goal ever receives. Var (not const):
// the spawn path stores a pointer to it in the goal input.
var unblockCompletionCriterion = "An execution goal implementing the identified solution has been created (goal action \"create\", priority \"front\") and its objective is cited in the completion reason — OR the investigation proved no autonomous solution exists and the goal was blocked with reason + expectation instead. Completing on prose advice without a created follow-up goal is invalid."

// Excerpt caps for the auto-unblock composition. The goal reminder injects
// only ExcerptObjectiveLen (400) runes of the objective, so a longer
// composition is pure storage weight — and an oversized objective breaks the
// re-queue path later: queue inserts enforce MaxObjectiveLength while
// mode.CreateGoal deliberately does not (internal transitions), so a goal
// stored oversized can never be re-queued (review finding F4).
const (
	unblockExcerptObjective = 1200
	unblockExcerptField     = 700
)

// enqueueUnblockGoal demotes the just-blocked standard goal A back onto the
// front of the queue, then activates an "unblocking" investigation goal U in
// front of it. U's objective embeds A's blocker and the
// investigate→execute-or-block contract; its recorded completion criterion
// arms the done-gate so prose-only completions are challenged. A's re-queue
// carries the block context (reason + expectation) as its handover, so the
// promotion reminder tells the resumed turn why the goal stopped — the old
// code claimed this rode along in the objective but passed the objective
// unchanged (review finding F3).
// Returns (true, output, nil) on a successful spawn; (false, "", nil) when
// the spawn is intentionally skipped (no queue, auto-unblock disabled, or
// spawn cap hit); (false, "", err) when a spawn step failed.
func (t *GoalTool) enqueueUnblockGoal(blocked goal.GoalSnapshot, reason, expectation string) (bool, string, error) {
	if t.Queue == nil || (t.AutoUnblock != nil && !t.AutoUnblock()) {
		return false, "", nil
	}
	if t.unblockSpawns >= maxConsecutiveUnblockSpawns {
		return false, "", nil
	}
	// Re-queue A at the FRONT so it resumes right after the unblocking goal
	// (or the execution goal the investigation creates) completes.
	requeue := blocked.Objective
	if requeue == "" {
		requeue = "(resume blocked goal)"
	}
	if _, err := t.Queue.PrependGoal(goal.UpcomingGoalInput{
		Objective:           requeue,
		CompletionCriterion: blocked.CompletionCriterion,
		VerifyCommand:       blocked.VerifyCommand,
		Handoff:             buildBlockHandoff(reason, expectation),
	}); err != nil {
		return false, "", fmt.Errorf("re-queue the blocked goal: %w", err)
	}
	// Clear A from active so U can start. Runtime actor: this is a framework
	// transition, not a user cancellation.
	if _, err := t.Mode.CancelGoal(goal.GoalActorRuntime); err != nil {
		return false, "", fmt.Errorf("clear the blocked goal: %w", err)
	}
	// Activate the unblocking investigation goal, marked as such (kind) and
	// armed with its completion criterion (done-gate).
	uObjective := buildUnblockObjective(requeue, reason, expectation)
	if _, err := t.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           uObjective,
		CompletionCriterion: &unblockCompletionCriterion,
		Kind:                goal.GoalKindUnblock,
	}, goal.GoalActorRuntime); err != nil {
		return false, "", fmt.Errorf("activate the unblocking investigation goal: %w", err)
	}
	t.unblockSpawns++
	return true, fmt.Sprintf("Goal blocked: %s. An unblocking goal was started to investigate solutions before asking you for input.", reason), nil
}

// buildBlockHandoff composes the handover stored with the re-queued blocked
// goal. On promotion it is injected into the resumed goal's reminder as an
// <untrusted_handover> block, so the resumed turn knows why the goal stopped
// and what it was waiting for. Inputs are model-supplied → treated as
// untrusted data and excerpted.
func buildBlockHandoff(reason, expectation string) *string {
	text := fmt.Sprintf("This goal was auto-blocked (a framework investigation goal looked for an autonomous solution). Blocker: %s. Waiting for: %s. On resume, first check whether the waiting-for condition is now satisfied before retrying.",
		goal.Excerpt(reason, unblockExcerptField), goal.Excerpt(expectation, unblockExcerptField))
	return &text
}

// buildUnblockObjective composes the investigation goal's objective. It
// forces the model to search for a way forward and encodes the two allowed
// outcomes: (1) solution found → create an execution goal with priority
// "front" and complete this goal citing it; (2) no solution → block THIS
// goal with reason + expectation to ask the user for guidance (which never
// spawns a successor investigation — see updateBlocked). All embedded inputs
// are excerpted: the composition must stay re-queueable under
// MaxObjectiveLength and the reminder only surfaces 400 runes anyway.
func buildUnblockObjective(blockedObjective, reason, expectation string) string {
	return fmt.Sprintf(`UNBLOCKING INVESTIGATION — find a solution for a blocked goal.

The goal "%s" was blocked because: %s
It was waiting for: %s

Your ONLY job is to determine whether this blocker can be solved without user input:
1. INVESTIGATE the blocker. Read code, run commands, search, experiment — do real work to find a concrete path forward.
2. If you find a viable solution: create a new EXECUTION goal that implements it, using goal action "create" with priority "front" (so it runs before the blocked goal resumes), then mark THIS investigation goal complete, citing the created goal's objective as completion evidence.
3. ONLY if no solution is possible without the user: mark THIS goal blocked with "reason" (why it cannot be solved autonomously) and "expectation" (exactly what you need from the user). Do NOT block prematurely — exhaust autonomous options first. A blocked investigation is final: the framework reports it to the user instead of spawning another investigation.`,
		goal.Excerpt(blockedObjective, unblockExcerptObjective), goal.Excerpt(reason, unblockExcerptField), goal.Excerpt(expectation, unblockExcerptField))
}
