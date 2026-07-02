// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

func (a *Agent) emitEvent(event OutputEvent) {
	a.mu.Lock()
	entries := make([]observerEntry, len(a.observers))
	copy(entries, a.observers)
	a.mu.Unlock()

	for _, entry := range entries {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panicked; continue with remaining observers.
				}
			}()
			entry.obs.OnEvent(event)
		}()
	}
}

func (a *Agent) emitMessage(msg Message) {
	switch msg.Type {
	case End:
		a.emitEndEvent(msg)
	case ToolCall:
		a.emitToolCallEvent(msg)
	default:
		a.emitContentMessage(msg)
	}
	a.emitStatelessEvents(msg)
}

func (a *Agent) emitEndEvent(msg Message) {
	a.transitionTo(StateIdle)
	a.emitEvent(OutputEvent{Type: EventEnd, Metadata: msg.Metadata})
}

func (a *Agent) emitToolCallEvent(msg Message) {
	a.transitionTo(StateToolCall)
	a.emitEvent(OutputEvent{
		Type: EventToolCall, State: StateToolCall,
		ToolName: msg.ToolName, ToolInput: msg.ToolInput, ToolCallID: msg.ToolCallID,
		Metadata: msg.Metadata,
	})
	a.transitionTo(StateIdle)
}

func (a *Agent) emitContentMessage(msg Message) {
	if msg.Role == ToolRole {
		a.emitToolResult(msg)
	} else if msg.Thinking != "" {
		a.emitThinking(msg)
	} else if msg.Content != "" {
		a.emitTextContent(msg)
	}
}

func (a *Agent) emitToolResult(msg Message) {
	a.transitionTo(StateToolResult)
	a.emitEvent(OutputEvent{
		Type: EventToolResult, State: StateToolResult,
		Role: msg.Role, Text: msg.Content, ToolCallID: msg.ToolCallID,
		Metadata: msg.Metadata,
	})
	if !msg.Delta {
		a.transitionTo(StateIdle)
	}
}

func (a *Agent) emitThinking(msg Message) {
	a.transitionTo(StateThinking)
	a.emitEvent(OutputEvent{
		Type: EventContent, State: StateThinking,
		Role: msg.Role, Text: msg.Thinking, IsDelta: msg.Delta,
		Metadata: msg.Metadata,
	})
	if !msg.Delta {
		a.transitionTo(StateIdle)
	}
}

func (a *Agent) emitTextContent(msg Message) {
	a.transitionTo(StateContent)
	a.emitEvent(OutputEvent{
		Type: EventContent, State: StateContent,
		Role: msg.Role, Text: msg.Content, IsDelta: msg.Delta,
		Metadata: msg.Metadata,
	})
	if !msg.Delta {
		a.transitionTo(StateIdle)
	}
}

func (a *Agent) emitStatelessEvents(msg Message) {
	if msg.Timings != nil {
		a.turnStatsEmitted = true
		a.emitEvent(OutputEvent{Type: EventTokenStats, Timings: msg.Timings, Metadata: msg.Metadata})
	}
	if msg.PromptProgress != nil {
		a.emitEvent(OutputEvent{Type: EventProgress, PromptProgress: msg.PromptProgress, Metadata: msg.Metadata})
	}
}
