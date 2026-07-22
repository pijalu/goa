// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
)

// newSessionCmdCtx builds a Context wired with a session store and an output
// buffer, mirroring how Router.Execute invokes commands.
func newSessionCmdCtx(store core.SessionStoreAPI) (core.Context, *strings.Builder) {
	var buf strings.Builder
	return core.Context{SessionStore: store, OutputBuffer: &buf}, &buf
}

func TestSessionCommand_Run_DefaultsToRestore(t *testing.T) {
	cmd := &SessionCommand{}
	store := newSessionStore([]core.SessionInfo{{Name: "s1", EventCount: 3, HasModelTurn: true}})
	ctx, buf := newSessionCmdCtx(store)

	// Default calls showSessionPicker; with no SelectOptionFunc it cancels
	// silently — no error, no output.
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if buf.Len() > 0 {
		t.Errorf("expected no output from cancelled picker, got: %s", buf.String())
	}
}

func TestSessionCommand_Run_ListSubcommand(t *testing.T) {
	cmd := &SessionCommand{}
	store := newSessionStore([]core.SessionInfo{{Name: "s1", EventCount: 3}})
	ctx, buf := newSessionCmdCtx(store)

	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "s1") {
		t.Errorf("expected session s1 in list, got: %s", buf.String())
	}
}

func TestSessionCommand_Run_SaveWithName(t *testing.T) {
	cmd := &SessionCommand{}
	store := newSessionStore(nil)
	ctx, buf := newSessionCmdCtx(store)

	if err := cmd.Run(ctx, []string{"save", "my-work"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "Session saved: my-work.jsonl") {
		t.Errorf("expected save output, got: %s", buf.String())
	}
}

func TestSessionCommand_Run_SaveEmpty(t *testing.T) {
	cmd := &SessionCommand{}
	store := core.NewSessionStore(t.TempDir())
	store.StartSession()
	ctx, buf := newSessionCmdCtx(store)

	if err := cmd.Run(ctx, []string{"save", "empty"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "is empty") {
		t.Errorf("expected empty-session message, got: %s", buf.String())
	}
}

func TestSessionCommand_Run_DeleteByName(t *testing.T) {
	cmd := &SessionCommand{}
	store := newSessionStore([]core.SessionInfo{{Name: "my-work", EventCount: 1}})
	ctx, buf := newSessionCmdCtx(store)

	if err := cmd.Run(ctx, []string{"delete", "my-work"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "Deleted session: my-work") {
		t.Errorf("expected delete output, got: %s", buf.String())
	}
}

func TestSessionCommand_Run_RestoreByName(t *testing.T) {
	cmd := &SessionCommand{}
	store := newSessionStore(nil)
	store.AddEvents("my-work", []agentic.OutputEvent{{Type: agentic.EventContent}})
	ctx, buf := newSessionCmdCtx(store)

	if err := cmd.Run(ctx, []string{"restore", "my-work"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "Restored session 'my-work'") {
		t.Errorf("expected restore output, got: %s", buf.String())
	}
}

func TestSessionCommand_Run_NewSubcommand(t *testing.T) {
	cmd := &SessionCommand{}
	store := newSessionStore(nil)
	ctx, _ := newSessionCmdCtx(store)

	// new subcommand stops session and sends NewSession event.
	// With no AgentManager, it just sends the control event.
	if err := cmd.Run(ctx, []string{"new"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestSessionCommand_Run_UnknownSubcommand(t *testing.T) {
	cmd := &SessionCommand{}
	ctx, _ := newSessionCmdCtx(nil)

	err := cmd.Run(ctx, []string{"frobnicate"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown session subcommand") {
		t.Errorf("expected unknown-subcommand error, got: %v", err)
	}
}

func TestSessionCommand_CompleteArgs(t *testing.T) {
	cmd := &SessionCommand{}
	comps := cmd.CompleteArgs(core.Context{}, "")
	seen := map[string]bool{}
	for _, c := range comps {
		seen[c.Value] = true
	}
	for _, want := range []string{"list", "save", "restore", "delete", "new", "import"} {
		if !seen[want] {
			t.Errorf("expected subcommand %q in completions, got %v", want, comps)
		}
	}
}

func TestSaveCurrentSession_NoStore(t *testing.T) {
	w := newWriter()
	if err := saveCurrentSession(w, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "auto-save active") {
		t.Errorf("expected auto-save message, got: %s", w.Text())
	}
}

func TestDeleteSessionByName_NoStore(t *testing.T) {
	w := newWriter()
	if err := deleteSessionByName(w, nil, "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Session store not available") {
		t.Errorf("expected unavailable message, got: %s", w.Text())
	}
}

func TestDeleteSessionByName_Error(t *testing.T) {
	w := newWriter()
	store := newSessionStore(nil)
	store.err = assertError("permission denied")

	if err := deleteSessionByName(w, store, "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Error deleting session") {
		t.Errorf("expected error message, got: %s", w.Text())
	}
}
