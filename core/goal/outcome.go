// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import "strings"

// BuildCompletionSummary builds the text injected as a system reminder after
// the model marks a goal complete.
func BuildCompletionSummary(snapshot GoalSnapshot) string {
	var b strings.Builder
	b.WriteString(buildCompletionMessage(snapshot))
	b.WriteString("\n\n")
	b.WriteString("Write a concise final message for the user. State that the goal is complete, summarize the main work completed, and mention any validation you ran. Do not call more goal tools.")
	return b.String()
}

// BuildBlockedReasonPrompt builds the text injected as a system reminder after
// the model marks a goal blocked.
func BuildBlockedReasonPrompt(snapshot GoalSnapshot) string {
	var b strings.Builder
	b.WriteString(buildBlockedMessage(snapshot))
	b.WriteString("\n\n")
	b.WriteString("Write a concise final message for the user. State that the goal is blocked, explain the concrete blocker, and say what input or change is needed before work can continue. Do not call more goal tools.")
	return b.String()
}

// BuildCancellationReminder builds the cancellation text injected when the user
// cancels a goal.
func BuildCancellationReminder() string {
	return "The user cancelled the current goal. Ignore earlier active-goal reminders for that goal. Handle the next user request normally unless the user starts or resumes a goal."
}

// BuildForkClearedReminder builds the reminder injected when a session forks
// and the fork does not inherit the source goal.
func BuildForkClearedReminder() string {
	return "This fork does not have a current goal. Ignore earlier active-goal reminders from the source session. Handle requests normally unless the user starts a new goal."
}

func buildCompletionMessage(snapshot GoalSnapshot) string {
	var b strings.Builder
	b.WriteString("Goal completed successfully")
	if snapshot.TerminalReason != nil {
		b.WriteString(": ")
		b.WriteString(*snapshot.TerminalReason)
	}
	b.WriteString(".\n")
	b.WriteString("Worked ")
	b.WriteString(Pluralize(snapshot.TurnsUsed, "turn", "turns"))
	b.WriteString(" over ")
	b.WriteString(FormatElapsed(snapshot.WallClockMs))
	b.WriteString(", using ")
	b.WriteString(FormatTokens(snapshot.TokensUsed))
	b.WriteString(" tokens.")
	return b.String()
}

func buildBlockedMessage(snapshot GoalSnapshot) string {
	var b strings.Builder
	b.WriteString("Goal blocked.\n")
	b.WriteString("Worked ")
	b.WriteString(Pluralize(snapshot.TurnsUsed, "turn", "turns"))
	b.WriteString(" over ")
	b.WriteString(FormatElapsed(snapshot.WallClockMs))
	b.WriteString(", using ")
	b.WriteString(FormatTokens(snapshot.TokensUsed))
	b.WriteString(" tokens.")
	return b.String()
}

// Pluralize returns "1 item" or "N items" based on count.
func Pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return FormatInt(n) + " " + plural
}
