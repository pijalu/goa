// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skillrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/skillrunner/tools"
)

// NewAgentWithSkills creates a new agent with skills loaded from the specified directories.
// This is a convenience function that creates the skills runner internally.
//
// Parameters:
//   - cfg: Agent configuration (provider, tools, logger, system prompt)
//   - skillsDirs: Directories to scan for skills (each containing SKILL.md)
//   - workDir: Working directory for skill file operations
//
// Example:
//
//	agent, err := NewAgentWithSkills(
//	    agentic.Config{Provider: provider, SystemPrompt: "You are helpful."},
//	    []string{"./skills"},
//	    ".",
//	)
func NewAgentWithSkills(cfg agentic.Config, skillsDirs []string, workDir string) (*agentic.Agent, error) {
	return NewAgentWithSkillsLoader(cfg, NewFileSkillsLoader(skillsDirs), workDir)
}

// NewAgentWithSkillsLoader creates a new agent with skills loaded via the provided loader.
// This allows using embedded skills (go:embed) or custom loader implementations.
//
// Example with embedded skills:
//
//	//go:embed skills
//	var embeddedSkills embed.FS
//
//	agent, err := NewAgentWithSkillsLoader(
//	    agentic.Config{Provider: provider, SystemPrompt: "You are helpful."},
//	    NewEmbeddedSkillsLoader(embeddedSkills, "skills"),
//	    ".",
//	)
func NewAgentWithSkillsLoader(cfg agentic.Config, loader *SkillsLoader, workDir string) (*agentic.Agent, error) {
	executionMode := executionModeFromConfig(cfg)

	var allTools []agentic.Tool
	var systemPrompt string

	if executionMode == ExecutionModeInline {
		allTools = buildInlineTools(cfg, loader, workDir)
		systemPrompt = buildInlineSystemPrompt(cfg, loader)
	} else {
		runner, err := newSubAgentRunner(cfg, loader, workDir, executionMode)
		if err != nil {
			return nil, err
		}
		allTools = appendToolIfMissingByType(cfg.Tools, runner, func(t agentic.Tool) bool {
			_, ok := t.(*Runner)
			return ok
		})
		systemPrompt = buildSubAgentSystemPrompt(cfg, runner)
	}

	return agentic.NewAgent(agentic.Config{
		Model:              cfg.Model,
		APIKey:             cfg.APIKey,
		StreamOptions:      cfg.StreamOptions,
		SystemPrompt:       systemPrompt,
		Logger:             cfg.Logger,
		Tools:              allTools,
		SkillExecutionMode: cfg.SkillExecutionMode,
		MaxToolRepeatTotal:       cfg.MaxToolRepeatTotal,
		MaxToolRepeatConsecutive: cfg.MaxToolRepeatConsecutive,
	}), nil
}

func executionModeFromConfig(cfg agentic.Config) ExecutionMode {
	if cfg.SkillExecutionMode == agentic.SkillExecutionModeInline {
		return ExecutionModeInline
	}
	return ExecutionModeSubAgent
}

func buildInlineTools(cfg agentic.Config, loader *SkillsLoader, workDir string) []agentic.Tool {
	learner := NewSkillLearner(loader)
	allTools := appendToolIfMissingByType(cfg.Tools, learner, func(t agentic.Tool) bool {
		_, ok := t.(*SkillLearner)
		return ok
	})
	return appendMissingSkillTools(allTools, workDir, cfg.Logger)
}

func appendMissingSkillTools(allTools []agentic.Tool, workDir string, logger *agentic.Logger) []agentic.Tool {
	skillTools := tools.Tools(workDir, logger)
	toolNames := make(map[string]bool)
	for _, t := range allTools {
		toolNames[t.Schema().Name] = true
	}
	for _, st := range skillTools {
		if !toolNames[st.Schema().Name] {
			allTools = append(allTools, st)
		}
	}
	return allTools
}

func buildInlineSystemPrompt(cfg agentic.Config, loader *SkillsLoader) string {
	learner := NewSkillLearner(loader)
	return buildSystemPrompt(cfg.SystemPrompt, learner.GenerateSkillsSection())
}

