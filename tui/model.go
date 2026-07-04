// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// MessageData is the protocol-free, structured record of one conversation
// message — the Model's representation. It is consumed by AI agent tooling and
// controllers, independent of how the message is rendered (docs/TUI-REWORK-
// EXTENDED.md §3). Render components (userMessage, assistantMessage, …) are the
// View; MessageData is the Model.
type MessageData struct {
	ID   int
	Type ConsoleItemType
	Text string
	Meta map[string]string
}

// MessageEntry pairs a message's Model data with its View component. The
// Conversation stores entries; agents/controllers read the Data, the renderer
// reads the View. Keeping them paired in one entry (updated together through a
// single write path) is what keeps Model and View in sync by construction.
// MessageEntry pairs one message's data with its visual Component (the View).
// The View owns the rendering logic; the Data is the semantic payload.
type MessageEntry struct {
	Data MessageData
	View Component
	// render cache: the ChatViewport owns these fields and invalidates them
	// when the conversation is mutated. Storing the cache per entry lets the
	// viewport re-render only the entries that changed instead of rebuilding
	// the entire conversation on every streaming frame.
	renderedWidth int
	renderedLines []string
	dirty         bool
	// lineOffset is the index in the frame cache where this entry's rendered
	// lines start. Cached to avoid an O(n) scan in updateLastEntry.
	lineOffset int
}

// Conversation is the Model: the authoritative, generic store of the chat
// history. It exposes a small set of composable primitives (Append / UpdateLast
// / RemoveLast / ForEach / Snapshot / …) rather than one method per message
// type, so new message kinds are added by composing factories + Append without
// modifying this type (Open/Closed). ChatViewport embeds it and delegates.
//
// Concurrency: the commandLoop is the sole owner of entries. Every primitive
// runs on the commandLoop, so no mutex is required.
type Conversation struct {
	entries []MessageEntry
	nextID  int
}

// NewConversation creates an empty Conversation.
func NewConversation() *Conversation { return &Conversation{} }

// Append adds an entry, assigning it a stable ID. Returns the ID.
func (c *Conversation) Append(e MessageEntry) int {
	c.nextID++
	e.Data.ID = c.nextID
	c.entries = append(c.entries, e)
	return e.Data.ID
}

// UpdateLast applies fn to the most recent entry whose type is in types
// (nil/empty types = match any). fn should update both Data and View so they
// stay in sync. Returns false if nothing matched.
func (c *Conversation) UpdateLast(types []ConsoleItemType, fn func(*MessageEntry)) bool {
	for i := len(c.entries) - 1; i >= 0; i-- {
		if matchesMessageType(c.entries[i].Data.Type, types) {
			fn(&c.entries[i])
			return true
		}
	}
	return false
}

// RemoveLast removes and returns the most recent entry whose type is in types
// (nil/empty = any). Returns the entry and true if one was removed.
func (c *Conversation) RemoveLast(types []ConsoleItemType) (MessageEntry, bool) {
	if len(c.entries) == 0 {
		return MessageEntry{}, false
	}
	last := c.entries[len(c.entries)-1]
	if !matchesMessageType(last.Data.Type, types) {
		return MessageEntry{}, false
	}
	c.entries = c.entries[:len(c.entries)-1]
	return last, true
}

// LastView returns the View of the most recent entry matching types
// (nil/empty = any), or nil if none.
func (c *Conversation) LastView(types []ConsoleItemType) Component {
	e, ok := c.LastWhere(func(e MessageEntry) bool {
		return matchesMessageType(e.Data.Type, types)
	})
	if !ok {
		return nil
	}
	return e.View
}

// LastWhere returns the most recent entry satisfying pred (oldest-to-newest
// search, returns the last match). Generic predicate keeps callers composable.
func (c *Conversation) LastWhere(pred func(MessageEntry) bool) (MessageEntry, bool) {
	for i := len(c.entries) - 1; i >= 0; i-- {
		if pred(c.entries[i]) {
			return c.entries[i], true
		}
	}
	return MessageEntry{}, false
}

// ForEach iterates entries oldest-to-newest.
func (c *Conversation) ForEach(fn func(MessageEntry)) {
	for _, e := range c.entries {
		fn(e)
	}
}

// Snapshot returns a pure-data copy of the conversation for offline
// consumption by agents/controllers (no View references).
func (c *Conversation) Snapshot() []MessageData {
	out := make([]MessageData, len(c.entries))
	for i, e := range c.entries {
		out[i] = e.Data
	}
	return out
}

// LastAssistantText returns the text of the most recent assistant message.
func (c *Conversation) LastAssistantText() string {
	for i := len(c.entries) - 1; i >= 0; i-- {
		if c.entries[i].Data.Type == ConsoleAssistantMessage {
			return c.entries[i].Data.Text
		}
	}
	return ""
}

// Clear removes all entries.
func (c *Conversation) Clear() { c.entries = nil }

// Len returns the number of entries.
func (c *Conversation) Len() int { return len(c.entries) }

// matchesMessageType reports whether t is in types, treating nil/empty types as
// a wildcard (match any). This keeps the generic primitives type-agnostic.
func matchesMessageType(t ConsoleItemType, types []ConsoleItemType) bool {
	if len(types) == 0 {
		return true
	}
	for _, want := range types {
		if want == t {
			return true
		}
	}
	return false
}
