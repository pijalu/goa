// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package agentctx holds the per-agent view context for the multi-agent TUI:
// one AgentTranscript per agent (its own ChatViewport + entry list) plus the
// saved compositor state (AgentCompositor) needed to detach/reattach it, all
// keyed by an AgentViewRegistry.
//
// T1 wires ONLY the main agent through an AgentTranscript: the main agent's
// ChatViewport is created and owned here, then mounted into the engine's
// component tree exactly as before. Behavior is therefore unchanged — the
// package is a pure extraction that introduces the mount/unmount and
// saved-state seams the later multi-agent phases build on.
package agentctx

import "github.com/pijalu/goa/tui"

// MainAgentID is the registry id of the primary (foreground) agent — the only
// view that exists in T1. Sub-agent delegations (T2+) receive their own ids.
const MainAgentID = "main"

// AgentTranscript owns one agent's conversation view: its ChatViewport (which
// embeds the Conversation model, i.e. the entry list) plus the per-agent
// compositor snapshot used to detach and later reattach it.
//
// Ownership / concurrency (R1): like the ChatViewport it wraps, a transcript
// is mutated only on the TUI command loop, so it needs no internal locking.
// Mount/Unmount flip pure local state; the engine's component tree holds the
// single live View at any moment, so nothing here performs terminal writes.
type AgentTranscript struct {
	id   string
	view *tui.ChatViewport
	comp *AgentCompositor

	// mounted reports whether the transcript's viewport is the one currently
	// attached to (rendered by) the engine. Pure bookkeeping in T1.
	mounted bool
}

// NewAgentTranscript creates a transcript for agent id backed by a fresh
// ChatViewport. The transcript starts unmounted; Mount marks it live.
func NewAgentTranscript(id string) *AgentTranscript {
	return &AgentTranscript{
		id:   id,
		view: tui.NewChatViewport(),
		comp: NewAgentCompositor(),
	}
}

// ID returns the agent identifier this transcript belongs to.
func (at *AgentTranscript) ID() string { return at.id }

// View returns the transcript's ChatViewport — the Component mounted into the
// engine's component tree and the target for all of the agent's entries.
func (at *AgentTranscript) View() *tui.ChatViewport { return at.view }

// Compositor returns the per-agent compositor holder (saved frame state).
func (at *AgentTranscript) Compositor() *AgentCompositor { return at.comp }

// Mount marks the transcript as the live (rendered) view. The caller attaches
// at.View() to the engine's component tree; in T1 the main agent is mounted
// once at startup and stays mounted, so this is a state marker only.
func (at *AgentTranscript) Mount() {
	at.mounted = true
}

// Unmount detaches the transcript from the screen, recording that it is no
// longer the live view. The entry list is untouched — it is the transcript's
// persistent state — so a later Mount re-renders the identical conversation.
// The saved compositor snapshot is the registry's concern (T2); here we only
// flip the mounted flag. Pure state change: no terminal writes.
func (at *AgentTranscript) Unmount() {
	at.mounted = false
}

// Mounted reports whether the transcript is currently the live view.
func (at *AgentTranscript) Mounted() bool { return at.mounted }

// Len returns the number of conversation entries owned by this transcript.
func (at *AgentTranscript) Len() int { return at.view.Len() }

// Snapshot returns a pure-data copy of the transcript's conversation (no View
// references), for offline inspection by agents/controllers and tests.
func (at *AgentTranscript) Snapshot() []tui.MessageData { return at.view.Snapshot() }