func newSubAgentRunner(cfg agentic.Config, loader *SkillsLoader, workDir string, executionMode ExecutionMode) (*Runner, error) {
	runner, err := NewRunner(Config{
		Loader:             loader,
		Model:              cfg.Model,
		StreamOptions:      cfg.StreamOptions,
		WorkDir:            workDir,
		Logger:             cfg.Logger,
		ExecutionMode:      executionMode,
		MaxToolRepeatTotal:       cfg.MaxToolRepeatTotal,
		MaxToolRepeatConsecutive: cfg.MaxToolRepeatConsecutive,
		ContextCompression: cfg.ContextCompression,
		ReasoningEffort:    cfg.ReasoningEffort,
		ToolResultAsUser:   cfg.ToolResultAsUser,
		SkillExecutionMode: cfg.SkillExecutionMode,
	})
	if err != nil {
		return nil, fmt.Errorf("create skill runner: %w", err)
	}
	return runner, nil
}

func buildSubAgentSystemPrompt(cfg agentic.Config, runner *Runner) string {
	return buildSystemPrompt(cfg.SystemPrompt, runner.GenerateSkillsSection())
}

func buildSystemPrompt(base, section string) string {
	if base == "" {
		return section
	}
	return base + section
}

func appendToolIfMissingByType(allTools []agentic.Tool, tool agentic.Tool, matches func(agentic.Tool) bool) []agentic.Tool {
	for _, t := range allTools {
		if matches(t) {
			return allTools
		}
	}
	return append(allTools, tool)
}

// ExecutionMode controls how skills are executed by the Runner.
//
// ExecutionModeSubAgent (default) spawns an isolated sub-agent for each skill
// call. This provides full context isolation but incurs provider connection
// overhead.
//
// ExecutionModeInline returns skill instructions as a tool result within the
// parent session. The LLM follows the instructions using the parent agent's
// tools. This is faster but accumulates tool calls in the parent history,
// making context compression important.
type ExecutionMode string

const (
	// ExecutionModeSubAgent runs each skill in an isolated sub-agent.
	// This is the default and provides full context isolation.
	ExecutionModeSubAgent ExecutionMode = "subagent"

	// ExecutionModeInline returns skill instructions within the parent
	// LLM session. The parent agent's tools are used to execute the task.
	// Context compression is recommended to manage history growth.
	ExecutionModeInline ExecutionMode = "inline"
)

// Config holds configuration for the skill runner.
type Config struct {
	// Loader is the skills loader (filesystem or embedded).
	// Use NewFileSkillsLoader or NewEmbeddedSkillsLoader.
	// Either Loader or Skills must be provided.
	Loader *SkillsLoader
	// Skills is an optional inline list of skills (alternative to Loader).
	// Used for creating sub-runners with a subset of skills.
	Skills []*Skill
	// Model is the LLM model to use for sub-agents.
	Model provider.Model
	// StreamOptions configures the stream request for sub-agents.
	StreamOptions provider.StreamOptions
	// WorkDir is the working directory for sub-agent file operations.
	WorkDir string
	// Logger is an optional logger for debugging.
	Logger *agentic.Logger
	// ParentTools is an optional list of tools from the parent agent that should
	// also be available to skill sub-agents.
	ParentTools []agentic.Tool
	// Observer is an optional shared observer for skill sub-agents.
	// When set, all sub-agents forward their events to this observer.
	// Useful for implementing features like XML streaming across sub-agents.
	Observer agentic.OutputObserver
	// Enricher is an optional callback called before each skill execution to enrich
	// the sub-agent's task with additional context from the parent environment.
	// This eliminates redundant tool calls (e.g., state.get) by injecting pre-computed
	// data directly into the task. The enricher receives the skill name and current
	// task string, and returns an enriched task string. Return the original task
	// unchanged if no enrichment is needed.
	//
	// Enrichers are automatically propagated to sub-runners so nested skills
	// also benefit from parent context.
	Enricher func(skillName string, task string) string
	// ExecutionMode controls how skills are executed.
	// Default is ExecutionModeSubAgent.
	ExecutionMode ExecutionMode

	// MaxToolRepeatTotal is propagated to skill sub-agents. See agentic.Config.MaxToolRepeatTotal.
	MaxToolRepeatTotal int
	// MaxToolRepeatConsecutive is propagated to skill sub-agents. See agentic.Config.MaxToolRepeatConsecutive.
	MaxToolRepeatConsecutive int
	// ContextCompression is propagated to skill sub-agents. See agentic.Config.ContextCompression.
	ContextCompression agentic.ContextCompressionConfig
	// ReasoningEffort is propagated to skill sub-agents. See agentic.Config.ReasoningEffort.
	ReasoningEffort agentic.ReasoningEffort
	// ToolResultAsUser overrides the provider's tool-result formatting for sub-agents.
	// When nil, the sub-agent inherits the value from the parent config / model compat.
	ToolResultAsUser *bool
	// SkillExecutionMode is propagated to skill sub-agents so nested skills use the
	// same execution mode as the parent.
	SkillExecutionMode agentic.SkillExecutionMode
}

