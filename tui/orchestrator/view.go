// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"fmt"
	"strconv"
)

// AgentTabKind classifies a tab in the multi-agent view.
type AgentTabKind int

const (
	// TabConversation is the default chat-visible tab. Selecting it hides the
	// stats panel and lets the normal chat viewport render the orchestrator
	// conversation via agent-scoped streaming blocks.
	TabConversation AgentTabKind = iota
	// TabStats is the aggregate stats table tab (always present).
	TabStats
	// TabAgent is a per-agent filter tab. Selecting it keeps the chat visible
	// but filters it to that one agent's blocks (thinking/content/tool),
	// restoring the per-agent view without duplicating streaming widgets.
	TabAgent
)

// AgentTab is one selectable tab. Agent tabs are keyed by AgentID but labeled
// by Role (stable, human-readable). Stats/All use fixed keys ("stats"/"all").
type AgentTab struct {
	Key   string
	Label string
	Kind  AgentTabKind
}

// AgentEnhancedRow is one stats-table row carrying the full column set
// requested by the tabbed-run UI: provider/model/thinking plus cache-hit.
// Label is the display name (role, disambiguated when a role recurs).
type AgentEnhancedRow struct {
	AgentID   string
	Role      string
	Label     string
	Provider  string
	Model     string
	Thinking  string
	Status    string
	Turns     int
	TokensIn  int
	TokensOut int
	CacheRead int
	ToolCalls int
}

// AgentLogLineKind classifies a transcript line for faint-vs-normal styling.
type AgentLogLineKind int

const (
	// LogContent is normal streamed assistant text.
	LogContent AgentLogLineKind = iota
	// LogThinking is reasoning text (rendered faint).
	LogThinking
	// LogMarker is a non-content annotation ([steer]/[finished]).
	LogMarker
)

// AgentLogLine is one line of an agent's transcript.
type AgentLogLine struct {
	Kind AgentLogLineKind
	Text string
}

// AgentLog is the transcript buffer for a single agent.
type AgentLog struct {
	AgentID string
	Role    string
	lines   []AgentLogLine
}

// Lines returns a copy of the transcript lines.
func (l *AgentLog) Lines() []AgentLogLine {
	if l == nil {
		return nil
	}
	out := make([]AgentLogLine, len(l.lines))
	copy(out, l.lines)
	return out
}

// MultiAgentView is the mutable state for the persistent multi-agent run
// view. It is source-agnostic: any multi-agent source (orchestrator runtime,
// foreground orchestrator, pipeline, swarm) feeds it neutral AgentViewEvents.
//
// ALL mutators run on the TUI commandLoop (the R1 single-owner invariant): the
// forwarder goroutine translates source events into AgentViewEvents and applies
// them via App.apply, so the view is only ever touched by the loop. The two
// render-only components (AgentContent, AgentTabBar) hold a pointer and read
// via the accessor methods below — never mutating.
//
// Tabs are reduced to Conversation (default) and Stats. The conversation
// itself renders in the normal chat viewport via the agent stream registry;
// this view only owns the stats table.
type MultiAgentView struct {
	source   string
	meta     map[string]string
	finished bool
	failed   bool

	tabs   []AgentTab
	active int

	rows      []AgentEnhancedRow
	logs      map[string]*AgentLog
	order     []string // agentIDs in first-seen order (stable tabs + "All")
	roleCount map[string]int
}

// NewMultiAgentView returns an empty view tagged with the given source label
// (e.g. "orchestration"). The source label prefixes the tab bar so the same
// component reads correctly for pipeline/swarm sources later.
func NewMultiAgentView(source string) *MultiAgentView {
	return &MultiAgentView{source: source, logs: map[string]*AgentLog{}}
}

// Source returns the source label (e.g. "orchestration").
func (v *MultiAgentView) Source() string { return v.source }

// Active reports whether the view has an attached run with tabs (i.e. it should
// be rendered). Returns false when no source has started, so the content/tabbar
// components return nil and the chat viewport renders normally.
func (v *MultiAgentView) Active() bool { return len(v.tabs) > 0 }

// Meta returns the display-only source metadata (objective/topology/name).
func (v *MultiAgentView) Meta() map[string]string {
	out := make(map[string]string, len(v.meta))
	for k, val := range v.meta {
		out[k] = val
	}
	return out
}

// MetaValue returns a single metadata value (empty if unset).
func (v *MultiAgentView) MetaValue(key string) string { return v.meta[key] }

// Finished reports whether the source has completed.
func (v *MultiAgentView) Finished() bool { return v.finished }

// Failed reports whether the source completed with failure.
func (v *MultiAgentView) Failed() bool { return v.failed }

