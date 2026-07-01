// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/tui"
)

// SessionCommand manages saved sessions and session lifecycle.
// Subcommands: list, save, restore, delete, new, import.
// Consolidates the former /save, /restore, /sessions, and /new commands into a
// single resource-oriented command with subcommands, reducing the number of
// top-level completions.
type SessionCommand struct{}

func (c *SessionCommand) Name() string      { return "session" }
func (c *SessionCommand) Aliases() []string { return []string{} }
func (c *SessionCommand) ShortHelp() string {
	return "List, save, restore, delete, import sessions, or start a new session"
}
func (c *SessionCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *SessionCommand) CompleteArgs(_ core.Context, prefix string) []core.ArgCompletion {
	subs := []struct{ val, desc string }{
		{"list", "list all saved sessions"},
		{"save", "save the current session"},
		{"restore", "restore a saved session"},
		{"delete", "delete a saved session"},
		{"new", "start a fresh session"},
		{"import", "import a session from an export ZIP"},
	}
	var comps []core.ArgCompletion
	for _, s := range subs {
		if prefix == "" || strings.HasPrefix(s.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: s.val, Description: s.desc})
		}
	}
	return comps
}

func (c *SessionCommand) Run(ctx core.Context, args []string) error {
	sub := "restore"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		return listSessions(ctx, ctx.SessionStore)
	case "save":
		return saveCurrentSession(ctx, ctx.SessionStore, restArgs(args))
	case "new":
		return runNew(ctx)
	case "restore":
		if name, ok := namedArg(args); ok {
			return restoreSession(ctx, ctx, ctx.SessionStore, name)
		}
		return showSessionPicker(ctx, ctx, ctx, ctx.SessionStore, false)
	case "delete":
		if name, ok := namedArg(args); ok {
			return deleteSessionByName(ctx, ctx.SessionStore, name)
		}
		return showSessionPicker(ctx, ctx, ctx, ctx.SessionStore, true)
	case "import":
		return importSessionFromZip(ctx, ctx, ctx.SessionStore, restArgs(args))
	default:
		return fmt.Errorf("unknown session subcommand: %s (use 'list', 'save', 'restore', 'delete', 'new', or 'import')", sub)
	}
}

// restArgs returns args[1:], or nil when there are no further args.
func restArgs(args []string) []string {
	if len(args) > 1 {
		return args[1:]
	}
	return nil
}

// namedArg returns the first operand after a subcommand and whether it exists.
func namedArg(args []string) (string, bool) {
	if len(args) > 1 && args[1] != "" {
		return args[1], true
	}
	return "", false
}

// saveCurrentSession saves the current session under the given (or timestamp)
// name. Depends on OutputWriter + SessionStoreAPI.
func saveCurrentSession(w core.OutputWriter, store core.SessionStoreAPI, args []string) error {
	name := fmt.Sprintf("session_%s", time.Now().Format("20060102_150405"))
	if len(args) > 0 {
		name = args[0]
	}
	if store == nil {
		writeFmt(w, "Session '%s' saved (auto-save active)\n", name)
		return nil
	}
	if err := store.SaveCurrent(name); err != nil {
		if errors.Is(err, core.ErrEmptySession) {
			writeFmt(w, "Session '%s' is empty — not saved.\n", name)
			return nil
		}
		writeFmt(w, "Error saving session: %v\n", err)
		return nil
	}
	writeFmt(w, "Session saved: %s.jsonl\n", name)
	return nil
}

// deleteSessionByName deletes a saved session by name.
// Depends on OutputWriter + SessionStoreAPI.
func deleteSessionByName(w core.OutputWriter, store core.SessionStoreAPI, name string) error {
	if store == nil {
		writeStr(w, "Session store not available.\n")
		return nil
	}
	if err := store.DeleteSession(name); err != nil {
		writeFmt(w, "Error deleting session: %v\n", err)
		return nil
	}
	writeFmt(w, "Deleted session: %s\n", name)
	return nil
}

// restoreSession loads and replays events from a saved session.
// Depends on OutputWriter + EventSink + SessionStoreAPI.
func restoreSession(
	w core.OutputWriter,
	es core.EventSink,
	store core.SessionStoreAPI,
	name string,
) error {
	if store == nil {
		writeStr(w, "Session store not available.\n")
		return nil
	}
	events, err := store.LoadSession(name)
	if err != nil {
		writeFmt(w, "Error loading session: %v\n", err)
		return nil
	}
	writeFmt(w, "Restored session '%s' — %d events\n", name, len(events))

	replayer, ok := es.(core.AgentEventReplayer)
	if !ok {
		// Environments without replay support (tests, headless) fall back to
		// a summary flash for each event.
		for _, ev := range events {
			es.Flash(fmt.Sprintf("%s event", ev.Type))
		}
		return nil
	}

	es.ClearChat()
	es.InterruptAgent()
	es.Flash(fmt.Sprintf("Restored session '%s' — %d events", name, len(events)))

	// Replay events off the command goroutine so the UI event loop can drain
	// the agent bus. Synchronous sends would deadlock when the command loop is
	// also the only consumer of bus.Agent.
	go func() {
		for _, ev := range events {
			replayer.ReplayAgentEvent(ev)
		}
		es.Flash(fmt.Sprintf("Loaded session: %s (%d events)", name, len(events)))
	}()
	return nil
}

