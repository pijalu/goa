package goal

import (
	"fmt"

	coregoal "github.com/pijalu/goa/core/goal"
)

type fakeQueue struct {
	goals []coregoal.UpcomingGoal
	n     int
}

func (q *fakeQueue) Read() ([]coregoal.UpcomingGoal, error) {
	return append([]coregoal.UpcomingGoal(nil), q.goals...), nil
}
func (q *fakeQueue) AppendGoal(i coregoal.UpcomingGoalInput) ([]coregoal.UpcomingGoal, error) {
	return q.insert(i, false)
}
func (q *fakeQueue) PrependGoal(i coregoal.UpcomingGoalInput) ([]coregoal.UpcomingGoal, error) {
	return q.insert(i, true)
}
func (q *fakeQueue) insert(i coregoal.UpcomingGoalInput, front bool) ([]coregoal.UpcomingGoal, error) {
	q.n++
	x := coregoal.UpcomingGoal{ID: fmt.Sprintf("q%d", q.n), Objective: i.Objective, CompletionCriterion: i.CompletionCriterion, VerifyCommand: i.VerifyCommand, FreshContext: i.FreshContext, Handoff: i.Handoff}
	if front {
		q.goals = append([]coregoal.UpcomingGoal{x}, q.goals...)
	} else {
		q.goals = append(q.goals, x)
	}
	return q.Read()
}
func (q *fakeQueue) Remove(id string) ([]coregoal.UpcomingGoal, *coregoal.UpcomingGoal, error) {
	for i, g := range q.goals {
		if g.ID == id {
			q.goals = append(q.goals[:i], q.goals[i+1:]...)
			out, _ := q.Read()
			return out, &g, nil
		}
	}
	return q.goals, nil, fmt.Errorf("queued goal %q not found", id)
}
func (q *fakeQueue) Clear() ([]coregoal.UpcomingGoal, error) {
	x := q.goals
	q.goals = nil
	return x, nil
}
func (q *fakeQueue) Move(id, d string) ([]coregoal.UpcomingGoal, error) {
	for i, g := range q.goals {
		if g.ID == id {
			j := i - 1
			if d == "down" {
				j = i + 1
			}
			if j >= 0 && j < len(q.goals) {
				q.goals[i], q.goals[j] = q.goals[j], q.goals[i]
			}
			break
		}
	}
	return q.Read()
}
func (q *fakeQueue) Restore(x coregoal.UpcomingGoal) ([]coregoal.UpcomingGoal, error) {
	q.goals = append([]coregoal.UpcomingGoal{x}, q.goals...)
	return q.Read()
}