// ApplyEvent applies one neutral event, mutating view state. Must be called on
// the command loop. The switch delegates to small helpers so each stays well
// under the complexity budget.
func (v *MultiAgentView) ApplyEvent(ev AgentViewEvent) {
	switch ev.Kind {
	case EvSourceStarted:
		v.handleSourceStarted(ev)
	case EvSourceFinished:
		v.handleSourceFinished(ev)
	case EvAgentStarted:
		v.handleAgentStarted(ev)
	case EvAgentStats:
		v.handleAgentStats(ev)
	case EvAgentSteered:
		v.handleAgentSteered(ev)
	case EvAgentFinished:
		v.handleAgentFinished(ev)
	}
}

func (v *MultiAgentView) handleSourceStarted(ev AgentViewEvent) {
	if v.meta == nil {
		v.meta = map[string]string{}
	}
	for k, val := range ev.Meta {
		v.meta[k] = val
	}
	v.ensureBookendTabs()
}

func (v *MultiAgentView) handleSourceFinished(ev AgentViewEvent) {
	v.finished = true
	if ev.Status == "failed" {
		v.failed = true
	}
}

func (v *MultiAgentView) handleAgentStarted(ev AgentViewEvent) {
	v.ensureBookendTabs()
	v.upsertRow(ev)
	if ev.AgentID != "" {
		v.order = append(v.order, ev.AgentID)
		v.ensureLog(ev.AgentID, ev.Role)
		label := v.DisambiguateLabel(ev.Role)
		v.setRowLabel(ev.AgentID, label)
		v.ensureAgentTab(ev.AgentID, label)
	}
}

// ensureAgentTab appends a per-agent filter tab for agentID (labelled with the
// disambiguated role) the first time the agent is seen. Tabs stay ordered
// [Conversation, Stats, <agent>…]. Idempotent per agentID.
func (v *MultiAgentView) ensureAgentTab(agentID, label string) {
	for _, t := range v.tabs {
		if t.Key == agentID {
			return
		}
	}
	v.tabs = append(v.tabs, AgentTab{Key: agentID, Label: label, Kind: TabAgent})
}

func (v *MultiAgentView) setRowLabel(agentID, label string) {
	for i := range v.rows {
		if v.rows[i].AgentID == agentID {
			v.rows[i].Label = label
			return
		}
	}
}

// disambiguateLabel returns a stable display label for a role, appending a
// ·N suffix when the same role recurs (e.g. hub delegating to "coder" twice
// yields "coder" then "coder·2") so tabs and rows stay distinguishable.
// It is exported so the agent stream registry can reuse the same rule.
func (v *MultiAgentView) DisambiguateLabel(role string) string {
	if v.roleCount == nil {
		v.roleCount = map[string]int{}
	}
	v.roleCount[role]++
	if v.roleCount[role] == 1 {
		return role
	}
	return fmt.Sprintf("%s·%d", role, v.roleCount[role])
}

func (v *MultiAgentView) handleAgentStats(ev AgentViewEvent) {
	if ev.Stats == nil {
		return
	}
	v.upsertRow(ev)
}

func (v *MultiAgentView) handleAgentSteered(ev AgentViewEvent) {
	v.ensureLog(ev.AgentID, ev.Role)
	text := ev.Text
	if text != "" {
		text = " " + text
	}
	v.appendLine(ev.AgentID, AgentLogLine{Kind: LogMarker, Text: "[steer]" + text})
}

func (v *MultiAgentView) handleAgentFinished(ev AgentViewEvent) {
	status := ev.Status
	if status == "" {
		status = "finished"
	}
	v.upsertRow(AgentViewEvent{
		Kind: EvAgentFinished, AgentID: ev.AgentID, Role: ev.Role, Status: status,
	})
	v.ensureLog(ev.AgentID, ev.Role)
	v.appendLine(ev.AgentID, AgentLogLine{Kind: LogMarker, Text: "[finished]"})
}

// ensureBookendTabs guarantees the Conversation (index 0) and Stats (index 1)
// tabs exist. Conversation is selected by default so the chat is visible.
func (v *MultiAgentView) ensureBookendTabs() {
	if len(v.tabs) > 0 {
		return
	}
	v.tabs = []AgentTab{
		{Key: "conversation", Label: "Conversation", Kind: TabConversation},
		{Key: "stats", Label: "Stats", Kind: TabStats},
	}
	v.active = 0
}

