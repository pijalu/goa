// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
)

// roleStreamState holds the TUI stream state for ONE sub-agent role: its own
// companion section plus its own thinking/message buffers. Concurrent
// delegates (planner + coder + companion) each get an isolated state — the
// previous single shared state cross-wired chunks and stream_end between
// roles, leaving sections stuck on "thinking..." (team UI bug RC-1).
type roleStreamState struct {
	section     *tui.CompanionSectionComponent
	thinkingBuf strings.Builder
	messageBuf  strings.Builder
}

// streamForwarder is the per-forwarder registry of role stream states plus
// the footer active-role tracking (a set of currently-streaming roles; the
// last element is the most recently started, shown in the status bar).
type streamForwarder struct {
	roles  map[string]*roleStreamState
	cycles map[string]int
	active []string
}

func newStreamForwarder() *streamForwarder {
	return &streamForwarder{
		roles:  make(map[string]*roleStreamState),
		cycles: make(map[string]int),
	}
}

// stateFor returns the stream state for role, creating it on first use.
func (f *streamForwarder) stateFor(role string) *roleStreamState {
	if role == "" {
		role = "companion"
	}
	st, ok := f.roles[role]
	if !ok {
		st = &roleStreamState{}
		f.roles[role] = st
	}
	return st
}

// markActive registers role as streaming (idempotent) and returns the role
// whose identity the footer should show (the most recently started active one).
func (f *streamForwarder) markActive(role string) string {
	for _, r := range f.active {
		if r == role {
			return f.active[len(f.active)-1]
		}
	}
	f.active = append(f.active, role)
	return role
}

// markInactive removes role from the active set and returns the role the
// footer should now show ("" when no role is streaming anymore).
func (f *streamForwarder) markInactive(role string) string {
	out := f.active[:0]
	for _, r := range f.active {
		if r != role {
			out = append(out, r)
		}
	}
	f.active = out
	if len(f.active) == 0 {
		return ""
	}
	return f.active[len(f.active)-1]
}

// anyActive reports whether any role is still streaming.
func (f *streamForwarder) anyActive() bool { return len(f.active) > 0 }

// nextCycle advances and returns the per-role cycle counter.
func (f *streamForwarder) nextCycle(role string) int {
	f.cycles[role]++
	return f.cycles[role]
}

func (a *App) runOrchestratorEventForwarder(done chan struct{}) {
	fwd := newStreamForwarder()

	for {
		select {
		case <-done:
			return
		case m, ok := <-a.subs.foregroundOrch.Events():
			if !ok {
				return
			}
			if a.handleOrchestratorControlMsg(m) {
				continue
			}
			a.apply(func() {
				if a.handleOrchestratorStreamMsg(m, fwd) {
					return
				}
				a.handleOrchestratorProgressMsg()
			})
		}
	}
}

func (a *App) handleOrchestratorControlMsg(msg multiagent.OrchestratorMessage) bool {
	switch {
	case msg.From == "gate" && strings.HasPrefix(msg.Content, "GATE_APPROVAL:"):
		a.forwardGateApproval(msg)
		return true
	case msg.From == "orchestrator" && strings.HasPrefix(msg.Content, "TASK_STATUS:"):
		a.forwardTaskStatus(msg)
		return true
	case msg.From == "system" && msg.Kind == "companion_cycle":
		a.forwardCompanionCycle()
		return true
	default:
		return false
	}
}

func (a *App) forwardGateApproval(msg multiagent.OrchestratorMessage) {
	parts := strings.SplitN(strings.TrimPrefix(msg.Content, "GATE_APPROVAL:"), "|", 3)
	if len(parts) != 3 {
		return
	}
	select {
	case a.subs.events.Chat <- event.ChatEvent{Flash: &event.Flash{
		Text: fmt.Sprintf("Gate %q needs approval — /gate approve %s or /gate reject %s", parts[0], parts[1], parts[2]),
	}}:
	default:
	}
}

func (a *App) forwardTaskStatus(msg multiagent.OrchestratorMessage) {
	parts := strings.SplitN(strings.TrimPrefix(msg.Content, "TASK_STATUS:"), "|", 5)
	if len(parts) != 5 {
		return
	}
	idx, _ := fmt.Sscanf(parts[3], "%d", new(int))
	total, _ := fmt.Sscanf(parts[4], "%d", new(int))
	select {
	case a.subs.events.Chat <- event.ChatEvent{Flash: &event.Flash{
		Text: fmt.Sprintf("Task %d/%d: %s — %s", idx+1, total, parts[1], parts[2]),
	}}:
	default:
	}
}

func (a *App) forwardCompanionCycle() {
	count, max := a.subs.foregroundOrch.CompanionCount()
	select {
	case a.subs.events.Footer <- event.FooterEvent{CompanionCycle: &event.CompanionCycle{Current: count, Max: max}}:
	default:
	}
}

// setActiveAgentFooter resolves the streaming sub-agent's real provider/model
// from the agent pool and pushes it to the status bar, so the footer shows the
// delegated agent actually running (team bug #3) instead of the main model.
// The stream's From field carries the role ("coder", "companion", …).
func (a *App) setActiveAgentFooter(role string) {
	if a.subs.agentPool == nil || role == "" {
		return
	}
	providerID, modelID := a.subs.agentPool.RoleModelInfo(role)
	if modelID == "" {
		return
	}
	a.subs.footer.SetActiveAgent(providerID, modelID, role)
}

