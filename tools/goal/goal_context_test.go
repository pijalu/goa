package goal

import (
	coregoal "github.com/pijalu/goa/core/goal"
	"testing"
)

func TestGoalTool_CreateFreshContextDefaults(t *testing.T) {
	m := coregoal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(m, func() bool { return true })
	if _, e := tool.Execute(`{"action":"create","objective":"default"}`); e != nil {
		t.Fatal(e)
	}
	if g := m.GetGoal().Goal; g == nil || !g.FreshContext {
		t.Fatalf("goal=%+v", g)
	}
	if _, e := tool.Execute(`{"action":"create","objective":"reuse","freshContext":false,"replace":true}`); e != nil {
		t.Fatal(e)
	}
	if g := m.GetGoal().Goal; g == nil || g.FreshContext {
		t.Fatalf("explicit false goal=%+v", g)
	}
}
func TestGoalTool_CreateFreshContextResolver(t *testing.T) {
	m := coregoal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(m, func() bool { return true })
	tool.FreshContextDefault = func() bool { return false }
	if _, e := tool.Execute(`{"action":"create","objective":"reuse"}`); e != nil {
		t.Fatal(e)
	}
	if g := m.GetGoal().Goal; g == nil || g.FreshContext {
		t.Fatalf("goal=%+v", g)
	}
}
func TestGoalTool_EnqueueFreshContext(t *testing.T) {
	m := coregoal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: m, CreateAllowed: func() bool { return true }, Queue: q}
	for _, x := range []string{"first", "second"} {
		if _, e := tool.Execute(`{"action":"create","objective":"` + x + `"}`); e != nil {
			t.Fatal(e)
		}
	}
	if len(q.goals) != 1 || !q.goals[0].FreshContext {
		t.Fatalf("queue=%+v", q.goals)
	}
}