// upsertRow merges a partial event into the row for ev.AgentID, creating it on
// first sight. Non-zero scalar fields overwrite; Stats counters are absolute.
func (v *MultiAgentView) upsertRow(ev AgentViewEvent) {
	for i := range v.rows {
		if v.rows[i].AgentID == ev.AgentID {
			v.applyRowEv(&v.rows[i], ev)
			return
		}
	}
	v.rows = append(v.rows, AgentEnhancedRow{AgentID: ev.AgentID})
	v.applyRowEv(&v.rows[len(v.rows)-1], ev)
}

// applyRowEv writes the non-zero/present fields of ev onto row.
func (v *MultiAgentView) applyRowEv(row *AgentEnhancedRow, ev AgentViewEvent) {
	if ev.Role != "" {
		row.Role = ev.Role
	}
	if ev.Provider != "" {
		row.Provider = ev.Provider
	}
	if ev.Model != "" {
		row.Model = ev.Model
	}
	if ev.Thinking != "" {
		row.Thinking = ev.Thinking
	}
	if ev.Status != "" {
		row.Status = ev.Status
	}
	if ev.Stats != nil {
		row.Turns = ev.Stats.Turns
		row.TokensIn = ev.Stats.TokensIn
		row.TokensOut = ev.Stats.TokensOut
		row.CacheRead = ev.Stats.CacheRead
		row.ToolCalls = ev.Stats.ToolCalls
	}
}

func (v *MultiAgentView) ensureLog(agentID, role string) {
	if v.logs[agentID] != nil {
		return
	}
	v.logs[agentID] = &AgentLog{AgentID: agentID, Role: role}
}

func (v *MultiAgentView) appendLine(agentID string, line AgentLogLine) {
	l := v.logs[agentID]
	if l == nil {
		return
	}
	l.lines = append(l.lines, line)
}

// --- read API for the render-only components (no mutation) ------------------

// Tabs returns a copy of the tab list.
func (v *MultiAgentView) Tabs() []AgentTab {
	out := make([]AgentTab, len(v.tabs))
	copy(out, v.tabs)
	return out
}

// ActiveIndex returns the active tab index (0-based), or 0 if there are none.
func (v *MultiAgentView) ActiveIndex() int {
	if v.active < 0 || v.active >= len(v.tabs) {
		return 0
	}
	return v.active
}

// ActiveTab returns the active tab. The bool is false when there are no tabs.
func (v *MultiAgentView) ActiveTab() (AgentTab, bool) {
	if v.active < 0 || v.active >= len(v.tabs) {
		return AgentTab{}, false
	}
	return v.tabs[v.active], true
}

// ActiveAgentID returns the AgentID steering should target for the active
// tab: the tab's own agent for a per-agent tab, the most recently started
// agent for the Conversation tab, or "" on Stats (broadcast to all).
func (v *MultiAgentView) ActiveAgentID() string {
	tab, ok := v.ActiveTab()
	if !ok {
		return ""
	}
	switch tab.Kind {
	case TabAgent:
		return tab.Key
	case TabConversation:
		if len(v.order) == 0 {
			return ""
		}
		return v.order[len(v.order)-1]
	default:
		return ""
	}
}

// SelectByKey selects the tab whose Key matches sel, or the 1-based numeric
// index in sel. Returns false (without changing selection) when not found.
func (v *MultiAgentView) SelectByKey(sel string) bool {
	if idx, err := strconv.Atoi(sel); err == nil {
		if idx >= 1 && idx <= len(v.tabs) {
			v.active = idx - 1
			return true
		}
		return false
	}
	for i, t := range v.tabs {
		if t.Key == sel {
			v.active = i
			return true
		}
	}
	return false
}

// Cycle moves the active tab by dir (negative wraps backward), modulo tab count.
func (v *MultiAgentView) Cycle(dir int) {
	n := len(v.tabs)
	if n == 0 {
		return
	}
	v.active = ((v.active+dir)%n + n) % n
}

// TabIndex returns the "[active/total]" indicator string (e.g. "2/5").
func (v *MultiAgentView) TabIndex() string {
	n := len(v.tabs)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d", v.ActiveIndex()+1, n)
}

// Rows returns a copy of the stats rows (in first-seen order).
func (v *MultiAgentView) Rows() []AgentEnhancedRow {
	out := make([]AgentEnhancedRow, len(v.rows))
	copy(out, v.rows)
	return out
}

// LogFor returns the transcript for one agent (nil if absent).
func (v *MultiAgentView) LogFor(agentID string) *AgentLog {
	return v.logs[agentID]
}

// AggregateTokens sums every row's token counters for the stats footer.
func (v *MultiAgentView) AggregateTokens() (in, out, ch, turns int) {
	for _, r := range v.rows {
		in += r.TokensIn
		out += r.TokensOut
		ch += r.CacheRead
		turns += r.Turns
	}
	return
}