func (a *App) handleOrchestratorStreamMsg(msg multiagent.OrchestratorMessage, fwd *streamForwarder) bool {
	// T4: delegation streams (delegate_to / request_review) route by
	// DelegationID into their own per-delegation AgentTranscripts via the
	// registry — they no longer interleave into the shared chat through
	// AddCompanionCycle. Untranslatable delegation kinds (stream framing) are
	// delegation traffic, not InterAgent messages, so they are swallowed here.
	if msg.DelegationID != "" {
		if ne, ok := translateDelegationMsg(msg); ok {
			a.handleDelegationViewEvent(ne)
		}
		return true
	}

	role := msg.From
	st := fwd.stateFor(role)

	// ensureSection creates this role's section on first activity (or after
	// the previous cycle closed). Every role gets its own cycle counter so
	// titles read "planner · cycle 1", "coder · cycle 1", …
	ensureSection := func() {
		if a.subs.chat == nil {
			return
		}
		if st.section != nil && !st.section.Done() {
			return
		}
		st.section = a.subs.chat.AddCompanionCycle(fwd.nextCycle(role), role)
		// Section created → the role is now streaming. Mark it active and
		// surface the most recently started active role's real provider/model
		// in the status bar (footer work happens once per section, not per
		// chunk, to avoid rebuild churn on every delta).
		top := fwd.markActive(role)
		a.subs.footer.SetCompanionBusy(true)
		a.setActiveAgentFooter(top)
		a.subs.footer.SetData(tui.FooterData{CompanionActivity: "reviewing"})
		a.subs.tuiEngine.RequestRender()
	}

	switch msg.Kind {
	case "content":
		a.handleRoleContentStream(msg, st, fwd, ensureSection)
		return true
	case "thinking_start":
		ensureSection()
		st.thinkingBuf.Reset()
		a.subs.footer.SetData(tui.FooterData{CompanionActivity: "thinking"})
		a.subs.tuiEngine.RequestRender()
		return true
	case "thinking_chunk":
		ensureSection()
		st.thinkingBuf.WriteString(msg.Content)
		if st.section != nil {
			st.section.SetThinking(st.thinkingBuf.String())
			a.subs.tuiEngine.RequestRender()
		}
		return true
	case "thinking_end":
		if st.section != nil {
			st.section.SetThinking(st.thinkingBuf.String())
			a.subs.tuiEngine.RequestRender()
		}
		return true
	case "tool_call", "tool_result":
		a.handleRoleToolEvent(msg, st, ensureSection)
		return true
	default:
		select {
		case a.subs.events.Chat <- event.ChatEvent{InterAgent: &event.InterAgent{
			From:    msg.From,
			To:      msg.To,
			Content: msg.Content,
		}}:
		default:
		}
		return false
	}
}

// handleRoleToolEvent surfaces sub-agent tool activity in the role's section
// ("⚙ name" on call, merged "⚙ name → ✓ preview" on result) so the user sees
// real work, not just thinking (team UI bug RC-2).
func (a *App) handleRoleToolEvent(msg multiagent.OrchestratorMessage, st *roleStreamState, ensureSection func()) {
	ensureSection()
	if st.section == nil {
		return
	}
	if msg.Kind == "tool_call" {
		st.section.AddToolLine(msg.Content, "")
	} else {
		st.section.AddToolLine("", msg.Content)
	}
	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleRoleContentStream(
	msg multiagent.OrchestratorMessage,
	st *roleStreamState,
	fwd *streamForwarder,
	ensureSection func(),
) {
	if a.subs.chat == nil {
		return
	}
	switch msg.To {
	case "stream_start":
		ensureSection()
		st.messageBuf.Reset()
		if st.section != nil {
			st.section.SetMessage("")
		}
	case "stream_chunk":
		st.messageBuf.WriteString(msg.Content)
		if st.section != nil {
			st.section.SetMessage(st.messageBuf.String())
		}
	case "stream_end":
		if st.section != nil {
			st.section.SetMessage(st.messageBuf.String())
			st.section.SetDone(st.messageBuf.String())
		}
		st.section = nil
		st.messageBuf.Reset()
		st.thinkingBuf.Reset()
		// This role finished — drop it from the active set. The footer reverts
		// to the main model only when NO role is streaming anymore; otherwise
		// it shows the next most recently started active role (RC-1 footer fix).
		top := fwd.markInactive(msg.From)
		a.subs.footer.SetCompanionBusy(fwd.anyActive())
		if top == "" {
			a.subs.footer.SetActiveAgent("", "", "")
			a.subs.footer.SetData(tui.FooterData{CompanionActivity: ""})
		} else {
			a.setActiveAgentFooter(top)
		}
		// Force full render to avoid screen shrinking artifacts when the
		// companion section collapses from many lines to 1.
		a.subs.tuiEngine.RequestRender()
		return
	}
	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleOrchestratorProgressMsg() {
	progress := a.subs.foregroundOrch.Progress()
	select {
	case a.subs.events.Footer <- event.FooterEvent{WorkflowProgress: &event.WorkflowProgress{
		StageIndex:  progress.StageIndex,
		TotalStages: progress.TotalStages,
		StageName:   progress.StageName,
		StageID:     progress.StageID,
		Status:      progress.Status,
	}}:
	default:
	}
}

// runPipelineEventForwarder is the single consumer of PipelineRunner.Events().
// It forwards every pipeline stage event to the TUI chat as an InterAgent
// message. Started once at app setup so repeated /pipeline:run calls do not
// spawn competing consumers that would round-robin events away.
func (a *App) runPipelineEventForwarder(done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-a.subs.pipelineRunner.Events():
			if !ok {
				return
			}
			select {
			case a.subs.events.Chat <- event.ChatEvent{InterAgent: &event.InterAgent{
				From:    "pipeline",
				To:      "user",
				Content: fmt.Sprintf("[%s] %s: %s", ev.PipelineID, ev.StageID, ev.Status),
			}}:
			default:
			}
		}
	}
}
