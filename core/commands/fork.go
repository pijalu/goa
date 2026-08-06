// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strconv"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/sessiontree"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// ForkCommand handles /fork: pi-style forking of a saved session at a chosen
// user turn into a new conversation, plus the legacy /fork:<node-id>
// session-tree branch form.
type ForkCommand struct {
	// Manager drives the session-tree branch form (/fork:<parent-node-id>).
	// The no-args picker flow uses only Context role interfaces.
	Manager *sessiontree.Manager
}

// Name returns the command name.
func (c *ForkCommand) Name() string { return "fork" }

// Aliases returns command aliases.
func (c *ForkCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *ForkCommand) ShortHelp() string {
	return "Fork a saved session at a chosen turn into a new conversation"
}

// LongHelp returns usage help.
func (c *ForkCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides node ID completions from the session tree.
func (c *ForkCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if c.Manager == nil {
		return nil
	}
	return nodeCompletions(c.Manager.Tree(), prefix)
}

// Run executes the command. With an argument it preserves the session-tree
// branch behavior (/fork:<parent-node-id>); without arguments it opens the
// session → turn picker flow.
func (c *ForkCommand) Run(ctx core.Context, args []string) error {
	if len(args) > 0 && args[0] != "" {
		return c.runTreeBranch(ctx, args[0])
	}

	maxHistory := 0
	if ctx.Config != nil && ctx.Config.TUI.History.MaxLoaded != nil {
		maxHistory = *ctx.Config.TUI.History.MaxLoaded
	}
	return forkSessionPicker(ctx, ctx, ctx, ctx.SessionStore, ctx.AgentManager, maxHistory, ctx.SetEditorText)
}

// runTreeBranch implements the legacy /fork:<parent-node-id> form.
func (c *ForkCommand) runTreeBranch(ctx core.Context, parentID string) error {
	if c.Manager == nil {
		return fmt.Errorf("session tree not configured")
	}
	n, err := c.Manager.Fork(parentID, "", "")
	if err != nil {
		return fmt.Errorf("fork failed: %w", err)
	}
	ctx.Writef("Created branch %s\n", n.ID)
	return nil
}

// forkSessionPicker lists forkable sessions and opens the session picker;
// selecting a session loads its events and opens the turn picker.
// prefill (optional, nil-safe) receives the selected message text for editor
// prefill after a successful fork.
// Depends on OutputWriter + EventSink + Selector + SessionStoreAPI.
func forkSessionPicker(
	w core.OutputWriter,
	es core.EventSink,
	sel core.Selector,
	store core.SessionStoreAPI,
	agentMgr *core.AgentManager,
	maxHistory int,
	prefill func(string),
) error {
	if store == nil {
		writeStr(w, "Session store not available.\n")
		return nil
	}
	sessions, err := store.ListSessions()
	if err != nil {
		writeFmt(w, "Error listing sessions: %v\n", err)
		return nil
	}
	sessions = filterSessionsWithModelTurn(sessions)
	if len(sessions) == 0 {
		writeStr(w, "No saved sessions found.\n")
		return nil
	}

	sel.SelectOption("Select session to fork:", buildSessionItems(sessions), "", func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		events, err := store.LoadSession(selected)
		if err != nil {
			es.Flash(fmt.Sprintf("Error loading session: %v", err))
			return
		}
		forkTurnPicker(w, es, sel, store, agentMgr, selected, events, maxHistory, prefill)
	})
	return nil
}

// forkTurnPicker opens the turn picker for one session: every user message in
// the stored event stream is a fork point, shown chronologically with the most
// recent turn preselected (pi semantics: fork → edit → resend).
func forkTurnPicker(
	w core.OutputWriter,
	es core.EventSink,
	sel core.Selector,
	store core.SessionStoreAPI,
	agentMgr *core.AgentManager,
	src string,
	events []agentic.OutputEvent,
	maxHistory int,
	prefill func(string),
) {
	turns := userTurnIndexes(events)
	if len(turns) == 0 {
		es.Flash(fmt.Sprintf("No messages to fork from in '%s'", src))
		return
	}

	items := make([]tui.SelectorItem, 0, len(turns))
	for i, idx := range turns {
		items = append(items, tui.SelectorItem{
			Value:         strconv.Itoa(idx),
			Label:         fmt.Sprintf("Turn %d  %s", i+1, truncateFirstMessage(events[idx].Text)),
			Description:   fmt.Sprintf("%d of %d", i+1, len(turns)),
			PreserveOrder: true,
		})
	}
	// Preselect the most recent turn, mirroring pi's initialSelectedId.
	current := items[len(items)-1].Value

	sel.SelectOption("Fork '"+src+"' at turn:", items, current, func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		forkConfirmTurn(w, es, store, agentMgr, src, events, turns, selected, maxHistory, prefill)
	})
}

