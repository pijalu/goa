package goal

import (
	coregoal "github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/toolaccess"
	"strings"
	"testing"
)

func TestGoalTool_CreateRejectsOversizedObjective(t *testing.T) {
	mode := coregoal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	oversized := strings.Repeat("a", coregoal.MaxObjectiveLength+1)
	_, err := tool.Execute(`{"action":"create","objective":"` + oversized + `"}`)
	if err == nil || !strings.Contains(err.Error(), "objective_too_long") || mode.GetGoal().Goal != nil {
		t.Fatalf("unexpected oversized objective result: %v", err)
	}
}
func TestGoalTool_AccessSerializesConcurrentCalls(t *testing.T) {
	var acc toolaccess.Accessor = &GoalTool{}
	a := acc.Access(`{"action":"list"}`)
	b := acc.Access(`{"action":"update","status":"complete"}`)
	if a.Category == "" || !toolaccess.Conflict(a, b) {
		t.Fatalf("goal tool access must self-conflict: a=%+v b=%+v", a, b)
	}
}
func TestGoalTool_CreateWithTeam(t *testing.T) {
	mode := coregoal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	if _, err := tool.Execute(`{"action":"create","objective":"ship it","team":"alpha"}`); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || g.Team != "alpha" {
		t.Fatalf("team=%v", g)
	}
}
func TestGoalTool_CreateTeamTrimmed(t *testing.T) {
	mode := coregoal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	if _, err := tool.Execute(`{"action":"create","objective":"ship it","team":"  beta  "}`); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || g.Team != "beta" {
		t.Fatalf("team=%v", g)
	}
}
func TestGoalTool_CreateReportsTotalQueueDepth(t *testing.T) {
	mode := coregoal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	for _, obj := range []string{"active one", "old 1", "old 2"} {
		if _, err := tool.Execute(`{"action":"create","objective":"` + obj + `"}`); err != nil {
			t.Fatal(err)
		}
	}
	out, err := tool.Execute(`{"action":"create","objectives":["new 1","new 2"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"totalQueued":4`) {
		t.Fatalf("result=%s", out)
	}
}
