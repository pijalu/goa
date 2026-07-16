// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/core/tasks"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// ChatEmitter emits user-visible chat messages from a running swarm.
// Implemented by the app layer so the swarm tool can surface sub-agent
// activity in the conversation history without importing the TUI.
type ChatEmitter interface {
	Emit(from, to, content string)
}

// AgentSwarmTool spawns multiple sub-agents in parallel to work on a list of
// items. It mirrors kimi-code's AgentSwarm tool: a single prompt_template
// expanded per item, with validation, structured XML results, task-bus
// lifecycle tracking, and one-shot agent eviction.
type AgentSwarmTool struct {
	agentic.BaseTool
	Pool         *multiagent.AgentPool
	ModeResolver multiagent.ModeResolver
	TaskBus      *tasks.Bus
	SwarmState   *swarm.State
	// CurrentMode returns the caller's current mode. Planner mode may only
	// spawn plan sub-agents to prevent escaping planner restrictions.
	CurrentMode func() internal.ModeState
	// Emitter surfaces sub-agent lifecycle/output in the chat history.
	Emitter ChatEmitter
	// ProgressReporter receives a live text summary of sub-agent status so
	// the TUI can display it inside the running agent_swarm tool widget.
	ProgressReporter func(text string)
}

const (
	swarmPlaceholder       = "{{item}}"
	maxSwarmSubagents      = 128
	defaultSubagentType    = "coder"
	swarmBackgroundTimeout = 30 * time.Minute
)

type agentSwarmInput struct {
	Task           string   `json:"task"`
	Items          []string `json:"items"`
	SubagentType   string   `json:"subagent_type"`
	PromptTemplate string   `json:"prompt_template"`
}

// Schema returns the tool schema.
func (t *AgentSwarmTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "agent_swarm",
		Description: "Spawn parallel sub-agents.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "swarm task description",
				},
				"items": map[string]any{
					"type":        "array",
					"description": "values filling {{item}} in prompt_template, one sub-agent per item",
					"items":       map[string]any{"type": "string"},
				},
				"subagent_type": map[string]any{
					"type":        "string",
					"description": "coder (default)|explore|plan",
					"enum":        []string{"coder", "explore", "plan"},
				},
				"prompt_template": map[string]any{
					"type":        "string",
					"description": "prompt template with {{item}} placeholder",
				},
			},
			"required": []string{"task", "items"},
		},
	}
}

// Execute runs the swarm.
func (t *AgentSwarmTool) Execute(input string) (string, error) {
	p, err := t.parse(input)
	if err != nil {
		return "", err
	}
	if t.Pool == nil {
		return "", &internal.ToolError{Tool: "agent_swarm", Type: "not_configured", Detail: "Agent pool is not available", HintText: "Swarm execution is not configured."}
	}

	// Mark swarm mode active for the tool trigger. The turn-end hook decides
	// auto-exit; the tool trigger omits the enter reminder (the model is
	// already calling agent_swarm), matching kimi-code.
	if t.SwarmState != nil {
		t.SwarmState.Enter(swarm.ToolTrigger, p.Task)
	}

	// Planner mode is restricted to planning sub-agents only.
	if t.CurrentMode != nil {
		mode := t.CurrentMode()
		if mode.Major == internal.MajorPlanner && p.SubagentType != "" && p.SubagentType != "plan" {
			return "", &internal.ToolError{
				Tool: "agent_swarm", Type: "forbidden_subagent",
				Detail:   fmt.Sprintf("planner mode may only spawn plan sub-agents, not %q", p.SubagentType),
				HintText: "Use subagent_type=\"plan\" or switch to a coding mode.",
			}
		}
	}

	cfg := t.prepareConfig(p.SubagentType)
	results := t.runAll(p, cfg)
	return renderSwarmResults(p.Task, results), nil
}

