// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// When a turn fails with a non-connection error (e.g. HTTP 404 "model not
// found"), the user-facing bubble must state the real cause, not the generic
// and misleading "[connection error] Connection to the LLM server was lost."
// friendlyConnectionHint only substring-matches connection keywords, so any
// error without them falls through to the wrong "connection lost" message.
// Regression test (C3) driven through the filmstrip harness.
func TestSessionEnd_NonConnectionError_ShowsRealCause(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Simulate the fatal error text the agent emits for a 404 model-not-found.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventEnd,
		Text: "LLM request failed (not retryable): Error: 404 - model 'typo-model' not found (not_found_error)",
	})

	msgs := sc.chat.Messages()
	if len(msgs) == 0 {
		t.Fatal("expected a system message for the failed turn")
	}
	last := msgs[len(msgs)-1].Content

	if strings.Contains(last, "Connection to the LLM server was lost") {
		t.Errorf("non-connection error mislabeled as connection loss.\nGot bubble:\n%s", last)
	}
	if !strings.Contains(last, "404") && !strings.Contains(strings.ToLower(last), "not found") {
		t.Errorf("error bubble should state the real cause (404 / not found).\nGot bubble:\n%s", last)
	}
}

// A genuine connection error (timeout) still gets the friendly connection hint.
func TestSessionEnd_ConnectionError_KeepsFriendlyHint(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventEnd,
		Text: "LLM connection lost after retries: context deadline exceeded",
	})
	msgs := sc.chat.Messages()
	if len(msgs) == 0 {
		t.Fatal("expected a system message for the failed turn")
	}
	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, "timed out") && !strings.Contains(last, "connection error") {
		t.Errorf("connection error should keep the friendly hint.\nGot bubble:\n%s", last)
	}
}
