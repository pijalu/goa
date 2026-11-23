// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

type statusMutationView struct {
	footer string
	events int
}

// executeStatusMutation runs a real tool and applies every resulting event to
// the same handlers used by the production readers. The rendered footer is
// returned separately from the rest of the screen so goal-bubble markers cannot
// satisfy a status-bar assertion accidentally.
func executeStatusMutation(t *testing.T, sc *uiScenario, bus *event.Bus, tool agentic.Tool, input, label string) statusMutationView {
	t.Helper()
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("%s: Execute: %v", label, err)
	}
	view := statusMutationView{}
	for draining := true; draining; {
		select {
		case ev := <-bus.Agent:
			sc.engine.ApplySync(func() {
				sc.app.handleAgentOutputEvent(&ev.Event)
				if ev.GoalUpdate != nil {
					sc.app.handleGoalUpdate(ev.GoalUpdate)
				}
			})
			view.events++
		case ev := <-bus.Footer:
			sc.engine.ApplySync(func() { sc.app.handleFooterEvent(ev) })
			view.events++
		default:
			draining = false
		}
	}
	sc.engine.RenderNow()
	sc.film.Capture(label, sc.engine.AgentFrame(), sc.status.Text())
	view.footer = ansi.Strip(strings.Join(sc.footer.Render(100), "\n"))
	return view
}

