// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Regression (2026-08-04 LM Studio export): runtime system injections
// (context-reset boundary, goal/swarm reminders, ephemeral nudges) used to
// reach the provider as system-role messages at arbitrary positions. Strict
// Jinja chat templates (LM Studio/llama.cpp) reject the whole request with
// HTTP 400 "System message must be at the beginning", bricking the session.
// buildProviderHistory must downgrade every non-leading system message to a
// user-role context note (leading system prompt still travels separately
// via SystemPrompt).

func newSystemRoleTestAgent() *Agent {
	return NewAgent(Config{
		SystemPrompt: "SYS",
		Logger:       NewLogger(Error),
	})
}

func providerRoles(msgs []provider.Message) []provider.Role {
	roles := make([]provider.Role, len(msgs))
	for i, m := range msgs {
		roles[i] = m.Role
	}
	return roles
}

func assertNoProviderSystemRole(t *testing.T, msgs []provider.Message) {
	t.Helper()
	for i, m := range msgs {
		if m.Role == provider.RoleSystem {
			t.Errorf("provider message [%d] still has system role; content %q", i, extractTextFromBlocks(m.Content))
		}
	}
}

func TestBuildProviderHistoryDowngradesMidHistorySystemMessage(t *testing.T) {
	a := newSystemRoleTestAgent()
	a.SetHistory([]Message{
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi there"},
	})
	// Durable runtime notice lands at the end of the conversation.
	a.InjectSystemMessage("The user cancelled the current goal.")

	msgs := a.buildProviderHistory()

	wantRoles := []provider.Role{provider.RoleUser, provider.RoleAssistant, provider.RoleUser}
	got := providerRoles(msgs)
	if len(got) != len(wantRoles) {
		t.Fatalf("provider message count = %d, want %d (roles %v)", len(got), len(wantRoles), got)
	}
	for i, want := range wantRoles {
		if got[i] != want {
			t.Errorf("provider message [%d] role = %v, want %v", i, got[i], want)
		}
	}
	assertNoProviderSystemRole(t, msgs)
	if text := extractTextFromBlocks(msgs[2].Content); text != "The user cancelled the current goal." {
		t.Errorf("downgraded note content = %q, want the injected text preserved", text)
	}
}

func TestBuildProviderHistoryDowngradesFreshContextResetNote(t *testing.T) {
	a := newSystemRoleTestAgent()
	// Fresh-context goal start (RunFresh): history cleared to the system
	// prompt, then the context-reset boundary note is injected — it used to
	// sit at payload index 1 as a second system message, which strict
	// templates already reject.
	a.SetHistory(nil)
	a.InjectSystemMessage("⟡ Context reset: this goal is running on a clean context.")
	a.history = append(a.history, Message{Type: Content, Role: User, Content: "continue the goal"})

	msgs := a.buildProviderHistory()

	wantRoles := []provider.Role{provider.RoleUser, provider.RoleUser}
	got := providerRoles(msgs)
	if len(got) != len(wantRoles) {
		t.Fatalf("provider message count = %d, want %d (roles %v)", len(got), len(wantRoles), got)
	}
	for i, want := range wantRoles {
		if got[i] != want {
			t.Errorf("provider message [%d] role = %v, want %v", i, got[i], want)
		}
	}
	assertNoProviderSystemRole(t, msgs)
}

func TestBuildProviderHistoryDowngradesEphemeralNudge(t *testing.T) {
	a := newSystemRoleTestAgent()
	a.SetHistory([]Message{
		{Type: Content, Role: User, Content: "do the task"},
		{Type: Content, Role: Assistant, Content: "partial answer"},
	})
	// Auto-continue / loop-recovery nudge appended mid-turn.
	a.InjectEphemeralSystemMessage("[goa-system] Internal control note: continue without repeating.")

	msgs := a.buildProviderHistory()

	wantRoles := []provider.Role{provider.RoleUser, provider.RoleAssistant, provider.RoleUser}
	got := providerRoles(msgs)
	if len(got) != len(wantRoles) {
		t.Fatalf("provider message count = %d, want %d (roles %v)", len(got), len(wantRoles), got)
	}
	for i, want := range wantRoles {
		if got[i] != want {
			t.Errorf("provider message [%d] role = %v, want %v", i, got[i], want)
		}
	}
	assertNoProviderSystemRole(t, msgs)
}
