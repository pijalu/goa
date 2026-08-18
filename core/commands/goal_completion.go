// SPDX-License-Identifier: GPL-3.0-or-later
package commands

import (
	"strings"

	"github.com/pijalu/goa/core"
)

var goalSubcommands = []struct{ value, desc string }{{"new", "create a new goal"}, {"next", "queue a goal to run after the current one"}, {"replace", "replace the current goal"}, {"manage", "open the queued-goals manager"}, {"reorder", "reorder queue with letter mapping"}, {"status", "show current goal status"}, {"list", "list active + queued goals with full objectives"}, {"log", "show recent goal event records"}, {"verify", "run the recorded verify command now"}, {"settings", "toggle goal settings (auto-unblock)"}, {"pause", "pause the active goal (:next to pause after completion)"}, {"resume", "resume a paused goal (or start the first queued goal)"}, {"cancel", "discard the current goal (next queued promotes paused)"}}
var goalCancelScopes = []struct{ value, desc string }{{"current", "discard the current goal (queued goals stay)"}, {"all", "discard the current goal and clear the queue"}}
var goalPauseScopes = []struct{ value, desc string }{{"current", "pause the active goal now"}, {"next", "pause after the active goal completes (successor promotes paused)"}, {"next:off", "disarm the pause-after-completion one-shot"}}
var goalNextOptions = []struct{ value, desc string }{{"first", "queue at the front — runs right after the active goal (default)"}, {"last", "queue at the end — runs after all queued goals"}, {"fresh", "queue on a clean context"}, {"reuse", "queue reusing the current conversation"}}

func splitGoalCompletionPrefix(p string) (string, string, bool) {
	i := strings.Index(p, ":")
	if i < 0 {
		return p, "", false
	}
	return p[:i], p[i+1:], true
}
func completion(rest, prefix string, items []struct{ value, desc string }) []core.ArgCompletion {
	var out []core.ArgCompletion
	for _, x := range items {
		if rest == "" || strings.HasPrefix(x.value, rest) {
			out = append(out, core.ArgCompletion{Value: prefix + x.value, Description: x.desc})
		}
	}
	return out
}
func cancelScopeCompletions(r string) []core.ArgCompletion {
	return completion(r, "cancel:", goalCancelScopes)
}
func pauseScopeCompletions(r string) []core.ArgCompletion {
	return completion(r, "pause:", goalPauseScopes)
}
func nextOptionCompletions(r string) []core.ArgCompletion {
	return completion(r, "next:", goalNextOptions)
}
func (c *GoalCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if sub, rest, n := splitGoalCompletionPrefix(prefix); n {
		switch sub {
		case "cancel":
			return cancelScopeCompletions(rest)
		case "pause":
			return pauseScopeCompletions(rest)
		case "next":
			return nextOptionCompletions(rest)
		}
	}
	var out []core.ArgCompletion
	for _, s := range goalSubcommands {
		if prefix == "" || strings.HasPrefix(s.value, prefix) {
			out = append(out, core.ArgCompletion{Value: s.value, Description: s.desc})
		}
	}
	return out
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
