// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// GoalRenderer renders the unified `goal` tool calls and results (bugs.md S2).
// Call headers are action-specific past-tense one-liners carrying the call's
// key argument (todo title, goal id, …); results decode the tool's per-action
// JSON shapes into compact summaries so the timeline shows WHAT the goal tool
// did — a successful todo/queue operation must never render as a bare
// "No current goal" (that string is reserved for a genuine null goal).
type GoalRenderer struct{}

// RenderCall implements tuirender.ToolRenderer.
func (r GoalRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	action := extractArg(args, "action")
	if action == "update" {
		return updateCallHeader(args)
	}
	header, ok := callHeaders[action]
	if !ok {
		return ansiBold("◆ Goal")
	}
	return withDetail(ansiBold(header), callDetail(action, args))
}

// callHeaders maps the goal action to its concise header. update is handled
// separately: its header depends on the requested lifecycle status.
var callHeaders = map[string]string{
	"create":      "◆ Started goal",
	"get":         "◆ Checked goal",
	"set_budget":  "◆ Set goal budget",
	"add_todo":    "◆ Added todo",
	"update_todo": "◆ Updated todo",
	"list":        "◆ Listed goals",
	"cancel":      "◆ Cancelled goal",
	"reorder":     "◆ Reordered goal",
}

// updateCallHeader renders the update action header, which depends on the
// requested lifecycle status; blocked/paused carry the truncated reason so
// the timeline shows WHY the goal yields.
func updateCallHeader(args map[string]any) string {
	switch status := extractArg(args, "status"); status {
	case "complete":
		return ansiBold("◆ Reported goal complete")
	case "blocked":
		return withDetail(ansiBold("◆ Reported goal blocked"), truncText(extractArg(args, "reason"), 60))
	case "paused":
		return withDetail(ansiBold("◆ Paused goal"), truncText(extractArg(args, "reason"), 60))
	case "active":
		return ansiBold("◆ Resumed goal")
	default:
		return withDetail(ansiBold("◆ Updated goal"), status)
	}
}

// callDetail extracts the call's key argument for display after the header.
func callDetail(action string, args map[string]any) string {
	switch action {
	case "create":
		return createDetail(args)
	case "set_budget":
		return budgetDetail(args)
	case "add_todo":
		return extractArg(args, "todoTitle")
	case "update_todo":
		return todoRefDetail(args)
	case "cancel":
		return extractArg(args, "goalId")
	case "reorder":
		return strings.TrimSpace(extractArg(args, "goalId") + " " + extractArg(args, "direction"))
	}
	return ""
}

// createDetail renders the objective for a single create, or the first
// objective plus a "+N more" suffix for a batch create.
func createDetail(args map[string]any) string {
	if obj := extractArg(args, "objective"); obj != "" {
		return obj
	}
	list, _ := args["objectives"].([]any)
	if len(list) == 0 {
		return ""
	}
	first, _ := list[0].(string)
	if len(list) == 1 {
		return first
	}
	return fmt.Sprintf("%s (+%d more)", first, len(list)-1)
}

// budgetDetail renders "(5 turns)" from the value/unit args.
func budgetDetail(args map[string]any) string {
	v, u := extractArg(args, "value"), extractArg(args, "unit")
	if v == "" || u == "" {
		return ""
	}
	return fmt.Sprintf("(%s %s)", v, u)
}

// todoRefDetail renders "t1 → done" from the todoId/todoStatus args.
func todoRefDetail(args map[string]any) string {
	id, status := extractArg(args, "todoId"), extractArg(args, "todoStatus")
	if id == "" {
		return status
	}
	if status == "" {
		return id
	}
	return id + " → " + status
}

// RenderResult implements tuirender.ToolRenderer.
func (r GoalRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	return renderGoalSummary(output)
}

// RenderPartial implements tuirender.StreamingRenderer so a goal call shows
// progress while its arguments are still streaming, like other tools: the
// body previews the objective(s) received so far for a create (numbered for
// batch creates) or the requested transition for an update. Returns "" until
// there is something worth showing (action/fields not yet arrived).
var _ tuirender.StreamingRenderer = GoalRenderer{}

func (r GoalRenderer) RenderPartial(args map[string]any, ctx tuirender.RenderContext) string {
	if s := partialSummary(args); s != "" {
		return ansiMuted(s)
	}
	return ""
}

// partialSummary builds the streaming-progress line for a goal call from the
// arguments received so far.
func partialSummary(args map[string]any) string {
	switch extractArg(args, "action") {
	case "create":
		return createPartial(args)
	case "update":
		status := extractArg(args, "status")
		if status == "" {
			return ""
		}
		return "reporting " + status + "…"
	}
	return ""
}