// forkConfirmTurn applies a confirmed turn-picker selection: resolves the cut
// point, performs the fork, and prefills the editor with the selected message.
func forkConfirmTurn(
	w core.OutputWriter,
	es core.EventSink,
	store core.SessionStoreAPI,
	agentMgr *core.AgentManager,
	src string,
	events []agentic.OutputEvent,
	turns []int,
	selected string,
	maxHistory int,
	prefill func(string),
) {
	cut, err := strconv.Atoi(selected)
	if err != nil || cut < 0 || cut > len(events) {
		es.Flash("Invalid fork point")
		return
	}
	turn := 1
	for i, idx := range turns {
		if idx == cut {
			turn = i + 1
			break
		}
	}
	if err := forkAtTurn(w, es, store, agentMgr, src, events, cut, turn, len(turns), maxHistory); err != nil {
		es.Flash(fmt.Sprintf("Fork failed: %v", err))
		return
	}
	// Pi parity: prefill the editor with the selected message so the user
	// can edit and resend it.
	if prefill != nil {
		prefill(events[cut].Text)
	}
}

// forkAtTurn starts a new conversation from the source session's events
// truncated just before the selected user message: the agent's history is
// rebuilt from events[:cut], the session store writer switches to a fresh
// derived session ID (so continuing the fork never appends to the source
// file), the chat viewport is cleared, and the truncated events are replayed
// for display.
func forkAtTurn(
	w core.OutputWriter,
	es core.EventSink,
	store core.SessionStoreAPI,
	agentMgr *core.AgentManager,
	src string,
	events []agentic.OutputEvent,
	cut, turn, total, maxHistory int,
) error {
	if store == nil {
		writeStr(w, "Session store not available.\n")
		return nil
	}
	if cut < 0 || cut > len(events) {
		return fmt.Errorf("invalid fork point %d", cut)
	}
	cutEvents := events[:cut]
	forkID := deriveForkID(src)

	replayer, ok := es.(core.AgentEventReplayer)
	if !ok {
		// Environments without replay support (tests, headless) fall back to
		// a summary flash for each event, same as session restore.
		for _, ev := range cutEvents {
			es.Flash(fmt.Sprintf("%s event", ev.Type))
		}
		return nil
	}

	if agentMgr != nil {
		if agent := agentMgr.CurrentAgent(); agent != nil {
			// Rebuild conversation history from the truncated events.
			agent.SetHistory(agentic.EventsToHistory(cutEvents))

			// Adopt the fresh fork identity: continuing the conversation
			// persists to a new file, never back into the source session.
			opts := agent.StreamOptions()
			opts.SessionID = forkID
			agent.SetStreamOptions(opts)

			// Switch the session store writer to the fork session ID.
			store.StartSessionWithID(forkID)
		}

		// Load input history for the source session: the selected message is
		// an Up-arrow away for edit + resend.
		if ss, ok := store.(*core.SessionStore); ok && maxHistory > 0 {
			agentMgr.SetPendingInputHistory(buildCombinedInputHistory(ss, src, maxHistory))
		}
	}

	es.ClearChat()
	es.Flash(fmt.Sprintf("Forked '%s' at turn %d of %d → new session '%s'", src, turn, total, forkID))

	// Replay events off the command goroutine so the UI event loop can drain
	// the agent bus (same deadlock-avoidance as session restore).
	go func() {
		for _, ev := range cutEvents {
			replayer.ReplayAgentEvent(ev)
		}
		es.Flash(fmt.Sprintf("Forked session: %s (%d events)", forkID, len(cutEvents)))
	}()
	return nil
}

// userTurnIndexes returns the indexes of all user-message events
// (EventContent with Role==User and non-empty Text), in stream order.
func userTurnIndexes(events []agentic.OutputEvent) []int {
	var idxs []int
	for i, ev := range events {
		if ev.Type == agentic.EventContent && ev.Role == agentic.User && ev.Text != "" {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

// deriveForkID builds a fresh session ID for a fork of src. The preferred
// form is "<src>_fork_<unix>"; when src is not a safe filename base the ID
// falls back to "fork_<unix>_<rand>". The result always passes
// validForkSessionID.
func deriveForkID(src string) string {
	if validForkSessionID(src) {
		return fmt.Sprintf("%s_fork_%d", src, time.Now().Unix())
	}
	return fmt.Sprintf("fork_%d_%s", time.Now().Unix(), internal.RandomString(6))
}

// validForkSessionID reports whether id is a safe session file base name:
// 1–64 characters of [A-Za-z0-9_-] only (the store appends ".jsonl").
func validForkSessionID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func nodeCompletions(t *sessiontree.Tree, prefix string) []core.ArgCompletion {
	if t == nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, n := range t.All() {
		if prefix == "" || (len(prefix) <= len(n.ID) && n.ID[:len(prefix)] == prefix) {
			comps = append(comps, core.ArgCompletion{Value: n.ID, Description: n.Summary})
		}
	}
	return comps
}