func (t *AgentSwarmTool) parse(input string) (agentSwarmInput, error) {
	var p agentSwarmInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, &internal.ToolError{
			Tool: "agent_swarm", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide valid JSON with task and items.",
		}
	}
	if strings.TrimSpace(p.Task) == "" {
		return p, &internal.ToolError{Tool: "agent_swarm", Type: "missing_task", Detail: "task is required", HintText: "Provide a short task description."}
	}
	items := normalizeItems(p.Items)
	if len(items) == 0 {
		return p, &internal.ToolError{Tool: "agent_swarm", Type: "missing_items", Detail: "items is required", HintText: "Provide a list of items to process."}
	}
	if len(items) > maxSwarmSubagents {
		return p, &internal.ToolError{
			Tool: "agent_swarm", Type: "too_many_items",
			Detail:   fmt.Sprintf("agent_swarm supports at most %d sub-agents (got %d)", maxSwarmSubagents, len(items)),
			HintText: "Split the work or reduce the number of items.",
		}
	}
	template := strings.TrimSpace(p.PromptTemplate)
	if template == "" {
		return p, &internal.ToolError{
			Tool: "agent_swarm", Type: "missing_template",
			Detail:   "prompt_template is required when items are provided",
			HintText: "Provide a prompt_template containing the {{item}} placeholder.",
		}
	}
	if !strings.Contains(template, swarmPlaceholder) {
		return p, &internal.ToolError{
			Tool: "agent_swarm", Type: "bad_template",
			Detail:   fmt.Sprintf("prompt_template must contain the %s placeholder", swarmPlaceholder),
			HintText: "Add {{item}} where each item's value should be substituted.",
		}
	}
	// Reject duplicate expanded prompts — they would produce conflicting/
	// duplicated sub-agents (kimi-code parity).
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		expanded := strings.ReplaceAll(template, swarmPlaceholder, it)
		if seen[expanded] {
			return p, &internal.ToolError{
				Tool: "agent_swarm", Type: "duplicate_prompts",
				Detail:   fmt.Sprintf("two items expanded to the same prompt (%q)", expanded),
				HintText: "Ensure each item produces a distinct sub-agent prompt.",
			}
		}
		seen[expanded] = true
	}
	p.Items = items
	p.PromptTemplate = template
	return p, nil
}

func normalizeItems(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (t *AgentSwarmTool) prepareConfig(subagentType string) multiagent.AgentConfig {
	if subagentType == "" {
		subagentType = defaultSubagentType
	}
	if t.ModeResolver == nil {
		return multiagent.AgentConfig{}
	}
	major := multiagent.SubagentMajorMode(subagentType)
	spec, err := t.ModeResolver.Resolve(major)
	if err != nil {
		return multiagent.AgentConfig{}
	}
	return multiagent.AgentConfig{
		SystemPrompt: spec.Body,
		AllowedTools: spec.AllowedTools,
		Temperature:  spec.Temperature,
	}
}

// swarmItemResult is the per-item outcome rendered into the XML result.
type swarmItemResult struct {
	item    string
	outcome string // "completed" | "failed"
	body    string // result text or error message
}

// swarmProgress tracks the live status of each swarm item so the TUI can
// display per-sub-agent progress inside the running agent_swarm tool widget.
type swarmProgress struct {
	mu    sync.Mutex
	items map[string]string
	order []string
}

func newSwarmProgress(items []string) *swarmProgress {
	p := &swarmProgress{
		items: make(map[string]string, len(items)),
		order: make([]string, len(items)),
	}
	copy(p.order, items)
	for _, it := range items {
		p.items[it] = "pending"
	}
	return p
}

func (p *swarmProgress) set(item, status string) {
	p.mu.Lock()
	p.items[item] = status
	p.mu.Unlock()
}

func (p *swarmProgress) snapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	completed, failed := 0, 0
	var b strings.Builder
	for _, it := range p.order {
		status := p.items[it]
		switch status {
		case "completed":
			completed++
		case "failed":
			failed++
		}
		b.WriteString(fmt.Sprintf("  %s: %s\n", it, status))
	}
	summary := renderSwarmSummary(completed, failed)
	return fmt.Sprintf("🐝 %s (%d/%d)\n%s", summary, completed, len(p.order), b.String())
}

func (t *AgentSwarmTool) runAll(p agentSwarmInput, cfg multiagent.AgentConfig) []swarmItemResult {
	results := make([]swarmItemResult, len(p.Items))
	progress := newSwarmProgress(p.Items)
	if t.ProgressReporter != nil {
		t.ProgressReporter(progress.snapshot())
	}
	var wg sync.WaitGroup
	for i, item := range p.Items {
		wg.Add(1)
		go func(idx int, it string) {
			defer wg.Done()
			results[idx] = t.runOne(p.Task, it, p.PromptTemplate, cfg, progress)
		}(i, item)
	}
	wg.Wait()
	return results
}

func (t *AgentSwarmTool) reportProgress(progress *swarmProgress) {
	if t.ProgressReporter == nil {
		return
	}
	t.ProgressReporter(progress.snapshot())
}

func (t *AgentSwarmTool) emit(from, to, content string) {
	if t.Emitter == nil || content == "" {
		return
	}
	// Non-blocking: never let chat emission stall a sub-agent turn.
	t.Emitter.Emit(from, to, content)
}

