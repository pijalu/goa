// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

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

// Pluralize returns "1 item" or "N items" based on count.
func Pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return FormatInt(n) + " " + plural
}
