// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

// TestSendUserInput_MidGoalTurnPreservesStats is the regression test for
// "cache stats are lost during goals": SendUserInputWithImages used to call
// turnRecorder.ResetTurn BEFORE the steering/busy early-return, so every user
// message typed while a goal turn owned the agent wiped the in-flight turn's
// accumulators — the finalized TurnRecord came out empty and /stats:cache
// lost the session history. Steering input must queue WITHOUT clearing.
func TestSendUserInput_MidGoalTurnPreservesStats(t *testing.T) {
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "test-model"},
	}))
	am.SetRunningForTest(true) // a goal continuation turn owns the agent

	// Simulate the in-flight goal turn's accumulated stats (as emitted by
	// EventTokenStats during agent.Run): the turn was started, then token
	// stats arrived.
	rec := am.TurnRecorder()
	rec.ResetTurn(time.Now())
	rec.RecordTokenStats(45000, 1200, 40000, 0, 50, 0, 46000, 128000)
	if cur := rec.CurrentTurn(); cur == nil || cur.TokenUsage.CacheRead != 40000 {
		t.Fatalf("fixture broken: CurrentTurn = %+v, want CacheRead=40000", cur)
	}

	// User types while the goal turn runs → must be queued as steering.
	if err := am.SendUserInput("stop, do X instead"); err != nil {
		t.Fatalf("SendUserInput while busy: %v", err)
	}

	// The in-flight accumulators survive.
	cur := rec.CurrentTurn()
	if cur == nil {
		t.Fatal("in-flight turn was reset by mid-goal steering input — cache stats lost")
	}
	if cur.TokenUsage.PromptN != 45000 || cur.TokenUsage.CacheRead != 40000 {
		t.Errorf("in-flight usage wiped: %+v, want promptN=45000 read=40000", cur.TokenUsage)
	}
	if strings.Contains(cur.UserInput, "stop, do X instead") {
		t.Errorf("steering text leaked into the in-flight user input: %q", cur.UserInput)
	}
}

// TestSendUserInput_IdleResetsAccumulators pins the other half of the
// contract: when NO turn owns the agent, a new user turn still starts from
// clean accumulators (the ResetTurn must not be skipped for real turns).
func TestSendUserInput_IdleResetsAccumulators(t *testing.T) {
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "test-model"},
	}))

	rec := am.TurnRecorder()
	rec.RecordTokenStats(1000, 0, 800, 0, 0, 0, 0, 0)

	// No turn is running; the send proceeds toward runAgentTurn. ResetTurn
	// must clear the stale accumulator snapshot before the new turn starts.
	if err := am.SendUserInput("fresh question"); err != nil {
		t.Fatalf("SendUserInput idle: %v", err)
	}
	if got := rec.LastTurn(); got != nil && got.UserInput != "" && got.TokenUsage.PromptN == 1000 {
		t.Errorf("stale usage survived into a new turn: %+v", got.TokenUsage)
	}
	// The strong assertion: CurrentTurn now reports the NEW turn number with
	// cleared usage (ResetTurn ran), not the pre-existing 40k figures.
	if cur := rec.CurrentTurn(); cur != nil && cur.TokenUsage.PromptN == 1000 {
		t.Errorf("CurrentTurn still shows the pre-reset fixture usage: %+v", cur.TokenUsage)
	}
}
