// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal"
)

// --- fixtures ---

// newPlanningTool returns a tool bound to a fresh plan in the planning phase.
func newPlanningTool(t *testing.T) (*PlanTool, *plan.Store) {
	t.Helper()
	store, err := plan.Create(t.TempDir(), "test plan")
	if err != nil {
		t.Fatalf("plan.Create: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewPlanTool(store), store
}

// newExecutingTool returns a tool whose plan has items and is executing, which
// is the only phase where the execution actions are legal.
func newExecutingTool(t *testing.T, titles ...string) (*PlanTool, *plan.Store, []string) {
	t.Helper()
	tool, store := newPlanningTool(t)
	ids := make([]string, 0, len(titles))
	for _, title := range titles {
		out, err := tool.Execute(`{"action":"add_item","title":` + jsonString(title) + `,"description":"desc of ` + title + `"}`)
		if err != nil {
			t.Fatalf("add_item %q: %v", title, err)
		}
		ids = append(ids, idFromAddOutput(t, out))
	}
	mustEnterExecution(t, store)
	return tool, store, ids
}

func mustEnterExecution(t *testing.T, store *plan.Store) {
	t.Helper()
	if err := store.SubmitRevision(); err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}
	if err := store.Approve(); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := store.StartExecution("run-1"); err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	if got := store.Plan().Status; got != plan.PlanExecuting {
		t.Fatalf("plan status = %q, want executing", got)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// idFromAddOutput extracts the item id from `Added item "title" (id)`.
func idFromAddOutput(t *testing.T, out string) string {
	t.Helper()
	open := strings.LastIndex(out, "(")
	closeIdx := strings.LastIndex(out, ")")
	if open < 0 || closeIdx <= open {
		t.Fatalf("cannot parse item id from %q", out)
	}
	return out[open+1 : closeIdx]
}

// assertInputTypeError requires that executing input fails with a ToolError of
// the given type, and returns that error.
func assertInputTypeError(t *testing.T, tool *PlanTool, input, wantType string) error {
	t.Helper()
	_, err := tool.Execute(input)
	if err == nil {
		t.Fatalf("expected a %s error for %s, got nil", wantType, input)
	}
	toolErr, ok := err.(*internal.ToolError)
	if !ok {
		t.Fatalf("error type = %T, want *internal.ToolError (%v)", err, err)
	}
	if toolErr.Type != wantType {
		t.Errorf("error type = %q, want %q (%v)", toolErr.Type, wantType, err)
	}
	return err
}

// --- planning-phase structural actions ---

// TestPlanTool_UpdateItem pins patch application plus the id/phase guards.
func TestPlanTool_UpdateItem(t *testing.T) {
	tool, store := newPlanningTool(t)
	addOut, err := tool.Execute(`{"action":"add_item","title":"first","description":"original"}`)
	if err != nil {
		t.Fatal(err)
	}
	id := idFromAddOutput(t, addOut)

	patch, _ := json.Marshal(map[string]any{"action": "update_item", "id": id, "title": "renamed", "description": "rewritten"})
	if _, err := tool.Execute(string(patch)); err != nil {
		t.Fatalf("update_item: %v", err)
	}
	item := store.Plan().Item(id)
	if item == nil || item.Title != "renamed" || item.Description != "rewritten" {
		t.Fatalf("item after update = %+v", item)
	}

	assertInputTypeError(t, tool, `{"action":"update_item","title":"x"}`, "invalid_input")
	assertInputTypeError(t, tool, `{"action":"update_item","id":"plan-1-item-99","title":"x"}`, "operation_failed")
}

// TestPlanTool_RemoveItem pins removal plus the id guard and unknown-id error.
func TestPlanTool_RemoveItem(t *testing.T) {
	tool, store := newPlanningTool(t)
	addOut, err := tool.Execute(`{"action":"add_item","title":"doomed"}`)
	if err != nil {
		t.Fatal(err)
	}
	id := idFromAddOutput(t, addOut)

	out, err := tool.Execute(`{"action":"remove_item","id":"` + id + `"}`)
	if err != nil {
		t.Fatalf("remove_item: %v", err)
	}
	if !strings.Contains(out, "Removed item") {
		t.Errorf("remove output = %q", out)
	}
	if store.Plan().Item(id) != nil {
		t.Error("item still present after remove_item")
	}

	assertInputTypeError(t, tool, `{"action":"remove_item"}`, "invalid_input")
	assertInputTypeError(t, tool, `{"action":"remove_item","id":"`+id+`"}`, "operation_failed")
}

// TestPlanTool_Reorder pins reordering, the empty-list guard, and rejection of
// an id set that does not match the plan.
func TestPlanTool_Reorder(t *testing.T) {
	tool, store := newPlanningTool(t)
	var ids []string
	for _, title := range []string{"one", "two", "three"} {
		out, err := tool.Execute(`{"action":"add_item","title":"` + title + `"}`)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, idFromAddOutput(t, out))
	}

	reversed, _ := json.Marshal([]string{ids[2], ids[1], ids[0]})
	if out, err := tool.Execute(`{"action":"reorder","ids":` + string(reversed) + `}`); err != nil {
		t.Fatalf("reorder: %v", err)
	} else if !strings.Contains(out, "reordered") {
		t.Errorf("reorder output = %q", out)
	}
	if first := store.Plan().Items[0].ID; first != ids[2] {
		t.Errorf("first item after reorder = %q, want %q", first, ids[2])
	}

	assertInputTypeError(t, tool, `{"action":"reorder","ids":[]}`, "invalid_input")
	assertInputTypeError(t, tool, `{"action":"reorder","ids":["nope"]}`, "operation_failed")
}

// TestPlanTool_GetIsPhaseAgnostic pins that get renders the plan in any phase.
func TestPlanTool_GetIsPhaseAgnostic(t *testing.T) {
	tool, store := newPlanningTool(t)
	if _, err := tool.Execute(`{"action":"add_item","title":"visible item"}`); err != nil {
		t.Fatal(err)
	}

	assertGetRenders(t, tool, "draft")

	mustEnterExecution(t, store)
	assertGetRenders(t, tool, "executing")
}

func assertGetRenders(t *testing.T, tool *PlanTool, phase string) {
	t.Helper()
	out, err := tool.Execute(`{"action":"get"}`)
	if err != nil {
		t.Fatalf("get in %s: %v", phase, err)
	}
	if !strings.HasPrefix(out, "# Plan:") {
		t.Errorf("get in %s did not render markdown: %q", phase, out)
	}
	if !strings.Contains(out, "visible item") {
		t.Errorf("get in %s omitted the item: %q", phase, out)
	}
}

// TestPlanTool_ResolveComment pins comment resolution and its guards.
func TestPlanTool_ResolveComment(t *testing.T) {
	tool, store := newPlanningTool(t)
	addOut, err := tool.Execute(`{"action":"add_item","title":"commented"}`)
	if err != nil {
		t.Fatal(err)
	}
	commentID, err := store.AddComment(idFromAddOutput(t, addOut), "please clarify")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	out, err := tool.Execute(`{"action":"resolve_comment","id":"` + commentID + `","note":"answered"}`)
	if err != nil {
		t.Fatalf("resolve_comment: %v", err)
	}
	if !strings.Contains(out, "resolved") {
		t.Errorf("resolve output = %q", out)
	}

	assertInputTypeError(t, tool, `{"action":"resolve_comment"}`, "invalid_input")
	assertInputTypeError(t, tool, `{"action":"resolve_comment","id":"plan-1-cmt-99"}`, "operation_failed")
}

// TestPlanTool_StructuralActionsRejectExecutionPhase pins that structural
// actions are refused once the plan is executing.
func TestPlanTool_StructuralActionsRejectExecutionPhase(t *testing.T) {
	tool, _, ids := newExecutingTool(t, "work")
	for name, input := range map[string]string{
		"add_item":        `{"action":"add_item","title":"late"}`,
		"update_item":     `{"action":"update_item","id":"` + ids[0] + `","title":"late"}`,
		"remove_item":     `{"action":"remove_item","id":"` + ids[0] + `"}`,
		"reorder":         `{"action":"reorder","ids":["` + ids[0] + `"]}`,
		"submit_review":   `{"action":"submit_review"}`,
		"resolve_comment": `{"action":"resolve_comment","id":"` + ids[0] + `"}`,
	} {
		assertInputTypeError(t, tool, input, "wrong_phase")
		_ = name
	}
}

// --- execution-phase actions ---

// TestPlanTool_StartItem pins the durable start contract: the item flips to
// in-progress and the item_started event records the role/agent defaults the
// orchestrator dispatches on (role "coder", agent "worker").
func TestPlanTool_StartItem(t *testing.T) {
	root := t.TempDir()
	store, err := plan.Create(root, "test plan")
	if err != nil {
		t.Fatalf("plan.Create: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	tool := NewPlanTool(store)

	addOut, err := tool.Execute(`{"action":"add_item","title":"work","description":"desc of work"}`)
	if err != nil {
		t.Fatal(err)
	}
	id := idFromAddOutput(t, addOut)
	mustEnterExecution(t, store)

	if _, err := tool.Execute(`{"action":"start_item","id":"` + id + `"}`); err != nil {
		t.Fatalf("start_item: %v", err)
	}
	item := store.Plan().Item(id)
	if item.Status != plan.ItemInProgress {
		t.Errorf("item status = %q, want in-progress", item.Status)
	}

	role, agentID := startedEventRoleAgent(t, root)
	if role != "coder" || agentID != "worker" {
		t.Errorf("item_started payload role/agent = %q/%q, want coder/worker", role, agentID)
	}

	assertInputTypeError(t, tool, `{"action":"start_item"}`, "invalid_input")
	assertInputTypeError(t, tool, `{"action":"start_item","id":"plan-1-item-99"}`, "operation_failed")
}

// startedEventRoleAgent reads the persisted event log and returns the role and
// agent recorded by the most recent item_started event. The event stream is the
// durable contract the orchestrator reads, so asserting it (rather than an
// in-memory item field) verifies what actually ships.
func startedEventRoleAgent(t *testing.T, planRoot string) (role, agentID string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(planRoot, "*", "events.jsonl"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no events.jsonl under %s (err=%v)", planRoot, err)
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, `"type":"item_started"`) {
				continue
			}
			var evt struct {
				Payload struct {
					Role    string `json:"role"`
					AgentID string `json:"agent_id"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				t.Fatalf("decode event line: %v", err)
			}
			role, agentID = evt.Payload.Role, evt.Payload.AgentID
		}
	}
	if role == "" {
		t.Fatal("no item_started event found in the persisted log")
	}
	return role, agentID
}

// TestPlanTool_StartItemBriefIncludesDependencyResults pins the worker brief
// content, including the dependency results section the worker needs.
func TestPlanTool_StartItemBriefIncludesDependencyResults(t *testing.T) {
	tool, store := newPlanningTool(t)
	depOut, err := tool.Execute(`{"action":"add_item","title":"dependency","description":"does the base work"}`)
	if err != nil {
		t.Fatal(err)
	}
	depID := idFromAddOutput(t, depOut)
	depJSON, _ := json.Marshal(map[string]any{"action": "add_item", "title": "dependent", "description": "builds on it", "depends_on": []string{depID}})
	depOut, err = tool.Execute(string(depJSON))
	if err != nil {
		t.Fatal(err)
	}
	itemID := idFromAddOutput(t, depOut)

	mustEnterExecution(t, store)
	if err := store.StartItem(depID, "coder", "worker"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteItem(depID, "dependency produced its result"); err != nil {
		t.Fatal(err)
	}

	out, err := tool.Execute(`{"action":"start_item","id":"` + itemID + `"}`)
	if err != nil {
		t.Fatalf("start_item: %v", err)
	}
	for _, want := range []string{"## dependent", "builds on it", "### Dependency results", "dependency produced its result", "Finish by calling task_outcome."} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q:\n%s", want, out)
		}
	}
}

// TestPlanTool_CompleteItem pins completion, its input guards, and the
// all-items-terminal notice.
func TestPlanTool_CompleteItem(t *testing.T) {
	tool, store, ids := newExecutingTool(t, "only item")
	if err := store.StartItem(ids[0], "coder", "worker"); err != nil {
		t.Fatal(err)
	}

	assertInputTypeError(t, tool, `{"action":"complete_item","id":"`+ids[0]+`"}`, "invalid_input")
	assertInputTypeError(t, tool, `{"action":"complete_item","result":"done"}`, "invalid_input")

	result, _ := json.Marshal(map[string]any{"action": "complete_item", "id": ids[0], "result": "implemented"})
	out, err := tool.Execute(string(result))
	if err != nil {
		t.Fatalf("complete_item: %v", err)
	}
	if !strings.Contains(out, "All items are terminal") || !strings.Contains(out, "call finish") {
		t.Errorf("terminal notice missing: %q", out)
	}
	if item := store.Plan().Item(ids[0]); item.Result != "implemented" {
		t.Errorf("item result = %q", item.Result)
	}
}

// TestPlanTool_BlockAndSkipItem pins the blocking/skipping paths, including the
// dependent warning and the input guards.
func TestPlanTool_BlockAndSkipItem(t *testing.T) {
	tool, store := newPlanningTool(t)
	firstOut, err := tool.Execute(`{"action":"add_item","title":"base"}`)
	if err != nil {
		t.Fatal(err)
	}
	firstID := idFromAddOutput(t, firstOut)
	depJSON, _ := json.Marshal(map[string]any{"action": "add_item", "title": "follower", "depends_on": []string{firstID}})
	secondOut, err := tool.Execute(string(depJSON))
	if err != nil {
		t.Fatal(err)
	}
	secondID := idFromAddOutput(t, secondOut)

	mustEnterExecution(t, store)
	if err := store.StartItem(firstID, "coder", "worker"); err != nil {
		t.Fatal(err)
	}

	assertInputTypeError(t, tool, `{"action":"block_item","id":"`+firstID+`"}`, "invalid_input")
	assertInputTypeError(t, tool, `{"action":"block_item","reason":"no id"}`, "invalid_input")

	blockJSON, _ := json.Marshal(map[string]any{"action": "block_item", "id": firstID, "reason": "missing credentials"})
	out, err := tool.Execute(string(blockJSON))
	if err != nil {
		t.Fatalf("block_item: %v", err)
	}
	if !strings.Contains(out, "missing credentials") {
		t.Errorf("block output = %q", out)
	}
	if !strings.Contains(out, "Dependents now unstartable") || !strings.Contains(out, secondID) {
		t.Errorf("dependent warning missing: %q", out)
	}

	if err := store.StartItem(secondID, "coder", "worker"); err == nil {
		t.Fatal("dependent item started despite its blocker")
	}

	assertInputTypeError(t, tool, `{"action":"skip_item"}`, "invalid_input")
	skipJSON, _ := json.Marshal(map[string]any{"action": "skip_item", "id": secondID, "reason": "obsolete"})
	if out, err := tool.Execute(string(skipJSON)); err != nil {
		t.Fatalf("skip_item: %v", err)
	} else if !strings.Contains(out, "skipped") {
		t.Errorf("skip output = %q", out)
	}
}

// TestPlanTool_ExecutionActionsRejectPlanningPhase pins that execution actions
// are refused while the plan is still in the planning phase.
func TestPlanTool_ExecutionActionsRejectPlanningPhase(t *testing.T) {
	tool, _ := newPlanningTool(t)
	out, err := tool.Execute(`{"action":"add_item","title":"not yet"}`)
	if err != nil {
		t.Fatal(err)
	}
	id := idFromAddOutput(t, out)
	for _, input := range []string{
		`{"action":"start_item","id":"` + id + `"}`,
		`{"action":"complete_item","id":"` + id + `","result":"x"}`,
		`{"action":"block_item","id":"` + id + `","reason":"x"}`,
		`{"action":"skip_item","id":"` + id + `"}`,
	} {
		assertInputTypeError(t, tool, input, "wrong_phase")
	}
}

// TestPlanTool_SubmitReviewStopsTurn pins the review hand-off contract.
func TestPlanTool_SubmitReviewStopsTurn(t *testing.T) {
	tool, store := newPlanningTool(t)
	if _, err := tool.Execute(`{"action":"add_item","title":"reviewed"}`); err != nil {
		t.Fatal(err)
	}
	result, err := tool.ExecuteWithResult(`{"action":"submit_review"}`)
	if err != nil {
		t.Fatalf("submit_review: %v", err)
	}
	if !result.StopTurn {
		t.Error("submit_review must stop the turn so the review pager can open")
	}
	if store.Plan().Status != plan.PlanInReview {
		t.Errorf("plan status = %q, want in_review", store.Plan().Status)
	}
}