// createPartial summarizes the objectives streamed so far: the single
// objective, or a numbered list for a batch create (one line per objective
// received, so progress is visible as the array streams in).
func createPartial(args map[string]any) string {
	if obj := extractArg(args, "objective"); obj != "" {
		return obj
	}
	list, _ := args["objectives"].([]any)
	if len(list) == 0 {
		// Mid-stream the TUI's string-oriented partial-args scanner maps a
		// still-open objectives array to its first element's growing string;
		// show it as item 1 so batch creates stream too.
		if first := extractArg(args, "objectives"); first != "" {
			return "1. " + truncText(first, 80)
		}
		return ""
	}
	lines := make([]string, 0, len(list))
	for i, o := range list {
		s, _ := o.(string)
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, truncText(s, 80)))
	}
	return strings.Join(lines, "\n")
}

// PreviewLines returns the number of preview lines.
func (r GoalRenderer) PreviewLines() int { return 3 }

// HideResultWhenCollapsed returns false.
func (r GoalRenderer) HideResultWhenCollapsed() bool { return false }

// renderGoalSummary renders the goal tool result. Plain-text results (e.g.
// "Goal marked complete." followed by the verification evidence — bugs.md
// Bug A) pass through unchanged so the command, timeout and output tail stay
// visible. JSON results dispatch on their shape: each action returns a
// different payload (todo / goal / active+queued / cancelled / queued) and
// each gets its own compact summary.
func renderGoalSummary(output string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return output
	}
	if v, ok := raw["todo"]; ok {
		return renderTodoResult(v)
	}
	if v, ok := raw["cancelled"]; ok {
		return renderCancelled(v)
	}
	if _, ok := raw["active"]; ok {
		return renderGoalList(raw)
	}
	if v, ok := raw["goal"]; ok {
		return renderGoalSnapshotLine(v, raw["queued"], raw["totalQueued"], output)
	}
	if v, ok := raw["queued"]; ok {
		return renderQueue(v, raw["totalQueued"])
	}
	return output
}

// goalSummaryJSON is the subset of core/goal.GoalSnapshot the summary needs.
type goalSummaryJSON struct {
	Name        string           `json:"name"` // friendly alias, e.g. "honest.zebra"
	Objective   string           `json:"objective"`
	Status      string           `json:"status"`
	TurnsUsed   int              `json:"turnsUsed"`
	TokensUsed  int              `json:"tokensUsed"`
	WallClockMs int64            `json:"wallClockMs"`
	Todos       []todoStatusJSON `json:"todos"`
}

// summaryLabel renders the goal's short friendly name when present, falling
// back to a truncated objective — the timeline must never flood with the
// raw, unbounded objective text (mirrors goalLabel for queued goals).
func summaryLabel(g goalSummaryJSON) string {
	if g.Name != "" {
		return g.Name
	}
	return truncText(g.Objective, 40)
}

// todoStatusJSON carries only the todo status (for the done/total count).
type todoStatusJSON struct {
	Status string `json:"status"`
}

// upcomingGoalJSON is the subset of core/goal.UpcomingGoal the summary needs.
type upcomingGoalJSON struct {
	Name      string `json:"name"`
	Objective string `json:"objective"`
}

// renderTodoResult summarizes add_todo's {"todo":{...}} result.
func renderTodoResult(raw json.RawMessage) string {
	var todo struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &todo); err != nil {
		return "Todo added"
	}
	return fmt.Sprintf("Todo %s added: %s (%s)", todo.ID, todo.Title, todo.Status)
}

// renderCancelled summarizes cancel's {"cancelled":{...}} result.
func renderCancelled(raw json.RawMessage) string {
	var g upcomingGoalJSON
	if err := json.Unmarshal(raw, &g); err != nil {
		return "Goal cancelled"
	}
	if g.Name != "" {
		return "Cancelled " + g.Name + ": " + truncText(g.Objective, 50)
	}
	return "Cancelled " + truncText(g.Objective, 60)
}

// renderGoalSnapshotLine summarizes results carrying a "goal" snapshot
// (create / get / update_todo), appending todo progress and the queued count
// when the payload carries them. A JSON null goal means "No current goal".
// A fresh goal (no turns/tokens/elapsed yet) omits the all-zero stats — a
// create summary must state the goal and queue totals, not "0 turns · 0s".
func renderGoalSnapshotLine(raw, queued, totalQueued json.RawMessage, fallback string) string {
	if string(raw) == "null" {
		return "No current goal"
	}
	var g goalSummaryJSON
	if err := json.Unmarshal(raw, &g); err != nil {
		return fallback
	}
	line := fmt.Sprintf("Goal %s: %s", g.Status, summaryLabel(g))
	if g.TurnsUsed > 0 || g.TokensUsed > 0 || g.WallClockMs > 0 {
		line += fmt.Sprintf(" · %d turns · %s tokens · %s",
			g.TurnsUsed, formatTokens(g.TokensUsed), formatElapsed(g.WallClockMs))
	}
	if done, total := todoProgress(g.Todos); total > 0 {
		line += fmt.Sprintf(" · todos %d/%d", done, total)
	}
	line += queueSuffix(queued, totalQueued)
	return line
}

