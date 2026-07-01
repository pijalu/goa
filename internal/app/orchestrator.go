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

func (a *App) runOrchestratorEventForwarder(done chan struct{}) {
	var section *tui.CompanionSectionComponent
	var cycle int
	var thinkingBuf strings.Builder
	var messageBuf strings.Builder

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
				if a.handleOrchestratorStreamMsg(m, &section, &cycle, &thinkingBuf, &messageBuf) {
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
	case a.subs.events.Control <- event.ControlEvent{GateApproval: &event.GateApproval{
		StageID:   parts[0],
		StageName: parts[1],
		Prompt:    parts[2],
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

func (a *App) handleOrchestratorStreamMsg(
	msg multiagent.OrchestratorMessage,
	section **tui.CompanionSectionComponent,
	cycle *int,
	thinkingBuf *strings.Builder,
	messageBuf *strings.Builder,
) bool {
	ensureCompanionSection := func() {
		if a.subs.chat == nil {
			return
		}
		if *section == nil || (*section).Done() {
			*cycle++
			*section = a.subs.chat.AddCompanionCycle(*cycle)
			// Companion section started — mark companion as busy
			a.subs.footer.SetCompanionBusy(true)
			a.subs.footer.SetData(tui.FooterData{
				CompanionActivity: "reviewing",
			})
			a.subs.tuiEngine.RequestRender()
		}
	}

	switch msg.Kind {
	case "content":
		a.handleOrchestratorContentStream(msg, section, messageBuf, ensureCompanionSection)
		return true
	case "thinking_start":
		ensureCompanionSection()
		thinkingBuf.Reset()
		// Show companion thinking activity
		a.subs.footer.SetData(tui.FooterData{
			CompanionActivity: "thinking",
		})
		a.subs.tuiEngine.RequestRender()
		return true
	case "thinking_chunk":
		ensureCompanionSection()
		thinkingBuf.WriteString(msg.Content)
		if *section != nil {
			(*section).SetThinking(thinkingBuf.String())
			a.subs.tuiEngine.RequestRender()
		}
		return true
	case "thinking_end":
		if *section != nil {
			(*section).SetThinking(thinkingBuf.String())
			a.subs.tuiEngine.RequestRender()
		}
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

func (a *App) handleOrchestratorContentStream(
	msg multiagent.OrchestratorMessage,
	section **tui.CompanionSectionComponent,
	messageBuf *strings.Builder,
	ensureSection func(),
) {
	if a.subs.chat == nil {
		return
	}
	switch msg.To {
	case "stream_start":
		ensureSection()
		messageBuf.Reset()
		if *section != nil {
			(*section).SetMessage("")
		}
	case "stream_chunk":
		messageBuf.WriteString(msg.Content)
		if *section != nil {
			(*section).SetMessage(messageBuf.String())
		}
	case "stream_end":
		if *section != nil {
			(*section).SetMessage(messageBuf.String())
			(*section).SetDone(messageBuf.String())
		}
		*section = nil
		messageBuf.Reset()
		// Companion finished — clear busy indicators
		a.subs.footer.SetCompanionBusy(false)
		a.subs.footer.SetData(tui.FooterData{
			CompanionActivity: "",
		})
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
