// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/toolaccess"
)

// Compile-time assertion that GoalTool declares its resource access so the
// tool scheduler can serialize concurrent goal calls in request order
// (bugs.md must-fix #4: "When multiple goal tool calls are executed, the
// request order should be kept"). Goal calls all mutate the same shared
// goal-manager state, so they share a category and never run in parallel.
var _ toolaccess.Accessor = (*GoalTool)(nil)

// Access declares that every goal tool call touches a single shared resource
// (the goal manager). Because all goal calls share the "goal" category, the
// tool scheduler serializes them — preserving the model's request order and
// preventing races on shared goal state when multiple goal calls arrive in one
// turn.
func (t *GoalTool) Access(_ string) toolaccess.Access {
	return toolaccess.Access{Category: "goal"}
}

// GoalTool is the single goal-management tool exposed to the model. It
// consolidates create / update / get / set_budget behind one `action`
// dispatcher so the tool array stays small and stable for prompt caching
// (bugs.md S2): one fixed schema instead of four.
type GoalTool struct {
	Mode *goal.GoalMode
	// CreateAllowed reports whether autonomous goal creation is permitted. It
	// gates only the `create` action and only when NO goal is currently active
	// (bugs.md S2: all goal actions are allowed while a goal is running).
	CreateAllowed func() bool
	// AutoUnblock reports whether a model-blocked goal (with justification)
	// should auto-spawn an unblocking investigation goal in front of it. Nil =
	// enabled (the default). When it returns false, a justified block falls
	// back to plain blocking (goal parked, turn stops).
	AutoUnblock func() bool
	// FreshContextDefault reports the configured default context mode for new
	// goals (goals.fresh_context; default true = clean context). The
	// per-create `freshContext` arg overrides it; when the arg is omitted
	// this resolver supplies the default. Nil = default true.
	FreshContextDefault func() bool
	// Queue is the durable FIFO of upcoming goals. When set, the tool manages
	// goals as a todo-like list: `create` APPENDS to the queue by default when
	// a goal is already active (rather than failing), and the list/cancel/reorder
	// actions operate over the active goal + the queue. May be nil (queue-less
	// setups) — list actions then operate on the active goal only.
	Queue GoalQueue
	// VerifyTimeout reports the configured verify-command timeout
	// (goals.verify_timeout) for the live progress display at completion.
	// Nil = default 2m (bugs.md Bug A: the timeout must be clear to the user).
	VerifyTimeout func() time.Duration
	// challenged tracks that the previous complete request was intercepted
	// by the done-gate, so the confirming call knows verification is about
	// to run and can announce it (command + timeout) to the user.
	challenged bool
}

// GoalQueue is the subset of the durable goal queue the tool needs. It is
// satisfied by *core.GoalQueueStore.
type GoalQueue interface {
	Read() ([]goal.UpcomingGoal, error)
	AppendGoal(input goal.UpcomingGoalInput) ([]goal.UpcomingGoal, error)
	// PrependGoal inserts a goal at the FRONT of the queue (promoted next).
	// Used by the auto-unblock flow and the model's push-in-front create.
	PrependGoal(input goal.UpcomingGoalInput) ([]goal.UpcomingGoal, error)
	Remove(id string) ([]goal.UpcomingGoal, *goal.UpcomingGoal, error)
	// Clear removes every queued goal (cancel "all"). Queue operations emit
	// no goal events, so clearing before cancelling the active goal keeps
	// the clear event's successor promotion a no-op.
	Clear() ([]goal.UpcomingGoal, error)
	Move(id, direction string) ([]goal.UpcomingGoal, error)
	// Restore puts a removed goal back at the front of the queue. Used to
	// roll back a promote when the follow-up activation fails.
	Restore(item goal.UpcomingGoal) ([]goal.UpcomingGoal, error)
}

