// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

// ForModel returns a copy of the snapshot with GoalID removed.
// The model never needs the goal ID — there's only ever one goal,
// and no tool accepts a goal ID as input.
func ForModel(snapshot GoalSnapshot) GoalSnapshot {
	snapshot.GoalID = ""
	return snapshot
}

// Context-injection size bounds.
//
// A goal's Objective/CompletionCriterion/Handoff/TerminalReason are
// user/agent-provided and unbounded at write time. When they are embedded in
// the per-turn [goal] reminder or serialized into a `goal list` tool result
// they enter the model's context on a content-prefix cache (Z.AI/DeepSeek) —
// an over-long field both bloats the context and enlarges the divergent
// suffix the provider recomputes each turn (a cache miss re-bills the whole
// uncached tail). These caps keep each injected excerpt small; the full text
// always remains retrievable via the goal store / `goal get`. Values are
// chosen so a normal goal (a sentence to a short paragraph) is unaffected.
const (
	// ExcerptObjectiveLen bounds the objective excerpt in reminders and list
	// summaries. Generous enough for a one-line objective plus context.
	ExcerptObjectiveLen = 400
	// ExcerptFieldLen bounds secondary free-text fields (criterion, verify
	// command, handover, terminal reason/expectation).
	ExcerptFieldLen = 280
)

// Excerpt returns s truncated to at most max runes, appending an ellipsis
// when truncation occurred. It is safe on UTF-8 (rune-based) and never
// mutates its input. Used to bound untrusted goal text before it is injected
// into the model's context, so a pathologically long field cannot blow up the
// per-turn reminder or a list tool result. Small values pass through
// unchanged (behavior-preserving for normal goals).
func Excerpt(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// ResultForModel returns a GoalToolResult with GoalID stripped.
func ResultForModel(result GoalToolResult) GoalToolResult {
	if result.Goal == nil {
		return result
	}
	stripped := ForModel(*result.Goal)
	return GoalToolResult{Goal: &stripped}
}

// TodoSummary is the compact form of a GoalTodoItem for list output: the
// model only needs the status roll-up and a bounded title, not the full
// per-item metadata.
type TodoSummary struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// GoalSummary is the compact form of a GoalSnapshot for `goal list`: it keeps
// identity, status, counters, a bounded objective excerpt, and a todo roll-up
// while dropping the large free-text fields (completion criterion, verify
// command, handover, terminal reason/expectation) that made a full list
// result balloon (a 33-goal list reached ~50KB / ~12.5k tokens). The full
// detail remains available via `goal get` on a single goal.
type GoalSummary struct {
	Name        string        `json:"name,omitempty"`
	Status      GoalStatus    `json:"status"`
	Objective   string        `json:"objective"` // excerpt, bounded
	TurnsUsed   int           `json:"turnsUsed"`
	TokensUsed  int           `json:"tokensUsed"`
	WallClockMs int64         `json:"wallClockMs"`
	Todos       []TodoSummary `json:"todos,omitempty"`
}

// UpcomingGoalSummary is the compact form of an UpcomingGoal for `goal list`.
type UpcomingGoalSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Objective string `json:"objective"` // excerpt, bounded
}

// SummarizeSnapshot converts a full GoalSnapshot to its compact list form,
// bounding the objective and each todo title. Behavior-preserving for normal
// goals (fields below the excerpt caps pass through unchanged).
func SummarizeSnapshot(s GoalSnapshot) GoalSummary {
	out := GoalSummary{
		Name:        s.Name,
		Status:      s.Status,
		Objective:   Excerpt(s.Objective, ExcerptObjectiveLen),
		TurnsUsed:   s.TurnsUsed,
		TokensUsed:  s.TokensUsed,
		WallClockMs: s.WallClockMs,
	}
	if len(s.Todos) > 0 {
		out.Todos = make([]TodoSummary, len(s.Todos))
		for i, td := range s.Todos {
			out.Todos[i] = TodoSummary{ID: td.ID, Title: Excerpt(td.Title, ExcerptFieldLen), Status: td.Status}
		}
	}
	return out
}

// SummarizeUpcoming converts a full UpcomingGoal to its compact list form.
func SummarizeUpcoming(g UpcomingGoal) UpcomingGoalSummary {
	return UpcomingGoalSummary{
		ID:        g.ID,
		Name:      g.Name,
		Objective: Excerpt(g.Objective, ExcerptObjectiveLen),
	}
}