func (t *AgentSwarmTool) runOne(task, item, template string, cfg multiagent.AgentConfig, progress *swarmProgress) swarmItemResult {
	prompt := strings.ReplaceAll(template, swarmPlaceholder, item)
	taskID := fmt.Sprintf("swarm-%d-%d", time.Now().UnixNano(), uniqueCounter())
	role := fmt.Sprintf("swarm-%s-%s", strings.ReplaceAll(task, " ", "-"), taskID)

	// Track on the shared task bus if available.
	if t.TaskBus != nil {
		t.TaskBus.Register(taskID, "agent_swarm", role, fmt.Sprintf("%s: %s", task, item))
		t.TaskBus.Start(taskID)
	}

	progress.set(item, "running")
	t.reportProgress(progress)

	t.emit(role, "user", fmt.Sprintf("🐝 sub-agent started: %s", item))

	// Use CreateEphemeralAgent so swarm sub-agents do not inherit the
	// foreground orchestrator's companion observer (which would otherwise route
	// their events into the companion renderer — the "companion · cycle" leak).
	agent, err := t.Pool.CreateEphemeralAgent(role, cfg)
	if err != nil {
		msg := fmt.Sprintf("create agent: %v", err)
		progress.set(item, "failed")
		t.reportProgress(progress)
		t.emit(role, "user", fmt.Sprintf("❌ sub-agent failed: %s\n%v", item, err))
		if t.TaskBus != nil {
			t.TaskBus.Fail(taskID, msg)
		}
		return swarmItemResult{item: item, outcome: "failed", body: msg}
	}

	// Accumulate the sub-agent's final output so the full conversation can be
	// emitted into the chat history once the sub-agent finishes.
	var resultBuf strings.Builder
	obs := agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
		if ev.Type == agentic.EventContent && ev.Role == agentic.Assistant && ev.Text != "" {
			resultBuf.WriteString(ev.Text)
		}
	})
	remove := agent.AddObserver(obs)
	defer remove()

	ctx, cancel := context.WithTimeout(context.Background(), swarmBackgroundTimeout)
	defer cancel()
	runErr := agent.Run(ctx, prompt)

	if runErr != nil {
		msg := fmt.Sprintf("%v", runErr)
		progress.set(item, "failed")
		t.reportProgress(progress)
		t.emit(role, "user", fmt.Sprintf("❌ sub-agent failed: %s\n%v", item, runErr))
		if t.TaskBus != nil {
			t.TaskBus.Fail(taskID, msg)
		}
		return swarmItemResult{item: item, outcome: "failed", body: msg}
	}
	result := resultBuf.String()
	progress.set(item, "completed")
	t.reportProgress(progress)

	displayResult := result
	if displayResult == "" {
		displayResult = "(no output)"
	}
	t.emit(role, "user", fmt.Sprintf("✅ sub-agent completed: %s\n%s", item, displayResult))
	if t.TaskBus != nil {
		t.TaskBus.Complete(taskID, result)
	}
	return swarmItemResult{item: item, outcome: "completed", body: result}
}

// renderSwarmResults renders a structured XML summary so the model can parse
// per-item outcomes. Format mirrors kimi-code's renderSwarmResults.
func renderSwarmResults(task string, results []swarmItemResult) string {
	var b strings.Builder
	completed, failed := 0, 0
	for _, r := range results {
		switch r.outcome {
		case "completed":
			completed++
		case "failed":
			failed++
		}
	}
	b.WriteString("<agent_swarm_result>\n")
	b.WriteString("<task>" + escapeXML(task) + "</task>\n")
	b.WriteString("<summary>" + renderSwarmSummary(completed, failed) + "</summary>\n")
	// Stable order by item for deterministic output (parallel runs otherwise
	// shuffle results).
	ordered := make([]swarmItemResult, len(results))
	copy(ordered, results)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].item < ordered[j].item })
	for _, r := range ordered {
		b.WriteString(fmt.Sprintf(`<subagent item=%q outcome=%q>`, r.item, r.outcome))
		b.WriteString(escapeXML(r.body))
		b.WriteString("</subagent>\n")
	}
	b.WriteString("</agent_swarm_result>")
	return b.String()
}

func renderSwarmSummary(completed, failed int) string {
	var parts []string
	if completed > 0 {
		parts = append(parts, fmt.Sprintf("completed: %d", completed))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("failed: %d", failed))
	}
	if len(parts) == 0 {
		return "no items"
	}
	return strings.Join(parts, ", ")
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// uniqueCounter returns a process-unique ascending id used to disambiguate
// swarm task ids spawned in the same nanosecond.
var swarmCounter struct {
	mu sync.Mutex
	n  uint64
}

func uniqueCounter() uint64 {
	swarmCounter.mu.Lock()
	defer swarmCounter.mu.Unlock()
	swarmCounter.n++
	return swarmCounter.n
}

// IsRetryable returns false.
func (t *AgentSwarmTool) IsRetryable(err error) bool { return false }

//go:embed agent_swarm.short.md agent_swarm.long.md
var agent_swarmDocs embed.FS

// ShortDoc returns a short doc string.
func (t *AgentSwarmTool) ShortDoc() string { return readDoc(agent_swarmDocs, "agent_swarm.short.md") }

// LongDoc returns a long doc string.
func (t *AgentSwarmTool) LongDoc() string { return readDoc(agent_swarmDocs, "agent_swarm.long.md") }

// Examples returns example inputs.
func (t *AgentSwarmTool) Examples() []string {
	return []string{
		`{"task":"Fix lint errors","items":["a.go","b.go"],"subagent_type":"coder","prompt_template":"Fix lint errors in {{item}}"}`,
	}
}