// Runner implements agentic.Tool and handles skill execution.
type Runner struct {
	cfg    Config
	skills map[string]*Skill // Cached skills, key is skill name
	mu     sync.RWMutex
}

// NewRunner creates a new skill runner, loading skills using the configured loader.
func NewRunner(cfg Config) (*Runner, error) {
	// Validate config: either Loader or Skills must be provided
	if cfg.Loader == nil && len(cfg.Skills) == 0 {
		return nil, fmt.Errorf("SkillsLoader or Skills is required")
	}

	r := &Runner{
		cfg:    cfg,
		skills: make(map[string]*Skill),
	}

	// Convert WorkDir to absolute path for path traversal checks
	if r.cfg.WorkDir != "" {
		absWorkDir, err := filepath.Abs(r.cfg.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("invalid work dir: %w", err)
		}
		r.cfg.WorkDir = absWorkDir
	}

	// Load skills
	var loadedSkills []*Skill
	if cfg.Loader != nil {
		loadedSkills = cfg.Loader.Load(cfg.Logger)
	} else {
		loadedSkills = cfg.Skills
	}
	for _, s := range loadedSkills {
		if _, exists := r.skills[s.Name]; exists {
			if cfg.Logger != nil {
				cfg.Logger.Log(agentic.Warn, "Duplicate skill name: %s, skipping", s.Name)
			}
			continue
		}
		r.skills[s.Name] = s
	}

	return r, nil
}

// GetSkill returns a skill by name, or nil if not found.
func (r *Runner) GetSkill(name string) *Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.skills[name]
}

// GetAllSkills returns a list of all loaded skills.
func (r *Runner) GetAllSkills() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skills := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		skills = append(skills, s)
	}
	return skills
}

// Schema implements agentic.Tool. Returns the schema for the "run_skill" tool.
func (r *Runner) IsRetryable(err error) bool { return false }

func (r *Runner) Schema() agentic.ToolSchema {
	// Build enum of skill names
	r.mu.RLock()
	skillNames := make([]string, 0, len(r.skills))
	for name := range r.skills {
		skillNames = append(skillNames, name)
	}
	r.mu.RUnlock()

	return agentic.ToolSchema{
		Name:        "run_skill",
		Description: "Execute a loaded skill with a given task. Available skills are listed in the system prompt.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"skill_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the skill to execute",
					"enum":        skillNames,
				},
				"task": map[string]interface{}{
					"type":        []string{"string", "object", "null"},
					"description": "Task to execute with the skill (can be null for skills that don't need input)",
				},
			},
			"required": []string{"skill_name"},
		},
	}
}

// Execute implements agentic.Tool. Dispatches to executeSubAgent or executeInline
// based on the configured ExecutionMode.
func (r *Runner) Execute(input string) (string, error) {
	skill, task, err := r.parseAndPrepare(input)
	if err != nil {
		return "", err
	}

	if r.cfg.ExecutionMode == ExecutionModeInline {
		return r.executeInline(skill, task)
	}
	return r.executeSubAgent(skill, task)
}

// parseAndPrepare parses the run_skill input, validates it, looks up the skill,
// and returns the prepared task string. This is shared between both execution modes.
func (r *Runner) parseAndPrepare(input string) (*Skill, string, error) {
	params, err := parseSkillParams(input)
	if err != nil {
		return nil, "", err
	}
	if err := validateSkillParams(params); err != nil {
		return nil, "", err
	}

	skill, err := r.lookupSkill(params.SkillName)
	if err != nil {
		return nil, "", err
	}

	task := unwrapTaskString(params.Task)
	task = wrapTaskWithSchema(task, skill.InputSchema)
	task = r.applyEnricher(params.SkillName, task)
	return skill, task, nil
}

