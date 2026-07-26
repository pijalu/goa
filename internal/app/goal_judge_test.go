// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/testutil"
)

func TestParseJudgeVerdict(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		pass    bool
		wantErr bool
	}{
		{name: "pass with rationale", reply: "The evidence cites a passing test run.\nVERDICT: PASS", pass: true},
		{name: "fail with rationale", reply: "No concrete evidence was given.\nVERDICT: FAIL", pass: false},
		{name: "lowercase verdict", reply: "looks fine\nverdict: pass", pass: true},
		{name: "trailing punctuation", reply: "ok\nVERDICT: PASS.", pass: true},
		{name: "blank lines before verdict", reply: "reasoning\n\n\nVERDICT: FAIL\n", pass: false},
		{name: "no verdict line", reply: "I think it is done.", wantErr: true},
		{name: "empty reply", reply: "", wantErr: true},
		{name: "verdict text only", reply: "VERDICT: FAIL", pass: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseJudgeVerdict(tc.reply)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.reply)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tc.pass {
				t.Errorf("Pass = %v, want %v", v.Pass, tc.pass)
			}
		})
	}
}

func TestParseJudgeVerdict_RationaleExcludesVerdictLine(t *testing.T) {
	v, err := parseJudgeVerdict("line one\nline two\nVERDICT: PASS")
	if err != nil {
		t.Fatal(err)
	}
	if v.Rationale != "line one\nline two" {
		t.Errorf("Rationale = %q", v.Rationale)
	}
}

// TestGoalJudge_Judge drives the judge end-to-end against the simulated
// provider: the streamed verdict must be parsed, and the case text must
// contain objective, criterion, and evidence.
func TestGoalJudge_Judge(t *testing.T) {
	sim := testutil.NewSimulatedProvider([]testutil.SimulatedResponse{
		{Content: "The evidence shows a passing verify command.\nVERDICT: PASS"},
	})
	agenticprovider.RegisterApiProvider(sim)

	mdl := agenticprovider.Model{Api: sim.API(), Name: "sim-judge"}
	j := &goalJudge{
		resolveModel: func() (agenticprovider.Model, error) { return mdl, nil },
		buildOpts:    func() agenticprovider.StreamOptions { return agenticprovider.StreamOptions{} },
	}
	verdict, err := j.Judge(context.Background(), goal.JudgeInput{
		Objective:           "fix tests",
		CompletionCriterion: "go test ./... passes",
		Evidence:            "ran go test ./... — all green",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Pass {
		t.Errorf("Pass = false, want true")
	}
	if !strings.Contains(verdict.Rationale, "passing verify command") {
		t.Errorf("Rationale = %q", verdict.Rationale)
	}
}

// TestGoalJudge_Judge_FailVerdict covers the rejecting path.
func TestGoalJudge_Judge_FailVerdict(t *testing.T) {
	sim := testutil.NewSimulatedProvider([]testutil.SimulatedResponse{
		{Content: "Vague claim without evidence.\nVERDICT: FAIL"},
	})
	agenticprovider.RegisterApiProvider(sim)

	mdl := agenticprovider.Model{Api: sim.API(), Name: "sim-judge"}
	j := &goalJudge{
		resolveModel: func() (agenticprovider.Model, error) { return mdl, nil },
		buildOpts:    func() agenticprovider.StreamOptions { return agenticprovider.StreamOptions{} },
	}
	verdict, err := j.Judge(context.Background(), goal.JudgeInput{
		Objective:           "x",
		CompletionCriterion: "y",
		Evidence:            "done",
	})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Pass {
		t.Error("Pass = true, want false")
	}
}

// TestGoalJudge_Judge_UnparseableReplyIsError pins the fail-open contract:
// an unparseable judge reply surfaces as an error, which runVerification
// treats as fail-open (telemetry goal_judge_error, completion proceeds).
func TestGoalJudge_Judge_UnparseableReplyIsError(t *testing.T) {
	sim := testutil.NewSimulatedProvider([]testutil.SimulatedResponse{
		{Content: "I cannot decide."},
	})
	agenticprovider.RegisterApiProvider(sim)

	mdl := agenticprovider.Model{Api: sim.API(), Name: "sim-judge"}
	j := &goalJudge{
		resolveModel: func() (agenticprovider.Model, error) { return mdl, nil },
		buildOpts:    func() agenticprovider.StreamOptions { return agenticprovider.StreamOptions{} },
	}
	if _, err := j.Judge(context.Background(), goal.JudgeInput{Objective: "x", CompletionCriterion: "y", Evidence: "z"}); err == nil {
		t.Fatal("expected error for unparseable judge reply")
	}
}

func TestJudgeCaseCap(t *testing.T) {
	input := goal.JudgeInput{
		Objective:           strings.Repeat("o", judgeCaseCap),
		CompletionCriterion: "c",
		Evidence:            "e",
	}
	if got := judgeCase(input); len(got) > judgeCaseCap+len("\n[... case truncated ...]") {
		t.Errorf("judge case length = %d, exceeds cap", len(got))
	}
}

func TestNewGoalJudge_Off(t *testing.T) {
	for _, value := range []string{"", "off", "OFF", " off "} {
		j, err := newGoalJudge(value, nil)
		if err != nil || j != nil {
			t.Errorf("newGoalJudge(%q) = (%v, %v), want (nil, nil)", value, j, err)
		}
	}
}

func TestNewGoalJudge_Errors(t *testing.T) {
	if _, err := newGoalJudge("same", nil); err == nil {
		t.Error("expected error for judge without provider manager")
	}
	if _, err := newGoalJudge("bogus", nil); err == nil {
		t.Error("expected error for unknown judge value")
	}
	if _, err := newGoalJudge("model:", nil); err == nil {
		t.Error("expected error for empty model id")
	}
}
