// SPDX-License-Identifier: GPL-3.0-or-later
package goal

import (
	"fmt"
	"strings"
	"time"

	coregoal "github.com/pijalu/goa/core/goal"
)

func formatOpenTodosReminder(snap *coregoal.GoalSnapshot) string {
	if snap == nil {
		return ""
	}
	var open []string
	for _, todo := range snap.Todos {
		if todo.Status != coregoal.TodoDone {
			open = append(open, todo.Title)
		}
	}
	if len(open) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nReminder: %d todo(s) were still open when the goal completed:", len(open))
	for _, title := range open {
		fmt.Fprintf(&b, "\n- %s", title)
	}
	b.WriteString("\nTodos do not escape the goal — if any of this work is still needed, create a follow-up goal for it now.")
	return b.String()
}
func formatVerifyEvidence(v *coregoal.VerifyEvidence) string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nVerification passed in %s (timeout %s):\n$ %s", formatMillis(v.DurationMs), formatMillis(v.TimeoutMs), v.Command)
	if out := strings.TrimRight(v.Output, "\n"); out != "" {
		b.WriteByte('\n')
		b.WriteString(strings.Join(tailLines(out, 10), "\n"))
	}
	return b.String()
}
func formatMillis(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Truncate(100 * time.Millisecond).String()
}
func tailLines(s string, n int) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
func optionalReason(args goalArgs) *string { return optionalText(args.Reason) }
func optionalText(s string) *string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
func optionalExpectation(args goalArgs) *string { return optionalText(args.Expectation) }