// showSessionPicker shows an interactive selector for sessions.
// Depends on OutputWriter + EventSink + Selector + SessionStoreAPI.
func showSessionPicker(
	w core.OutputWriter,
	es core.EventSink,
	sel core.Selector,
	store core.SessionStoreAPI,
	deleteMode bool,
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
	if len(sessions) == 0 {
		writeStr(w, "No saved sessions found.\n")
		return nil
	}

	items := buildSessionItems(sessions)
	sel.SelectOption("Select session:", items, "", func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		if deleteMode {
			if err := store.DeleteSession(selected); err != nil {
				es.Flash(fmt.Sprintf("Error deleting %q: %v", selected, err))
				return
			}
			es.Flash("Deleted session: " + selected)
			return
		}
		restoreSession(w, es, store, selected)
	})
	return nil
}

// buildSessionItems converts session records into selector items.
// Pure data transformation — no Context dependency.
func buildSessionItems(sessions []core.SessionInfo) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, len(sessions))
	for _, s := range sessions {
		desc := fmt.Sprintf("%d events", s.EventCount)
		if s.TokenTotal > 0 {
			desc += fmt.Sprintf(", %d tokens", s.TokenTotal)
		}
		if s.FirstMessage != "" {
			msg := truncateFirstMessage(s.FirstMessage)
			if desc != "" {
				desc = msg + " — " + desc
			} else {
				desc = msg
			}
		}
		items = append(items, tui.SelectorItem{
			Value:       s.Name,
			Label:       s.Name,
			Description: desc,
		})
	}
	return items
}

// truncateFirstMessage shortens a user message for display in the session picker.
func truncateFirstMessage(msg string) string {
	// Take the first line only.
	if idx := strings.IndexAny(msg, "\n\r"); idx >= 0 {
		msg = msg[:idx]
	}
	// Trim trailing whitespace/punctuation.
	msg = strings.TrimRight(msg, " \t\n\r.,;:!?")
	// Truncate if too long.
	runes := []rune(msg)
	if len(runes) > 60 {
		msg = string(runes[:57]) + "..."
	}
	return msg
}

// importSessionFromZip extracts session/events.jsonl from an export ZIP and
// imports it into the session store under the ZIP filename (without .zip).
func readEventsFromZip(zipPath string) ([]byte, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name != "session/events.jsonl" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("no session/events.jsonl found in the ZIP")
}

// importSessionFromZip extracts session/events.jsonl from an export ZIP and
// imports it into the session store under the ZIP filename (without .zip).
func importSessionFromZip(
	w core.OutputWriter,
	es core.EventSink,
	store core.SessionStoreAPI,
	args []string,
) error {
	if store == nil {
		writeStr(w, "Session store not available.\n")
		return nil
	}
	if len(args) == 0 || args[0] == "" {
		writeStr(w, "Usage: /session:import:<path-to-zip>\n")
		return nil
	}

	eventsData, err := readEventsFromZip(args[0])
	if err != nil {
		writeFmt(w, "Error opening ZIP: %v\n", err)
		return nil
	}

	// Write to a temp file and import through the store.
	tmpFile, err := os.CreateTemp("", "goa-import-*.jsonl")
	if err != nil {
		writeFmt(w, "Error creating temp file: %v\n", err)
		return nil
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(eventsData); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		writeFmt(w, "Error writing temp file: %v\n", err)
		return nil
	}
	tmpFile.Close()
	defer os.Remove(tmpPath)

	sessionName := strings.TrimSuffix(filepath.Base(args[0]), ".zip")
	if err := store.ImportSession(sessionName, tmpPath); err != nil {
		writeFmt(w, "Error importing session: %v\n", err)
		return nil
	}

	writeFmt(w, "Imported session '%s' from %s\n", sessionName, filepath.Base(args[0]))
	es.Flash(fmt.Sprintf("Imported session: %s", sessionName))
	return nil
}
func listSessions(w core.OutputWriter, store core.SessionStoreAPI) error {
	if store == nil {
		writeStr(w, "Session store not available.\n")
		return nil
	}
	sessions, err := store.ListSessions()
	if err != nil {
		writeFmt(w, "Error listing sessions: %v\n", err)
		return nil
	}
	if len(sessions) == 0 {
		writeStr(w, "No saved sessions found.\n")
		return nil
	}
	writeStr(w, "Available sessions:\n")
	for _, s := range sessions {
		dateStr := s.Date.Format("2006-01-02 15:04")
		if s.FirstMessage != "" {
			msg := truncateFirstMessage(s.FirstMessage)
			writeFmt(w, "  %s  %-30s  %s\n", dateStr, s.Name, msg)
		} else {
			writeFmt(w, "  %s  %-30s  (%d events, %d tokens)\n", dateStr, s.Name, s.EventCount, s.TokenTotal)
		}
	}
	return nil
}
