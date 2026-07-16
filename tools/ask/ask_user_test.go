// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ask

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSchema_NameAndQuestionsRequired(t *testing.T) {
	tool := &AskUserQuestionTool{}
	s := tool.Schema()
	if s.Name != "ask_user_question" {
		t.Errorf("name = %q", s.Name)
	}
	if !strings.Contains(s.Description, "clarif") {
		t.Errorf("description should mention clarification: %q", s.Description)
	}
}

func TestExecute_MissingQuestions(t *testing.T) {
	tool := &AskUserQuestionTool{}
	if _, err := tool.Execute(`{"questions":[]}`); err == nil {
		t.Fatal("expected error for empty questions")
	}
}

func TestExecute_InvalidJSON(t *testing.T) {
	tool := &AskUserQuestionTool{}
	if _, err := tool.Execute(`{not json`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestExecute_MissingQuestionField(t *testing.T) {
	tool := &AskUserQuestionTool{Clarify: func(title, summary, question string, options []string, step, total int) (string, bool) {
		t.Fatal("clarify should not be called when question is empty")
		return "", false
	}}
	if _, err := tool.Execute(`{"questions":[{"title":"x"}]}`); err == nil {
		t.Fatal("expected missing_question error")
	}
}

func TestExecute_Series(t *testing.T) {
	calls := 0
	tool := &AskUserQuestionTool{Clarify: func(title, summary, question string, options []string, step, total int) (string, bool) {
		calls++
		switch calls {
		case 1:
			return "main", true
		case 2:
			return "2", true
		case 3:
			return "", false // skipped
		}
		return "", false
	}}
	out, err := tool.Execute(`{"questions":[
		{"title":"branch","question":"target?","options":["main","dev"]},
		{"question":"second?","options":["a","b"]},
		{"question":"optional?"}
	]}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 3 {
		t.Errorf("clarify called %d times, want 3", calls)
	}
	if !strings.Contains(out, "3 question(s) answered") {
		t.Errorf("summary missing: %q", out)
	}
	// Parse the JSON payload (after the header line).
	payload := strings.SplitN(out, "\n", 2)[1]
	var ans []clarifyAnswer
	if err := json.Unmarshal([]byte(payload), &ans); err != nil {
		t.Fatalf("parse answers: %v", err)
	}
	if len(ans) != 3 {
		t.Fatalf("got %d answers", len(ans))
	}
	if ans[0].Answer != "main" {
		t.Errorf("ans[0] = %q, want main", ans[0].Answer)
	}
	if ans[1].Answer != "b" { // "2" -> options[1]
		t.Errorf("ans[1] = %q, want b", ans[1].Answer)
	}
	if !ans[2].Skipped {
		t.Errorf("ans[2] should be skipped")
	}
}

func TestExecute_RequiredCancelled(t *testing.T) {
	tool := &AskUserQuestionTool{Clarify: func(title, summary, question string, options []string, step, total int) (string, bool) {
		return "", false // user cancelled
	}}
	_, err := tool.Execute(`{"questions":[{"question":"q","required":true}]}`)
	if err == nil {
		t.Fatal("expected cancelled error for required question")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention cancelled: %v", err)
	}
}

func TestExecute_NoHost(t *testing.T) {
	tool := &AskUserQuestionTool{}
	_, err := tool.Execute(`{"questions":[{"question":"q"}]}`)
	if err == nil {
		t.Fatal("expected no_host error")
	}
}

func TestCanonicalize_NumericAndExact(t *testing.T) {
	opts := []string{"main", "release-2.x"}
	if got := canonicalizeAnswer("1", opts, true); got != "main" {
		t.Errorf("numeric 1 = %q", got)
	}
	if got := canonicalizeAnswer("RELEASE-2.X", opts, true); got != "release-2.x" {
		t.Errorf("case-insensitive = %q", got)
	}
	if got := canonicalizeAnswer("custom-branch", opts, true); got != "custom-branch" {
		t.Errorf("free text = %q", got)
	}
	// Restricted mode: unknown answer maps to closest option.
	if got := canonicalizeAnswer("mai", opts, false); got != "main" {
		t.Errorf("closest = %q", got)
	}
}

func TestCanonicalize_OptionsCapped(t *testing.T) {
	big := make([]string, 50)
	for i := range big {
		big[i] = "opt"
	}
	tool := &AskUserQuestionTool{Clarify: func(_, _, _ string, options []string, _, _ int) (string, bool) {
		if len(options) > maxOptions {
			t.Errorf("options not capped: %d", len(options))
		}
		return "1", true
	}}
	_, _ = tool.Execute(`{"questions":[{"question":"q","options":["a","b","c","d","e","f","g","h"]}]}`)
}

func TestExecute_ProgressForwarded(t *testing.T) {
	type pos struct{ step, total int }
	var got []pos
	tool := &AskUserQuestionTool{Clarify: func(_, _, _ string, _ []string, step, total int) (string, bool) {
		got = append(got, pos{step, total})
		return "1", true
	}}
	if _, err := tool.Execute(`{"questions":[{"question":"a"},{"question":"b"},{"question":"c"}]}`); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []pos{{1, 3}, {2, 3}, {3, 3}}
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("call %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestExecuteContext_CancelledBeforeStart(t *testing.T) {
	tool := &AskUserQuestionTool{Clarify: func(_, _, _ string, _ []string, _, _ int) (string, bool) {
		t.Fatal("clarify must not run when ctx is already cancelled")
		return "", false
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.ExecuteContext(ctx, `{"questions":[{"question":"q"}]}`); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestExecuteContext_CancelUnblocksWaiting(t *testing.T) {
	// clarify blocks forever; ctx cancellation must release ExecuteContext
	// instead of hanging the agent turn (the "no output / context canceled" bug).
	release := make(chan struct{})
	tool := &AskUserQuestionTool{Clarify: func(_, _, _ string, _ []string, _, _ int) (string, bool) {
		<-release // parked until test ends
		return "", false
	}}
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tool.ExecuteContext(ctx, `{"questions":[{"question":"q"}]}`)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteContext did not return after cancellation")
	}
}

func TestExecute_TitleNormalized(t *testing.T) {
	var gotTitle string
	tool := &AskUserQuestionTool{Clarify: func(title, _, _ string, _ []string, _, _ int) (string, bool) {
		gotTitle = title
		return "", false
	}}
	if _, err := tool.Execute(`{"questions":[{"title":"Target branch","question":"q"}]}`); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotTitle != "Clarifications needed" {
		t.Errorf("title = %q, want 'Clarifications needed'", gotTitle)
	}
}
