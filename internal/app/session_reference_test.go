// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

func TestExpandSessionReferences_NoStoreIsNoop(t *testing.T) {
	a := &App{subs: &subsystems{}}
	out, err := expandSessionReferences(a.subs, "hello @[x](goa-session:s1)")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello @[x](goa-session:s1)" {
		t.Errorf("out = %q, want unchanged", out)
	}
}

func TestExpandSessionReferences_PrependsSnapshot(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	writeAppSession(t, dir, "s1", []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, Text: "prior session content"},
		{Type: agentic.EventEnd},
	})
	a := &App{subs: &subsystems{sessionStore: store}}

	out, err := expandSessionReferences(a.subs, "use @[old](goa-session:s1) please")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "## Referenced sessions") {
		t.Errorf("snapshot frame not prepended: %q", out[:min(60, len(out))])
	}
	if !strings.Contains(out, "prior session content") {
		t.Error("projected content missing")
	}
	if !strings.Contains(out, "use @old please") {
		t.Errorf("mention not rewritten: %q", out)
	}
}

func TestExpandSessionReferences_SelfReferenceRejected(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	store.StartSessionWithID("cur")
	writeAppSession(t, dir, "cur", []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, Text: "x"},
	})
	am := core.NewAgentManager(&config.Config{}, store, nil, core.NewSessionState(
		internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo}),
		event.MakeBus(16, 16, 16, 16), "")
	a := &App{subs: &subsystems{sessionStore: store, agentMgr: am}}

	_, err := expandSessionReferences(a.subs, "@[me](goa-session:cur)")
	if !errors.Is(err, agentic.ErrSessionReferenceSelf) {
		t.Errorf("err = %v, want ErrSessionReferenceSelf", err)
	}
}

func TestExpandSessionReferences_UnknownSessionRejected(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	a := &App{subs: &subsystems{sessionStore: store}}
	_, err := expandSessionReferences(a.subs, "@[x](goa-session:nope)")
	if !errors.Is(err, agentic.ErrSessionReferenceInvalid) {
		t.Errorf("err = %v, want ErrSessionReferenceInvalid", err)
	}
}

func TestExpandSessionReferences_NoMentionsNoop(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	a := &App{subs: &subsystems{sessionStore: store}}
	out, err := expandSessionReferences(a.subs, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if out != "plain" {
		t.Errorf("out = %q, want unchanged", out)
	}
}

// writeAppSession writes a raw JSONL session file directly.
func writeAppSession(t *testing.T, dir, sessionID string, events []agentic.OutputEvent) {
	t.Helper()
	sessionDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(sessionDir, sessionID+".jsonl")
	var b strings.Builder
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
}