func newStatusMutationScenario(t *testing.T) (*uiScenario, *event.Bus, *core.GoalManager) {
	t.Helper()
	sc := newUIScenario(t, 100, 24)
	bus := event.MakeBus(32, 1, 1, 32)
	publisher := &goalEventPublisher{bus: bus}
	mode := goal.NewGoalMode(nil, publisher, nil, nil)
	manager := core.NewGoalManagerWithMode("", mode)
	manager.SetQueue(core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json")))
	sc.app.subs.events = bus
	sc.app.subs.goalManager = manager
	sc.engine.ApplySync(func() {
		sc.footer.SetData(tui.FooterData{Profile: "coding-posture", Mode: "yolo"})
	})
	return sc, bus, manager
}

func assertStatusMutation(t *testing.T, view statusMutationView, label string, want, unwanted []string) {
	t.Helper()
	if view.events == 0 {
		t.Errorf("%s: mutation published no status event", label)
	}
	for _, text := range want {
		if !strings.Contains(view.footer, text) {
			t.Errorf("%s: footer missing %q:\n%s", label, text, view.footer)
		}
	}
	for _, text := range unwanted {
		if strings.Contains(view.footer, text) {
			t.Errorf("%s: footer unexpectedly contains %q:\n%s", label, text, view.footer)
		}
	}
}

func TestFilmstrip_SessionTodoStatusUpdatesFooter(t *testing.T) {
	sc, bus, _ := newStatusMutationScenario(t)
	tool, ok := makeToolFactory(sc.app.subs)("todo_list")
	if !ok {
		t.Fatal("todo_list runtime factory unavailable")
	}

	steps := []struct {
		label string
		input string
		want  []string
		not   []string
	}{
		{"add first", `{"action":"add","description":"one"}`, []string{"⬩"}, nil},
		{"add second", `{"action":"add","description":"two"}`, []string{"⬩⬩"}, nil},
		{"add third", `{"action":"add","description":"three"}`, []string{"⬩⬩⬩"}, nil},
		{"add fourth", `{"action":"add","description":"four"}`, []string{"⬩⬩⬩+1"}, nil},
		{"start first", `{"action":"update","id":"todo-1","status":"in_progress"}`, []string{"⬩⬩⬩+1"}, nil},
		{"complete first", `{"action":"complete","id":"todo-1"}`, []string{"⬩⬩⬩"}, []string{"+1"}},
		{"remove second", `{"action":"remove","id":"todo-2"}`, []string{"⬩⬩"}, []string{"⬩⬩⬩"}},
		{"finish third", `{"action":"update","id":"todo-3","status":"done"}`, []string{"⬩"}, []string{"⬩⬩"}},
		{"clear", `{"action":"clear"}`, nil, []string{"⬩"}},
	}
	for _, step := range steps {
		view := executeStatusMutation(t, sc, bus, tool, step.input, step.label)
		assertStatusMutation(t, view, step.label, step.want, step.not)
	}
}

func TestFilmstrip_GoalAndTodoStatusUpdatesFooter(t *testing.T) {
	sc, bus, manager := newStatusMutationScenario(t)
	tool := newGoalTool(manager, func() bool { return true }, nil, nil, nil)

	steps := []struct {
		label string
		input string
		want  []string
		not   []string
	}{
		{"goal active", `{"action":"create","objective":"track status"}`, []string{"◈"}, []string{"⬩"}},
		{"todo pending", `{"action":"add_todo","todoTitle":"first"}`, []string{"◈", "⬩"}, nil},
		{"todo in progress", `{"action":"update_todo","todoId":"t1","todoStatus":"in_progress"}`, []string{"◈", "⬩"}, nil},
		{"todo done", `{"action":"update_todo","todoId":"t1","todoStatus":"done"}`, []string{"◈"}, []string{"⬩"}},
		{"second pending", `{"action":"add_todo","todoTitle":"second"}`, []string{"◈", "⬩"}, nil},
		{"goal paused", `{"action":"update","status":"paused","reason":"review"}`, nil, []string{"◈", "⬩"}},
		{"goal resumed", `{"action":"update","status":"active"}`, []string{"◈", "⬩"}, nil},
		{"second done", `{"action":"update_todo","todoId":"t2","todoStatus":"done"}`, []string{"◈"}, []string{"⬩"}},
		{"goal complete", `{"action":"update","status":"complete","reason":"validated"}`, nil, []string{"◈", "⬩"}},
	}
	for _, step := range steps {
		view := executeStatusMutation(t, sc, bus, tool, step.input, step.label)
		assertStatusMutation(t, view, step.label, step.want, step.not)
	}
}

func TestFilmstrip_SessionAndGoalTodoListsStayIsolated(t *testing.T) {
	sc, bus, manager := newStatusMutationScenario(t)
	todoTool, ok := makeToolFactory(sc.app.subs)("todo_list")
	if !ok {
		t.Fatal("todo_list runtime factory unavailable")
	}
	goalTool := newGoalTool(manager, func() bool { return true }, nil, nil, nil)

	view := executeStatusMutation(t, sc, bus, todoTool, `{"action":"add","description":"session"}`, "session before goal")
	assertStatusMutation(t, view, "session before goal", []string{"⬩"}, []string{"◈"})

	view = executeStatusMutation(t, sc, bus, goalTool, `{"action":"create","objective":"isolate lists"}`, "goal hides session list")
	assertStatusMutation(t, view, "goal hides session list", []string{"◈"}, []string{"⬩"})

	view = executeStatusMutation(t, sc, bus, goalTool, `{"action":"add_todo","todoTitle":"goal first"}`, "goal first")
	assertStatusMutation(t, view, "goal first", []string{"◈", "⬩"}, nil)
	view = executeStatusMutation(t, sc, bus, todoTool, `{"action":"add","description":"goal second"}`, "goal second through todo_list")
	assertStatusMutation(t, view, "goal second through todo_list", []string{"◈", "⬩⬩"}, nil)
	if got := len(todoTool.(*tools.TodoListTool).Items()); got != 1 {
		t.Fatalf("session list changed while goal active: got %d items, want 1", got)
	}

	for _, id := range []string{"t1", "t2"} {
		input := `{"action":"update_todo","todoId":"` + id + `","todoStatus":"done"}`
		executeStatusMutation(t, sc, bus, goalTool, input, "finish "+id)
	}
	view = executeStatusMutation(t, sc, bus, goalTool, `{"action":"update","status":"complete","reason":"validated"}`, "goal complete restores session")
	assertStatusMutation(t, view, "goal complete restores session", []string{"⬩"}, []string{"◈"})

	view = executeStatusMutation(t, sc, bus, todoTool, `{"action":"complete","id":"todo-1"}`, "session done")
	assertStatusMutation(t, view, "session done", nil, []string{"◈", "⬩"})
	t.Logf("session/goal status terminal filmstrip:\n%s", sc.filmstrip().Render())
}