func parseSkillParams(input string) (struct {
	SkillName string          `json:"skill_name"`
	Task      json.RawMessage `json:"task"`
}, error) {
	var params struct {
		SkillName string          `json:"skill_name"`
		Task      json.RawMessage `json:"task"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return params, fmt.Errorf("parse skill input: %w", err)
	}
	return params, nil
}

func validateSkillParams(params struct {
	SkillName string          `json:"skill_name"`
	Task      json.RawMessage `json:"task"`
}) error {
	if params.SkillName == "" {
		return fmt.Errorf("missing required field: skill_name")
	}
	if len(params.Task) == 0 {
		return fmt.Errorf("missing required field: task")
	}
	return nil
}

func unwrapTaskString(task json.RawMessage) string {
	taskStr := string(task)
	if len(taskStr) >= 2 && taskStr[0] == '"' && taskStr[len(taskStr)-1] == '"' {
		var s string
		if err := json.Unmarshal(task, &s); err == nil {
			return s
		}
	}
	return taskStr
}

func (r *Runner) lookupSkill(name string) (*Skill, error) {
	r.mu.RLock()
	skill, ok := r.skills[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("skill not found: %s", name)
	}
	return skill, nil
}

func wrapTaskWithSchema(task string, schema map[string]interface{}) string {
	if schema == nil {
		return task
	}
	var temp interface{}
	if err := json.Unmarshal([]byte(task), &temp); err == nil {
		return task
	}
	return wrapTaskIfNeeded(task, schema)
}

func (r *Runner) applyEnricher(skillName, task string) string {
	if r.cfg.Enricher == nil {
		return task
	}
	if enriched := r.cfg.Enricher(skillName, task); enriched != "" {
		return enriched
	}
	return task
}

// executeInline returns the skill instructions prepended to the task string.
// The LLM follows the instructions and uses available tools within the same session.
func (r *Runner) executeInline(skill *Skill, task string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Skill: %s\n\n", skill.Name))
	sb.WriteString(skill.Instructions)
	sb.WriteString("\n\n## Task\n")
	sb.WriteString(task)
	sb.WriteString("\n\nFollow the skill instructions above and complete the task using available tools.")
	return sb.String(), nil
}

// executeSubAgent spawns an isolated sub-agent with the skill's instructions
// as the system prompt. This is the default behavior for full context isolation.
func (r *Runner) executeSubAgent(skill *Skill, task string) (string, error) {
	r.logStart(skill, task)

	effectiveParentTools := r.effectiveParentTools(skill)
	subTools := r.buildSubTools(effectiveParentTools, skill)
	systemPrompt := r.buildSubAgentSystemPrompt(skill, effectiveParentTools, subTools)

	subAgent := r.newSubAgent(systemPrompt, subTools)
	defer subAgent.Stop()

	capture := r.attachSubAgentObservers(subAgent, skill)
	if err := r.runSubAgent(subAgent, skill, systemPrompt, task); err != nil {
		return "", err
	}

	r.logFinish(skill, capture.content)
	return capture.content, nil
}

func (r *Runner) logStart(skill *Skill, task string) {
	if r.cfg.Logger != nil {
		r.cfg.Logger.Log(agentic.Info, "[subagent] Starting skill=%q task=%q", skill.Name, task)
	}
}

func (r *Runner) effectiveParentTools(skill *Skill) []agentic.Tool {
	if len(skill.Tools) == 0 {
		return r.cfg.ParentTools
	}
	var out []agentic.Tool
	for _, requestedName := range skill.Tools {
		for _, pt := range r.cfg.ParentTools {
			if pt.Schema().Name == requestedName {
				out = append(out, pt)
				break
			}
		}
	}
	return out
}

func (r *Runner) buildSubTools(parentTools []agentic.Tool, skill *Skill) []agentic.Tool {
	subTools := tools.Tools(r.cfg.WorkDir, r.cfg.Logger)
	if len(parentTools) > 0 {
		subTools = append(subTools, parentTools...)
	}
	if subRunner := r.buildSubRunner(skill, parentTools); subRunner != nil {
		subTools = append(subTools, subRunner)
	}
	r.logTools(skill, subTools)
	return subTools
}

func (r *Runner) logTools(skill *Skill, subTools []agentic.Tool) {
	if r.cfg.Logger == nil {
		return
	}
	toolNames := make([]string, 0, len(subTools))
	for _, t := range subTools {
		toolNames = append(toolNames, t.Schema().Name)
	}
	r.cfg.Logger.Log(agentic.Info, "[subagent] skill=%q tools=%v", skill.Name, toolNames)
}

func (r *Runner) buildSubAgentSystemPrompt(skill *Skill, parentTools, subTools []agentic.Tool) string {
	systemPrompt := skill.Instructions
	if subRunner := r.findSubRunner(subTools); subRunner != nil {
		systemPrompt += subRunner.GenerateSkillsSection()
	}
	return systemPrompt
}

func (r *Runner) findSubRunner(subTools []agentic.Tool) *Runner {
	for _, t := range subTools {
		if sr, ok := t.(*Runner); ok {
			return sr
		}
	}
	return nil
}

func (r *Runner) buildSubRunner(skill *Skill, parentTools []agentic.Tool) *Runner {
	inheritedSkills := r.inheritedSkills(skill)
	if len(inheritedSkills) == 0 && len(skill.SubSkills) == 0 {
		return nil
	}
	subRunner, err := NewRunner(Config{
		Skills:             append(inheritedSkills, skill.SubSkills...),
		Model:              r.cfg.Model,
		StreamOptions:      r.cfg.StreamOptions,
		WorkDir:            r.cfg.WorkDir,
		Logger:             r.cfg.Logger,
		ParentTools:        parentTools,
		Observer:           r.cfg.Observer,
		Enricher:           r.cfg.Enricher,
		MaxToolRepeatTotal:       r.cfg.MaxToolRepeatTotal,
		MaxToolRepeatConsecutive: r.cfg.MaxToolRepeatConsecutive,
		ContextCompression: r.cfg.ContextCompression,
		ReasoningEffort:    r.cfg.ReasoningEffort,
		ToolResultAsUser:   r.cfg.ToolResultAsUser,
		SkillExecutionMode: r.cfg.SkillExecutionMode,
	})
	if err != nil {
		if r.cfg.Logger != nil {
			r.cfg.Logger.Log(agentic.Error, "[subagent] skill=%q sub-runner failed: %v", skill.Name, err)
		}
		return nil
	}
	return subRunner
}

func (r *Runner) inheritedSkills(skill *Skill) []*Skill {
	all := r.GetAllSkills()
	if len(skill.Skills) == 0 {
		out := make([]*Skill, 0, len(all))
		for _, s := range all {
			if s.Name != skill.Name {
				out = append(out, s)
			}
		}
		return out
	}
	allowed := make(map[string]bool, len(skill.Skills))
	for _, name := range skill.Skills {
		allowed[name] = true
	}
	out := make([]*Skill, 0, len(all))
	for _, s := range all {
		if s.Name != skill.Name && allowed[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

func (r *Runner) newSubAgent(systemPrompt string, subTools []agentic.Tool) *agentic.Agent {
	return agentic.NewAgent(agentic.Config{
		Model:              r.cfg.Model,
		APIKey:             r.cfg.StreamOptions.APIKey,
		StreamOptions:      r.cfg.StreamOptions,
		SystemPrompt:       systemPrompt,
		Tools:              subTools,
		Logger:             r.cfg.Logger,
		SkillExecutionMode: r.cfg.SkillExecutionMode,
		ContextCompression: r.cfg.ContextCompression,
		MaxToolRepeatTotal:       r.cfg.MaxToolRepeatTotal,
		MaxToolRepeatConsecutive: r.cfg.MaxToolRepeatConsecutive,
		ReasoningEffort:    r.cfg.ReasoningEffort,
		ToolResultAsUser:   r.cfg.ToolResultAsUser,
	})
}

func (r *Runner) attachSubAgentObservers(subAgent *agentic.Agent, skill *Skill) *resultCapture {
	capture := &resultCapture{logger: r.cfg.Logger, skill: skill.Name}
	subAgent.AddObserver(capture)
	if r.cfg.Observer != nil {
		subAgent.AddObserver(&forwardObserver{Observer: r.cfg.Observer})
	}
	return capture
}

func (r *Runner) runSubAgent(subAgent *agentic.Agent, skill *Skill, systemPrompt, task string) error {
	if r.cfg.Logger != nil {
		r.cfg.Logger.Log(agentic.Info, "[subagent] skill=%q running with system_prompt_len=%d", skill.Name, len(systemPrompt))
	}
	if err := subAgent.Run(context.Background(), task); err != nil {
		if r.cfg.Logger != nil {
			r.cfg.Logger.Log(agentic.Error, "[subagent] skill=%q run failed: %v", skill.Name, err)
		}
		return fmt.Errorf("sub-agent failed: %w", err)
	}
	return nil
}

func (r *Runner) logFinish(skill *Skill, content string) {
	if r.cfg.Logger != nil {
		r.cfg.Logger.Log(agentic.Info, "[subagent] skill=%q finished content_len=%d", skill.Name, len(content))
	}
}

// wrapTaskIfNeeded tries to wrap a plain string into a JSON object based on the schema.
// Returns the original string if wrapping is not possible.
func wrapTaskIfNeeded(task string, schema map[string]interface{}) string {
	// Check if schema expects an object with one required string field
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return task
	}

	required, _ := schema["required"].([]interface{})
	if len(required) != 1 {
		return task
	}

	// Get the required field name
	fieldName, ok := required[0].(string)
	if !ok {
		return task
	}

	// Check if the field is of type string
	field, ok := props[fieldName].(map[string]interface{})
	if !ok {
		return task
	}

	fieldType, _ := field["type"].(string)
	if fieldType != "string" {
		return task
	}

	// Wrap the task string in a JSON object
	wrapped := map[string]string{fieldName: task}
	b, err := json.Marshal(wrapped)
	if err != nil {
		return task
	}

	return string(b)
}

// GenerateSkillsSection returns a string to inject into the main agent's system prompt,
// listing all available skills and their details.
func (r *Runner) GenerateSkillsSection() string {
	return generateSkillsSection(r.GetAllSkills())
}

// generateSkillsSection builds the skills section markdown for a given skill list.
func generateSkillsSection(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Available Skills\n")
	sb.WriteString("To use a skill, call the 'run_skill' tool with 'skill_name' and 'task' parameters.\n")
	for _, s := range skills {
		writeSkillSection(&sb, s)
	}
	return sb.String()
}

func writeSkillSection(sb *strings.Builder, s *Skill) {
	sb.WriteString(fmt.Sprintf("\n### %s\n", s.Name))
	sb.WriteString(fmt.Sprintf("Description: %s\n", s.Description))
	if s.InputSchema != nil {
		schemaJSON, _ := json.MarshalIndent(s.InputSchema, "  ", "  ")
		sb.WriteString(fmt.Sprintf("Input Schema:\n%s\n", string(schemaJSON)))
	}
	if names := skillNames(s.SubSkills); len(names) > 0 {
		sb.WriteString(fmt.Sprintf("Sub-skills: %s\n", strings.Join(names, ", ")))
	}
	if len(s.Skills) > 0 {
		sb.WriteString(fmt.Sprintf("Inherited skills: %s\n", strings.Join(s.Skills, ", ")))
	}
	if len(s.Tools) > 0 {
		sb.WriteString(fmt.Sprintf("Required tools: %s\n", strings.Join(s.Tools, ", ")))
	}
}

func skillNames(skills []*Skill) []string {
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, s.Name)
	}
	return names
}

// resultCapture is an OutputObserver that captures only the final assistant content
// from the sub-agent. It excludes thinking/reasoning tokens and any content generated
// before tool calls — only the last assistant message after all tool execution is kept.
type resultCapture struct {
	content string
	logger  *agentic.Logger
	skill   string
}

// forwardObserver wraps another observer and forwards all events.
// Used to propagate shared observers to sub-agents.
type forwardObserver struct {
	Observer agentic.OutputObserver
}

func (fo *forwardObserver) OnEvent(event agentic.OutputEvent) {
	fo.Observer.OnEvent(event)
}

// Ensure forwardObserver implements agentic.OutputObserver
var _ agentic.OutputObserver = &forwardObserver{}

func (rc *resultCapture) OnEvent(event agentic.OutputEvent) {
	switch event.Type {
	case agentic.EventToolCall:
		rc.handleToolCall(event)
	case agentic.EventContent:
		rc.handleContent(event)
	case agentic.EventToolResult, agentic.EventEnd:
		rc.logEvent(event.Type, "%s", event.Text)
	}
}

func (rc *resultCapture) handleToolCall(event agentic.OutputEvent) {
	rc.logEvent(agentic.EventToolCall, "name=%q input=%q (discarding prior content)", event.ToolName, event.ToolInput)
	rc.content = ""
}

func (rc *resultCapture) handleContent(event agentic.OutputEvent) {
	if event.Text == "" || event.Role != agentic.Assistant || event.State != agentic.StateContent {
		return
	}
	rc.logEvent(agentic.EventContent, "content=%q", event.Text)
	rc.content += event.Text
}

func (rc *resultCapture) logEvent(typ agentic.EventType, format string, args ...interface{}) {
	if rc.logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	rc.logger.Log(agentic.Debug, "[subagent] skill=%q event=%s %s", rc.skill, typ, msg)
}

// Ensure resultCapture implements agentic.OutputObserver
var _ agentic.OutputObserver = &resultCapture{}