// goalArgs is the union of all per-action fields. `action` selects which
// subset is meaningful; the rest are validated per action in Execute.
type goalArgs struct {
	Action              string   `json:"action"`
	Objective           string   `json:"objective,omitempty"`
	Objectives          []string `json:"objectives,omitempty"`
	CompletionCriterion *string  `json:"completionCriterion,omitempty"`
	VerifyCommand       string   `json:"verifyCommand,omitempty"`
	Replace             bool     `json:"replace,omitempty"`
	// FreshContext is a tri-state per-create override: nil = use the
	// configured default (goals.fresh_context, default true = clean context);
	// explicit true/false wins for this goal.
	FreshContext *bool `json:"freshContext,omitempty"`
	// Handover is an optional continuity note for the successor goal (free
	// text, untrusted data). On `create` it becomes the new goal's handover;
	// the next goal's reminder shows it inside an <untrusted_handover> block.
	Handover string `json:"handover,omitempty"`
	// Team binds the created goal to a named agent team (TEAMS.md §5.1): while
	// the goal is active, the team's overlay is applied. Empty = inherit the
	// session-level team. Only meaningful for `create` (and create-derived
	// queue actions).
	Team string `json:"team,omitempty"`
	Status string `json:"status,omitempty"`
	// Terminal-answer contract (update): reason justifies the transition;
	// expectation states what unblocks a blocked goal.
	Reason      string   `json:"reason,omitempty"`
	Expectation string   `json:"expectation,omitempty"`
	Value       *float64 `json:"value,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	// Goal list management (todo-like): target a goal by id/name and reorder.
	GoalID    string `json:"goalId,omitempty"`
	Direction string `json:"direction,omitempty"`
	// Priority, when "front", inserts a created goal at the FRONT of the queue
	// (promoted next) instead of appending. Used to push an execution goal
	// ahead of the goal it unblocks.
	Priority string `json:"priority,omitempty"`
	// Todo management (framework-managed todo list for the goal).
	TodoTitle  string `json:"todoTitle,omitempty"`
	TodoID     string `json:"todoId,omitempty"`
	TodoStatus string `json:"todoStatus,omitempty"`
}

// Schema returns the tool schema.
func (t *GoalTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "goal",
		Description: GoalDescription(),
		Schema: map[string]any{
			"type":     "object",
			"required": []string{"action"},
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"create", "update", "get", "set_budget", "add_todo", "update_todo", "list", "cancel", "reorder", "postpone", "promote"},
					"description": "The goal operation to perform.",
				},
				"objective": map[string]any{
					"type":        "string",
					"description": "create: the objective to pursue (must have a verifiable end state).",
				},
				"objectives": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "create: add several goals at once; first starts active if none is, rest queue.",
				},
				"goalId": map[string]any{
					"type":        "string",
					"description": "cancel: target — omitted or \"current\" cancels the ACTIVE goal (a queued successor is promoted PAUSED, never auto-started), \"all\" also wipes the queue, otherwise the queued goal's ID or friendly name. reorder: the queued goal's ID or friendly name to act on.",
				},
				"direction": map[string]any{
					"type":        "string",
					"enum":        []string{"up", "down"},
					"description": "reorder: move the queued goal up or down one position.",
				},
				"priority": map[string]any{
					"type":        "string",
					"enum":        []string{"front"},
					"description": "create: \"front\" = insert at queue FRONT (promoted next) instead of appending; pushes an execution goal ahead of the one it unblocks.",
				},
				"completionCriterion": map[string]any{
					"type":        "string",
					"description": "create: how to verify the goal is complete.",
				},
				"verifyCommand": map[string]any{
					"type":        "string",
					"description": "create: machine-checkable done-condition (e.g. \"go test ./...\"). Done-gate runs it after confirmed completion: exit0=close, else stay active w/ output. Prefer over prose criterion when checkable by command.",
				},
				"replace": map[string]any{
					"type":        "boolean",
					"description": "create: replace an existing goal instead of failing.",
				},
				"freshContext": map[string]any{
					"type":        "boolean",
					"description": "create: run continuation turns on clean context (objective+handover only), not the current conversation; history kept in transcript, not sent to the agent. Default: goals.fresh_context config (true). false = keep current context.",
				},
				"handover": map[string]any{
					"type":        "string",
					"description": "create: free-text continuity note for the successor goal — what makes clean context sufficient. Recommended structure: State (done/verified w/ evidence), Decisions (constraints), Next steps (first actions), Risks/open questions, Carried limits (budget, verify command, completion criterion). Shown to the next goal as untrusted data (\"<untrusted_handover>\"), never instructions. Max 4096 chars.",
				},
				"team": map[string]any{
					"type":        "string",
					"description": "create: bind this goal to a named agent team (TEAMS.md §5.1) — while active, the team's overlay is applied. Empty/omitted = inherit the session-level team.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "complete", "paused", "blocked"},
					"description": "update: the lifecycle status to set.",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "update: justification for the transition. REQUIRED for `paused` (why the goal must yield) and `blocked` (the concrete blocker). For `complete`, carries the verification evidence and is required when the done-gate asks for it.",
				},
				"expectation": map[string]any{
					"type":        "string",
					"description": "update: REQUIRED for `blocked` — exactly what input or change from the user/environment will unblock the goal.",
				},
				"value": map[string]any{
					"type":        "number",
					"description": "set_budget: the positive numeric budget value.",
				},
				"unit": map[string]any{
					"type":        "string",
					"enum":        []string{"turns", "tokens", "milliseconds", "seconds", "minutes", "hours"},
					"description": "set_budget: the unit for the budget value.",
				},
				"todoTitle": map[string]any{
					"type":        "string",
					"description": "add_todo: title of the task to add to the goal's todo list.",
				},
				"todoId": map[string]any{
					"type":        "string",
					"description": "update_todo: ID of the todo item to update.",
				},
				"todoStatus": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "in_progress", "done"},
					"description": "update_todo: the new status for the todo item.",
				},
			},
			"additionalProperties": false,
		},
	}
}

// Execute parses the input and dispatches to the action handler.
func (t *GoalTool) Execute(input string) (string, error) {
	res, err := t.ExecuteWithResult(input)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}

// ExecuteContextWithResult implements agentic.ContextResultTool: the ctx
// carries the execution-progress emitter so a goal completion can ANNOUNCE
// the verify command it is about to run (exact command + timeout) instead of
// sitting silent for up to 2 minutes (bugs.md Bug A), while the ToolResult
// keeps the StopTurn signal for terminal statuses.
func (t *GoalTool) ExecuteContextWithResult(ctx context.Context, input string) (agentic.ToolResult, error) {
	return t.executeWithResult(ctx, input)
}

// ExecuteWithResult implements agentic.ResultTool and carries the StopTurn
// signal for terminal update statuses.
func (t *GoalTool) ExecuteWithResult(input string) (agentic.ToolResult, error) {
	return t.executeWithResult(context.Background(), input)
}

// executeWithResult is the ctx-carrying dispatch shared by Execute,
// ExecuteContext and ExecuteWithResult.
func (t *GoalTool) executeWithResult(ctx context.Context, input string) (agentic.ToolResult, error) {
	var args goalArgs
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input", fmt.Errorf("invalid goal input: %w", err))
	}
	action := inferAction(args)
	handler, ok := t.actionHandlers()[action]
	if !ok {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_action",
			fmt.Errorf("invalid goal action %q: must be create, update, get, set_budget, add_todo, update_todo, list, cancel, reorder, postpone, or promote", args.Action))
	}
	return handler(ctx, args)
}

// actionHandlers maps each goal action to its handler. The uniform ctx
// signature keeps the dispatcher flat; handlers that ignore ctx use `_`.
func (t *GoalTool) actionHandlers() map[string]func(context.Context, goalArgs) (agentic.ToolResult, error) {
	wrap := func(h func(goalArgs) (agentic.ToolResult, error)) func(context.Context, goalArgs) (agentic.ToolResult, error) {
		return func(_ context.Context, a goalArgs) (agentic.ToolResult, error) { return h(a) }
	}
	return map[string]func(context.Context, goalArgs) (agentic.ToolResult, error){
		"create":      wrap(t.handleCreate),
		"update":      t.handleUpdate,
		"get":         wrap(func(goalArgs) (agentic.ToolResult, error) { return t.handleGet() }),
		"set_budget":  wrap(t.handleSetBudget),
		"add_todo":    wrap(t.handleAddTodo),
		"update_todo": wrap(t.handleUpdateTodo),
		"list":        wrap(func(goalArgs) (agentic.ToolResult, error) { return t.handleList() }),
		"cancel":      wrap(t.handleCancel),
		"reorder":     wrap(t.handleReorder),
		"postpone":    wrap(t.handlePostpone),
		"promote":     wrap(t.handlePromote),
	}
}

// inferAction returns the effective action for a call. `action` always wins
// when present; when omitted (a common model slip — e.g. sending only
// {"status":"blocked",...}), the intended action is inferred from whichever
// payload fields are set, so the call does what the model obviously meant
// instead of erroring (bugs.md "Goal management tool issue").
func inferAction(args goalArgs) string {
	if args.Action != "" {
		return args.Action
	}
	for _, rule := range actionInferenceRules {
		if rule.match(args) {
			return rule.action
		}
	}
	return "get"
}

// actionInferenceRules maps payload-field presence to the intended action, in
// priority order (first match wins).
var actionInferenceRules = []struct {
	match  func(goalArgs) bool
	action string
}{
	{func(a goalArgs) bool { return a.Status != "" }, "update"},
	{func(a goalArgs) bool { return a.Objective != "" || len(a.Objectives) > 0 }, "create"},
	{func(a goalArgs) bool { return a.TodoTitle != "" }, "add_todo"},
	{func(a goalArgs) bool { return a.TodoID != "" || a.TodoStatus != "" }, "update_todo"},
	{func(a goalArgs) bool { return a.GoalID != "" && a.Direction != "" }, "reorder"},
	{func(a goalArgs) bool { return a.GoalID != "" }, "cancel"},
	{func(a goalArgs) bool { return a.Value != nil || a.Unit != "" }, "set_budget"},
}

func (t *GoalTool) handleAddTodo(args goalArgs) (agentic.ToolResult, error) {
	if args.TodoTitle == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"add_todo\" requires a non-empty \"todoTitle\""))
	}
	item, err := t.Mode.AddGoalTodo(args.TodoTitle, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "add_todo_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"todo": item})
	return agentic.ToolResult{Output: string(out)}, nil
}

func (t *GoalTool) handleUpdateTodo(args goalArgs) (agentic.ToolResult, error) {
	if args.TodoID == "" || args.TodoStatus == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"update_todo\" requires \"todoId\" and \"todoStatus\" (pending|in_progress|done)"))
	}
	snapshot, err := t.Mode.UpdateGoalTodo(args.TodoID, args.TodoStatus, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "update_todo_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"goal": goal.ForModel(snapshot)})
	return agentic.ToolResult{Output: string(out)}, nil
}

func (t *GoalTool) handleCreate(args goalArgs) (agentic.ToolResult, error) {
	// Gather the objectives: single `objective` or batch `objectives`.
	objectives := make([]string, 0, 1+len(args.Objectives))
	if args.Objective != "" {
		objectives = append(objectives, args.Objective)
	}
	objectives = append(objectives, args.Objectives...)
	if len(objectives) == 0 {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"create\" requires \"objective\" or a non-empty \"objectives\" array"))
	}
	// Execution-time gate (bugs.md S2): autonomous creation is blocked only
	// when the feature flag is off AND no goal is active. All actions are
	// allowed while a goal is running.
	if t.CreateAllowed != nil && !t.CreateAllowed() {
		return agentic.ToolResult{}, goalToolErr("goal", "create_disabled",
			fmt.Errorf("autonomous goal creation is disabled; the user can start one with /goal"))
	}

	// Single-objective with replace keeps the legacy replace semantics exactly.
	if len(objectives) == 1 && args.Replace {
		return t.createActive(args, objectives[0], true)
	}

	// Todo-like default: ADD to the goal list. The first objective becomes the
	// active goal only when NO goal exists at all; every objective after that
	// (and all of them when a goal exists — active, paused, or blocked) is
	// appended to the queue — never an implicit replace. Checking existence
	// (not just active status) matters: GetActiveGoal hides paused/blocked
	// goals while CreateGoal rejects any existing state, so an active-only
	// check makes create fail with "a goal already exists" behind a parked
	// goal (bugs.md "Goal management tool issue").
	var activated *goal.GoalSnapshot
	queued := 0
	for _, obj := range objectives {
		if activated == nil && t.Mode.GetGoal().Goal == nil {
			res, err := t.createActive(args, obj, false)
			if err != nil {
				return res, err
			}
			snap := t.Mode.GetActiveGoal()
			activated = snap
			continue
		}
		if err := t.enqueue(obj, args.CompletionCriterion, optionalText(args.VerifyCommand), t.resolveFreshContext(args), args.Priority == "front", optionalText(args.Handover), strings.TrimSpace(args.Team)); err != nil {
			return agentic.ToolResult{}, goalToolErr("goal", "create_failed", err)
		}
		queued++
	}
	return t.createResult(activated, queued)
}

// resolveFreshContext returns the effective context mode for a create call:
// the explicit freshContext arg wins; otherwise the configured default
// (goals.fresh_context via FreshContextDefault; default true = clean context).
func (t *GoalTool) resolveFreshContext(args goalArgs) bool {
	if args.FreshContext != nil {
		return *args.FreshContext
	}
	if t.FreshContextDefault != nil {
		return t.FreshContextDefault()
	}
	return true
}

// createActive starts obj as the active goal. replace mirrors the legacy
// replace-on-create behaviour.
func (t *GoalTool) createActive(args goalArgs, objective string, replace bool) (agentic.ToolResult, error) {
	// Creation entry point: reject an oversized objective with the
	// point-to-a-markdown-doc hint so the model restructures its request
	// instead of hitting a wall later at resume/promotion time.
	if err := goal.ValidateObjective(objective); err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "objective_too_long", err)
	}
	snapshot, err := t.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           objective,
		CompletionCriterion: args.CompletionCriterion,
		VerifyCommand:       optionalText(args.VerifyCommand),
		Replace:             replace,
		FreshContext:        t.resolveFreshContext(args),
		Team:                strings.TrimSpace(args.Team),
		Handoff:             optionalText(args.Handover),
	}, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "create_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"goal": goal.ForModel(snapshot)})
	return agentic.ToolResult{Output: string(out)}, nil
}

// enqueue appends a goal to the durable queue. It requires a wired queue.
// The shared criterion, verify command, and handover apply to every objective
// in the call. front=true inserts at the FRONT of the queue (promoted next).
func (t *GoalTool) enqueue(objective string, criterion *string, verifyCommand *string, freshContext bool, front bool, handover *string, team string) error {
	if t.Queue == nil {
		return fmt.Errorf("a goal is already active and no goal queue is available; use replace to start a new one")
	}
	input := goal.UpcomingGoalInput{
		Objective:           objective,
		CompletionCriterion: criterion,
		VerifyCommand:       verifyCommand,
		FreshContext:        freshContext,
		Team:                team,
		Handoff:             handover,
	}
	var err error
	if front {
		_, err = t.Queue.PrependGoal(input)
	} else {
		_, err = t.Queue.AppendGoal(input)
	}
	return err
}

// createResult builds the create result, reporting the active goal and how
// many objectives were queued.
func (t *GoalTool) createResult(activated *goal.GoalSnapshot, queued int) (agentic.ToolResult, error) {
	payload := map[string]any{"queued": queued}
	if activated != nil {
		payload["goal"] = goal.ForModel(*activated)
	}
	out, _ := json.Marshal(payload)
	return agentic.ToolResult{Output: string(out)}, nil
}

// handleList returns the active goal plus the queued goals as a todo-like list.
func (t *GoalTool) handleList() (agentic.ToolResult, error) {
	payload := map[string]any{}
	if g := t.Mode.GetGoal().Goal; g != nil {
		payload["active"] = goal.ForModel(*g)
	} else {
		payload["active"] = nil
	}
	queued := []goal.UpcomingGoal{}
	if t.Queue != nil {
		if q, err := t.Queue.Read(); err == nil {
			queued = q
		}
	}
	payload["queued"] = queued
	payload["count"] = len(queued)
	out, _ := json.Marshal(payload)
	return agentic.ToolResult{Output: string(out)}, nil
}

// handleCancel deletes goal(s). goalId empty or "current" cancels the ACTIVE
// goal (the host promotes the queued successor PAUSED — a cancel never
// auto-starts the next goal); "all" additionally wipes the queue; any other
// value removes a queued goal by ID or friendly name.
func (t *GoalTool) handleCancel(args goalArgs) (agentic.ToolResult, error) {
	switch strings.ToLower(strings.TrimSpace(args.GoalID)) {
	case "", "current":
		return t.cancelActive(false)
	case "all":
		return t.cancelActive(true)
	default:
		return t.cancelQueued(args.GoalID)
	}
}

// cancelActive cancels the ACTIVE goal. With wipeQueue the queued goals are
// removed first: queue operations emit no goal events, so the clear event's
// successor promotion then finds an empty queue and stays a no-op (the same
// ordering /goal:cancel:all uses).
func (t *GoalTool) cancelActive(wipeQueue bool) (agentic.ToolResult, error) {
	cleared := 0
	if wipeQueue && t.Queue != nil {
		goals, err := t.Queue.Clear()
		if err != nil {
			return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", err)
		}
		cleared = len(goals)
	}
	if t.Mode.GetGoal().Goal == nil {
		if wipeQueue && cleared > 0 {
			out, _ := json.Marshal(map[string]any{"cancelled": nil, "queueCleared": cleared})
			return agentic.ToolResult{Output: string(out)}, nil
		}
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed",
			fmt.Errorf("no active goal to cancel"))
	}
	cancelled, err := t.Mode.CancelGoal(goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", err)
	}
	payload := map[string]any{"cancelled": goal.ForModel(cancelled)}
	if wipeQueue {
		payload["queueCleared"] = cleared
	}
	out, _ := json.Marshal(payload)
	return agentic.ToolResult{
		Output:   string(out) + "\nActive goal cancelled. A queued successor is promoted PAUSED — it does NOT auto-start; the user resumes it explicitly.",
		StopTurn: true,
	}, nil
}

// cancelQueued removes a queued goal by ID or friendly name.
func (t *GoalTool) cancelQueued(idOrName string) (agentic.ToolResult, error) {
	if t.Queue == nil {
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", fmt.Errorf("no goal queue available"))
	}
	id, err := t.resolveQueuedID(idOrName)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", err)
	}
	_, removed, err := t.Queue.Remove(id)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"cancelled": removed})
	return agentic.ToolResult{Output: string(out)}, nil
}

// handleReorder moves a queued goal up or down one position.
func (t *GoalTool) handleReorder(args goalArgs) (agentic.ToolResult, error) {
	if args.GoalID == "" || (args.Direction != "up" && args.Direction != "down") {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"reorder\" requires \"goalId\" and \"direction\" (up|down)"))
	}
	if t.Queue == nil {
		return agentic.ToolResult{}, goalToolErr("goal", "reorder_failed", fmt.Errorf("no goal queue available"))
	}
	id, err := t.resolveQueuedID(args.GoalID)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "reorder_failed", err)
	}
	goals, err := t.Queue.Move(id, args.Direction)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "reorder_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"queued": goals})
	return agentic.ToolResult{Output: string(out)}, nil
}

// handlePostpone demotes the active goal to the BACK of the queue so the next
// scheduled goal starts (bugs.md "Goal scheduling"). It is the model's
// deprioritize primitive: the demoted goal keeps its objective, criterion,
// verify command and context mode, and the clear event drives the host's
// auto-promotion of the new queue head — exactly as after a completion.
// With an empty queue the goal simply parks in the queue until promoted.
func (t *GoalTool) handlePostpone(args goalArgs) (agentic.ToolResult, error) {
	snap := t.Mode.GetGoal().Goal
	if snap == nil {
		return agentic.ToolResult{}, goalToolErr("goal", "postpone_failed",
			fmt.Errorf("no active goal to postpone"))
	}
	if t.Queue == nil {
		return agentic.ToolResult{}, goalToolErr("goal", "postpone_failed",
			fmt.Errorf("no goal queue available"))
	}
	if _, err := t.Queue.AppendGoal(goal.UpcomingGoalInput{
		Objective:           snap.Objective,
		CompletionCriterion: snap.CompletionCriterion,
		VerifyCommand:       snap.VerifyCommand,
		FreshContext:        snap.FreshContext,
		Handoff:             snap.Handoff,
	}); err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "postpone_failed", err)
	}
	// Runtime actor: a postpone is a framework RESCHEDULE, not a user/model
	// cancellation — the goal survives at the back of the queue. The actor
	// matters: the host applies the successor policy from the clear event's
	// actor, and only runtime/framework clears keep the start-the-next-goal
	// behavior a postpone promises (mirrors the unblock flow below).
	if _, err := t.Mode.CancelGoal(goal.GoalActorRuntime); err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "postpone_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"postponed": goal.ForModel(*snap), "position": "back"})
	return agentic.ToolResult{
		Output:   string(out) + "\nGoal moved to the back of the queue; the next queued goal starts now.",
		StopTurn: true,
	}, nil
}

// handlePromote activates a queued goal NOW (bugs.md "Goal scheduling"): the
// model's prioritize primitive. The current goal (if any) is demoted to the
// FRONT of the queue so it resumes right after, and the chosen queued goal
// becomes active atomically (replace semantics inside GoalMode).
func (t *GoalTool) handlePromote(args goalArgs) (agentic.ToolResult, error) {
	if args.GoalID == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"promote\" requires \"goalId\" (queued goal ID or name)"))
	}
	if t.Queue == nil {
		return agentic.ToolResult{}, goalToolErr("goal", "promote_failed", fmt.Errorf("no goal queue available"))
	}
	id, err := t.resolveQueuedID(args.GoalID)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "promote_failed", err)
	}
	_, promoted, err := t.Queue.Remove(id)
	if err != nil || promoted == nil {
		if err == nil {
			err = fmt.Errorf("queued goal %q not found", args.GoalID)
		}
		return agentic.ToolResult{}, goalToolErr("goal", "promote_failed", err)
	}
	// Demote the current goal to the FRONT of the queue so it resumes next.
	var demoted *goal.GoalSnapshot
	if snap := t.Mode.GetGoal().Goal; snap != nil {
		demoted = snap
		if _, err := t.Queue.PrependGoal(goal.UpcomingGoalInput{
			Objective:           snap.Objective,
			CompletionCriterion: snap.CompletionCriterion,
			VerifyCommand:       snap.VerifyCommand,
			FreshContext:        snap.FreshContext,
			Handoff:             snap.Handoff,
		}); err != nil {
			_, _ = t.Queue.Restore(*promoted)
			return agentic.ToolResult{}, goalToolErr("goal", "promote_failed", err)
		}
	}
	snap, err := t.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           promoted.Objective,
		Name:                promoted.Name,
		CompletionCriterion: promoted.CompletionCriterion,
		VerifyCommand:       promoted.VerifyCommand,
		FreshContext:        promoted.FreshContext,
		Handoff:             promoted.Handoff,
		Replace:             true, // atomically clears the demoted goal
	}, goal.GoalActorModel)
	if err != nil {
		_, _ = t.Queue.Restore(*promoted)
		return agentic.ToolResult{}, goalToolErr("goal", "promote_failed", err)
	}
	payload := map[string]any{"promoted": goal.ForModel(snap)}
	if demoted != nil {
		payload["demoted"] = goal.ForModel(*demoted)
		payload["demotedPosition"] = "front"
	}
	out, _ := json.Marshal(payload)
	return agentic.ToolResult{
		Output:   string(out) + "\nQueued goal activated; the previous goal waits at the front of the queue.",
		StopTurn: true,
	}, nil
}

// resolveQueuedID maps a goalId that may be a queue ID or a friendly name to
// the queue ID, so the model can reference goals by either.
func (t *GoalTool) resolveQueuedID(idOrName string) (string, error) {
	goals, err := t.Queue.Read()
	if err != nil {
		return "", err
	}
	for _, g := range goals {
		if g.ID == idOrName || g.Name == idOrName {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("no queued goal with ID or name %q", idOrName)
}

func (t *GoalTool) handleUpdate(ctx context.Context, args goalArgs) (agentic.ToolResult, error) {
	if args.Status == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"update\" requires \"status\" (active|complete|paused|blocked)"))
	}
	handlers := map[string]func(goalArgs) (agentic.ToolResult, error){
		"active":  t.updateActive,
		"paused":  t.updatePaused,
		"blocked": t.updateBlocked,
	}
	if args.Status == "complete" {
		res, err := t.updateComplete(ctx, args)
		if err != nil {
			return agentic.ToolResult{}, goalToolErr("goal", "update_failed", err)
		}
		return res, nil
	}
	handler, ok := handlers[args.Status]
	if !ok {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_status",
			fmt.Errorf("invalid goal status %q: must be active, complete, paused, or blocked", args.Status))
	}
	res, err := handler(args)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "update_failed", err)
	}
	return res, nil
}

func (t *GoalTool) updateActive(args goalArgs) (agentic.ToolResult, error) {
	if _, err := t.Mode.ResumeGoal(goal.GoalReasonInput{Reason: optionalReason(args)}, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{Output: "Goal resumed."}, nil
}

// updatePaused parks the goal. A justification is mandatory: pausing stops
// all autonomous work until the user resumes, so an unexplained pause forces
// the user to say "please continue" — exactly what goal mode exists to avoid.
func (t *GoalTool) updatePaused(args goalArgs) (agentic.ToolResult, error) {
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("status \"paused\" requires \"reason\" justifying why the goal must yield. If you can still make progress, keep working instead; if an external blocker prevents progress, use status \"blocked\" with \"reason\" and \"expectation\""))
	}
	input := goal.GoalReasonInput{Reason: &reason, Expectation: optionalExpectation(args)}
	if _, err := t.Mode.PauseGoal(input, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{Output: "Goal paused: " + reason, StopTurn: true}, nil
}

// updateBlocked stops the goal on an external blocker. Both the concrete
// blocker (reason) and the unblock condition (expectation) are mandatory so
// the user knows exactly what to provide, and the next turn can auto-resume
// once it arrives.
func (t *GoalTool) updateBlocked(args goalArgs) (agentic.ToolResult, error) {
	reason := strings.TrimSpace(args.Reason)
	expectation := strings.TrimSpace(args.Expectation)
	if reason == "" || expectation == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("status \"blocked\" requires \"reason\" (the concrete blocker) AND \"expectation\" (exactly what input or change unblocks the goal)"))
	}
	input := goal.GoalReasonInput{Reason: &reason, Expectation: &expectation}
	blocked, err := t.Mode.MarkBlocked(input, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, err
	}
	if blocked == nil {
		return agentic.ToolResult{Output: "No active goal to block."}, nil
	}

	// Auto-unblock: when the model supplies a justification (reason +
	// expectation), the framework enqueues an investigation goal IN FRONT of
	// the blocked goal, forcing the search for a solution before the user is
	// asked for guidance. Falls back to plain blocking when no queue is wired.
	if out, ok := t.enqueueUnblockGoal(*blocked, reason, expectation); ok {
		return out, nil
	}

	return agentic.ToolResult{
		Output:   fmt.Sprintf("Goal marked blocked: %s. Waiting for: %s. Tell the user what you need; when their reply supplies it or asks to continue, resume with goal action \"update\", status \"active\".", reason, expectation),
		StopTurn: true,
	}, nil
}

// enqueueUnblockGoal demotes the just-blocked goal A back onto the front of
// the queue, then activates an "unblocking" investigation goal U in front of
// it. U's objective embeds A's blocker and the investigate→execute-or-block
// contract: find a solution and push an execution goal in front, or — only
// when no solution exists — block U itself and ask the user for guidance.
// Returns (result, true) on success; (_, false) when auto-unblock is disabled
// or no queue is available.
func (t *GoalTool) enqueueUnblockGoal(blocked goal.GoalSnapshot, reason, expectation string) (agentic.ToolResult, bool) {
	if t.Queue == nil || (t.AutoUnblock != nil && !t.AutoUnblock()) {
		return agentic.ToolResult{}, false
	}
	// Re-queue A at the FRONT so it resumes right after the unblocking goal
	// completes. Its block context rides along in the objective so the
	// eventual resume turn sees why it stopped.
	requeueObjective := blocked.Objective
	if requeueObjective == "" {
		requeueObjective = "(resume blocked goal)"
	}
	if _, err := t.Queue.PrependGoal(goal.UpcomingGoalInput{
		Objective:           requeueObjective,
		CompletionCriterion: blocked.CompletionCriterion,
		VerifyCommand:       blocked.VerifyCommand,
	}); err != nil {
		return agentic.ToolResult{}, false
	}
	// Clear A from active so U can start. Runtime actor: this is a framework
	// transition, not a user cancellation.
	if _, err := t.Mode.CancelGoal(goal.GoalActorRuntime); err != nil {
		return agentic.ToolResult{}, false
	}
	// Activate the unblocking investigation goal.
	uObjective := buildUnblockObjective(requeueObjective, reason, expectation)
	if _, err := t.Mode.CreateGoal(goal.CreateGoalInput{
		Objective: uObjective,
	}, goal.GoalActorRuntime); err != nil {
		return agentic.ToolResult{}, false
	}
	return agentic.ToolResult{
		Output: fmt.Sprintf("Goal blocked: %s. An unblocking goal was started to investigate solutions before asking you for input.", reason),
	}, true
}

// buildUnblockObjective composes the investigation goal's objective. It forces
// the model to search for a way forward and encodes the two allowed outcomes:
// (1) solution found → create an execution goal with priority "front" and
// complete this goal; (2) no solution → block THIS goal with reason +
// expectation to ask the user for guidance.
func buildUnblockObjective(blockedObjective, reason, expectation string) string {
	return fmt.Sprintf(`UNBLOCKING INVESTIGATION — find a solution for a blocked goal.

The goal "%s" was blocked because: %s
It was waiting for: %s

Your ONLY job is to determine whether this blocker can be solved without user input:
1. INVESTIGATE the blocker. Read code, run commands, search, experiment — do real work to find a concrete path forward.
2. If you find a viable solution: create a new EXECUTION goal that implements it, using goal action "create" with priority "front" (so it runs before the blocked goal resumes), then mark THIS investigation goal complete with the solution as evidence.
3. ONLY if no solution is possible without the user: mark THIS goal blocked with "reason" (why it cannot be solved autonomously) and "expectation" (exactly what you need from the user). Do NOT block prematurely — exhaust autonomous options first.`,
		blockedObjective, reason, expectation)
}

// updateComplete closes the goal through the done-gate (goals.done_gate),
// machine verification (verify command), and judge. In verify mode with a
// recorded completion criterion, the first request is intercepted: the goal
// stays active and the tool returns the verification challenge WITHOUT
// stopping the turn, so the model audits the criterion and re-calls complete
// with the evidence in `reason`. A confirmed request then runs the recorded
// verify command (exit 0 required) and judge; a failure keeps the goal
// active with the failure detail, escalating to blocked at the configured
// streak cap. Without a criterion (or gate off), completion is immediate.
//
// Transparency (bugs.md Bug A): the confirming call ANNOUNCES the verify
// command before running it (exact command + timeout, via the execution
// progress emitter), and a successful completion returns the full evidence
// block (command, exit, elapsed, timeout, output tail) so the user can
// follow exactly what validated the goal.
func (t *GoalTool) updateComplete(ctx context.Context, args goalArgs) (agentic.ToolResult, error) {
	t.announceVerification(ctx)
	input := goal.GoalReasonInput{Reason: optionalReason(args)}
	result, err := t.Mode.RequestComplete(context.Background(), input, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, err
	}
	switch result.Outcome {
	case goal.CompleteChallenged:
		t.challenged = true
		return agentic.ToolResult{Output: goal.BuildVerificationChallenge(*result.Snapshot)}, nil
	case goal.CompleteVerifyFailed:
		t.challenged = false
		return agentic.ToolResult{Output: goal.BuildVerifyFailureMessage(result), StopTurn: result.Failure != nil && result.Failure.Escalated}, nil
	case goal.CompleteClosed:
		t.challenged = false
		return agentic.ToolResult{Output: "Goal marked complete." + formatVerifyEvidence(result.Verification) + formatOpenTodosReminder(result.Snapshot), StopTurn: true}, nil
	default:
		t.challenged = false
		return agentic.ToolResult{Output: "No active goal to complete."}, nil
	}
}

// announceVerification emits a live progress update naming the verify command
// and the configured timeout right before the confirming completion runs it.
// It fires only when the previous request was challenged AND a verify command
// is recorded — the exact situation where the tool call would otherwise sit
// silent for up to the full timeout.
func (t *GoalTool) announceVerification(ctx context.Context) {
	if !t.challenged {
		return
	}
	emit := agentic.ProgressFromContext(ctx)
	if emit == nil {
		return
	}
	snap := t.Mode.GetGoal().Goal
	if snap == nil || snap.VerifyCommand == nil {
		return
	}
	timeout := 2 * time.Minute
	if t.VerifyTimeout != nil {
		timeout = t.VerifyTimeout()
	}
	emit(fmt.Sprintf("Running goal verification (timeout %s):\n$ %s", timeout, *snap.VerifyCommand))
}

// formatOpenTodosReminder appends the open-todos reminder to a completion
// result (bugs.md "when a goal is achieved: if there are pending todos, the
// framework should remind the model of the open todos"). A gated completion
// (recorded criterion) already requires every todo done, so this fires for
// criterion-less completions. Todos are contained by the goal and die with
// it — the reminder exists so unfinished work is not silently dropped: the
// model can schedule a follow-up goal if the work is still needed.
func formatOpenTodosReminder(snap *goal.GoalSnapshot) string {
	if snap == nil {
		return ""
	}
	var open []string
	for _, todo := range snap.Todos {
		if todo.Status != goal.TodoDone {
			open = append(open, todo.Title)
		}
	}
	if len(open) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nReminder: %d todo(s) were still open when the goal completed:", len(open))
	for _, title := range open {
		fmt.Fprintf(&b, "\n- %s", title)
	}
	b.WriteString("\nTodos do not escape the goal — if any of this work is still needed, create a follow-up goal for it now.")
	return b.String()
}

// formatVerifyEvidence renders the post-completion evidence block: the exact
// command, how long it took, the applied timeout, and its output tail.
func formatVerifyEvidence(v *goal.VerifyEvidence) string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nVerification passed in %s (timeout %s):\n$ %s",
		formatMillis(v.DurationMs), formatMillis(v.TimeoutMs), v.Command)
	if out := strings.TrimRight(v.Output, "\n"); out != "" {
		b.WriteString("\n")
		b.WriteString(strings.Join(tailLines(out, 10), "\n"))
	}
	return b.String()
}

// formatMillis renders a millisecond duration compactly (e.g. 0.3s, 12.3s, 1m30s).
func formatMillis(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Truncate(100 * time.Millisecond).String()
}

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// optionalReason maps the raw reason arg to a trimmed *string (nil when blank).
func optionalReason(args goalArgs) *string {
	return optionalText(args.Reason)
}

// optionalText maps a raw string to a trimmed *string (nil when blank).
func optionalText(s string) *string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// optionalExpectation maps the raw expectation arg to a trimmed *string (nil when blank).
func optionalExpectation(args goalArgs) *string {
	return optionalText(args.Expectation)
}

func (t *GoalTool) handleGet() (agentic.ToolResult, error) {
	result := goal.ResultForModel(t.Mode.GetGoal())
	out, _ := json.Marshal(result)
	return agentic.ToolResult{Output: string(out)}, nil
}

func (t *GoalTool) handleSetBudget(args goalArgs) (agentic.ToolResult, error) {
	if args.Value == nil || args.Unit == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"set_budget\" requires \"value\" (number) and \"unit\""))
	}
	normalized := normalizeBudgetValue(*args.Value, args.Unit)
	limits, ok := budgetLimitsFromInput(normalized, args.Unit)
	if !ok {
		return agentic.ToolResult{Output: fmt.Sprintf("Goal budget not set: %s is not a reasonable goal budget.", formatBudget(normalized, args.Unit))}, nil
	}
	if _, err := t.Mode.SetBudgetLimits(limits, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "set_budget_failed", err)
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Goal budget set: %s.", formatBudget(normalized, args.Unit))}, nil
}

// IsRetryable reports whether the error is transient.
func (t *GoalTool) IsRetryable(err error) bool { return false }
