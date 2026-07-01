// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ask

import (
	"errors"
	"testing"
)

func TestAskUserQuestionSchema(t *testing.T) {
	tool := &AskUserQuestionTool{}
	schema := tool.Schema()
	if schema.Name != "ask_user_question" {
		t.Errorf("name = %q, want ask_user_question", schema.Name)
	}
}

func TestAskUserQuestionMissingQuestion(t *testing.T) {
	tool := &AskUserQuestionTool{}
	_, err := tool.Execute(`{"options":["yes","no"]}`)
	if err == nil {
		t.Fatal("expected error for missing question")
	}
}

func TestAskUserQuestionHandler(t *testing.T) {
	tool := &AskUserQuestionTool{
		Ask: func(q string, opts []string) (string, error) {
			return "yes", nil
		},
	}
	result, err := tool.Execute(`{"question":"Proceed?","options":["yes","no"]}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result != "[ask_user_question] Proceed?\nAnswer: yes" {
		t.Errorf("result = %q", result)
	}
}

func TestAskUserQuestionHandlerError(t *testing.T) {
	tool := &AskUserQuestionTool{
		Ask: func(q string, opts []string) (string, error) {
			return "", errors.New("user refused")
		},
	}
	_, err := tool.Execute(`{"question":"Proceed?"}`)
	if err == nil {
		t.Fatal("expected error")
	}
}