// queueSuffix renders the queued-count suffix for create results: " · N
// queued" for the number queued by this call, extended to "(M total)" when
// the payload reports the total queue depth after the call and it differs
// (the queue already held goals before this create).
func queueSuffix(queued, totalQueued json.RawMessage) string {
	n, ok := queuedCount(queued)
	if !ok || n <= 0 {
		return ""
	}
	if total, ok := queuedCount(totalQueued); ok && total > n {
		return fmt.Sprintf(" · %d queued (%d total)", n, total)
	}
	return fmt.Sprintf(" · %d queued", n)
}

// renderGoalList summarizes list's {"active":…, "queued":[…], "count":n}.
func renderGoalList(raw map[string]json.RawMessage) string {
	var head string
	if v, ok := raw["active"]; ok && string(v) != "null" {
		var g goalSummaryJSON
		if err := json.Unmarshal(v, &g); err == nil {
			head = fmt.Sprintf("Active: %s (%s)", summaryLabel(g), g.Status)
		}
	}
	if head == "" {
		head = "No active goal"
	}
	items := queuedItems(raw["queued"])
	if len(items) == 0 {
		return head + " · queue empty"
	}
	return fmt.Sprintf("%s · %d queued: %s", head, len(items), queueNames(items))
}

// renderQueue summarizes results that carry only "queued": a plain count
// (create while another goal is active) or the goal list itself (reorder).
// totalQueued (create only) reports the TOTAL queue depth after the call.
func renderQueue(raw, totalQueued json.RawMessage) string {
	if n, ok := queuedCount(raw); ok {
		line := "1 goal queued"
		if n != 1 {
			line = fmt.Sprintf("%d goals queued", n)
		}
		if total, ok := queuedCount(totalQueued); ok && total > n {
			line += fmt.Sprintf(" (%d total)", total)
		}
		return line
	}
	items := queuedItems(raw)
	if len(items) == 0 {
		return "Queue empty"
	}
	return fmt.Sprintf("Queue (%d): %s", len(items), queueNames(items))
}

// queuedCount decodes a "queued" payload that is a plain count (create).
func queuedCount(raw json.RawMessage) (int, bool) {
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

// queuedItems decodes a "queued" payload that is an array of upcoming goals.
func queuedItems(raw json.RawMessage) []upcomingGoalJSON {
	var items []upcomingGoalJSON
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	return items
}

// queueNames joins up to 3 queued-goal labels, collapsing the rest into a
// "+N more" suffix.
func queueNames(items []upcomingGoalJSON) string {
	labels := make([]string, 0, len(items))
	for _, g := range items {
		labels = append(labels, goalLabel(g))
	}
	if len(labels) > 3 {
		return fmt.Sprintf("%s +%d more", strings.Join(labels[:3], ", "), len(labels)-3)
	}
	return strings.Join(labels, ", ")
}

// goalLabel prefers the friendly name, falling back to a truncated objective.
func goalLabel(g upcomingGoalJSON) string {
	if g.Name != "" {
		return g.Name
	}
	return truncText(g.Objective, 40)
}

// todoProgress counts done vs total todos.
func todoProgress(todos []todoStatusJSON) (done, total int) {
	for _, td := range todos {
		if td.Status == "done" {
			done++
		}
	}
	return done, len(todos)
}

// withDetail appends a muted detail to a header, skipping empty details.
func withDetail(header, detail string) string {
	if detail == "" {
		return header
	}
	return header + " " + ansiMuted(detail)
}

// truncText truncates s to max runes, adding an ellipsis when cut.
func truncText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func extractArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	if v, ok := args[key].(float64); ok {
		return fmt.Sprintf("%g", v)
	}
	if v, ok := args[key].(bool); ok {
		return fmt.Sprintf("%t", v)
	}
	return ""
}

func ansiBold(s string) string {
	return ansi.Bold + ansi.Fg(ansiColorPrimary) + s + ansi.Reset + ansi.BoldReset
}

func ansiMuted(s string) string {
	return ansi.Faint + s + ansi.Reset
}

func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func formatElapsed(ms int64) string {
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}
