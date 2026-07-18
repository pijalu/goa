// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/perms"
	gorole "github.com/pijalu/goa/internal/role"
)

// AgentConfig defines the configuration for a role-specific sub-agent.
type AgentConfig struct {
	ModelName       string // may differ from main agent model
	ProviderID      string // optional per-role provider override
	SystemPrompt    string
	Temperature     float64
	ReasoningEffort agentic.ReasoningEffort
	AllowedTools    []string // if non-empty, restrict available tools to these names
}

// AgentPool lazily creates and caches agents for different roles.
// Each agent is created using the Model + StreamOptions path through
// agentic.Config (the new streaming architecture). Per-role model isolation
// is achieved by setting ModelFactory — each role with a distinct ModelName
// gets its own Model resolved through this factory.
type AgentPool struct {
	mu      sync.Mutex
	agents  map[string]*agentic.Agent // role name → agent
	configs map[string]AgentConfig    // role → config

	// defaultModel is the fallback model when no per-role model is configured.
	defaultModel provider.Model
	// defaultOpts are stream options applied to all agents.
	defaultOpts provider.StreamOptions
	tools       []agentic.Tool

	// ModelFactory creates a Model for a given model name (per-role isolation).
	// When set, each role with a distinct ModelName gets its own Model resolved
	// through this factory, avoiding model interference between roles.
	ModelFactory func(modelName string) (provider.Model, error)

	// ProviderModelFactory creates a Model for a given provider and model name.
	// When set and a role config has ProviderID, this factory is used instead of
	// ModelFactory so the role can use a different provider than the main agent.
	ProviderModelFactory func(providerID, modelName string) (provider.Model, error)

	// OnAgentCreated is called for every newly created agent.
	// Used by ForegroundOrchestrator to wire output observers.
	OnAgentCreated func(role string, agent *agentic.Agent)

	// PromptRegistry loads system prompts for roles.
	// If set, GetOrCreate loads "{role}.system" from the registry instead of
	// using the generic fallback prompt.
	PromptRegistry interface {
		Load(name string) (string, error)
		Render(name string, data interface{}) (string, error)
	}

	// Config holds the Goa configuration used to build agentic.Config for
	// sub-agents. When nil, sub-agents are created with minimal defaults.
	Config *config.Config

	// PolicySource supplies the autonomy level, guard rules, and tool
	// confirmation callback that sub-agents must inherit so they are subject to
	// the SAME safety gating as the main agent. Without it, a spawned coder
	// sub-agent would run tools (e.g. bash) unconfirmed even when the session
	// is in an ask/confirm autonomy mode — a safety and transparency hole (C2).
	// Sourced from the host's AgentManager at pool construction.
	GetAutonomy    func() internal.AutonomyLevel
	GetGuardConfig func() perms.GuardConfig
	ConfirmTool    func(ctx context.Context, toolName, input string) (bool, error)
	// ProjectDir scopes SOLO-mode filesystem/shell restrictions for sub-agents.
	ProjectDir string

	// agentBus enables agent-to-agent messaging via send_message tools.
	agentBus *agentic.AgentBus
	// connectors keeps CommConnector instances alive for the agent lifetime.
	connectors map[string]*agentic.CommConnector

	// orch is the orchestrator that runs workflows. When set, agents created
	// by this pool get the WorkflowNextTool for stage advancement.
	orch *ForegroundOrchestrator
}

// SetOrchestrator sets the orchestrator for this pool, enabling workflow tools.
func (p *AgentPool) SetOrchestrator(orch *ForegroundOrchestrator) {
	p.orch = orch
}

// Orchestrator returns the orchestrator associated with this pool.
func (p *AgentPool) Orchestrator() *ForegroundOrchestrator {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.orch
}

// NewAgentPool creates an AgentPool with the given default model and stream options.
func NewAgentPool(defaultModel provider.Model, defaultOpts provider.StreamOptions, tools []agentic.Tool) *AgentPool {
	return &AgentPool{
		agents:       make(map[string]*agentic.Agent),
		configs:      make(map[string]AgentConfig),
		connectors:   make(map[string]*agentic.CommConnector),
		defaultModel: defaultModel,
		defaultOpts:  defaultOpts,
		tools:        tools,
	}
}

// SetConfig sets the configuration for a role.
func (p *AgentPool) SetConfig(role string, cfg AgentConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configs[role] = cfg
}

// SetGoaConfig sets the Goa configuration used for creating sub-agents.
// When set, sub-agents inherit MaxToolRepeatTotal, MaxToolRepeatConsecutive, context compression,
// reasoning effort, tool-result-as-user, and skill execution mode.
func (p *AgentPool) SetGoaConfig(cfg *config.Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Config = cfg
}

// SetAgentBus sets the communication bus used for agent-to-agent messaging.
// When set, every sub-agent created by the pool receives a send_message tool
// and an auto-receive CommConnector.
func (p *AgentPool) SetAgentBus(bus *agentic.AgentBus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.agentBus = bus
}

// RoleConfig returns the configured AgentConfig for a role, or zero value.
func (p *AgentPool) RoleConfig(role string) AgentConfig {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.configs[role]
}

// CreateTaskAgent creates a one-off agent for a task role using the provided config.
func (p *AgentPool) CreateTaskAgent(role string, cfg AgentConfig) (*agentic.Agent, error) {
	p.mu.Lock()
	p.configs[role] = cfg
	onCreated := p.OnAgentCreated
	p.mu.Unlock()

	systemPrompt := p.resolveSystemPrompt(role, cfg)
	mdl := p.resolveRoleModel(cfg)
	ac := p.buildAgentConfig(role, mdl, cfg, systemPrompt)
	a := agentic.NewAgent(ac)

	p.mu.Lock()
	p.agents[role] = a
	p.wireAgentBusLocked(role, a)
	p.mu.Unlock()

	if onCreated != nil {
		onCreated(role, a)
	}
	return a, nil
}

// GetOrCreate returns an agent for the given role, creating it if needed.
// The agent is configured using the role's AgentConfig (if set), or falls
// back to the main agent's provider and system prompt.
//
// Model selection (in priority order):
//  1. If ProviderFactory is set and ModelName is configured, create a new
//     provider instance so each role gets full model isolation.
//  2. If the provider supports SetModel, mutate the shared provider (works
//     but shares state across roles — safe under the pool's mutex).
//  3. Use the default provider model.
func (p *AgentPool) GetOrCreate(role string) (*agentic.Agent, error) {
	p.mu.Lock()
	if a, ok := p.agents[role]; ok {
		p.mu.Unlock()
		return a, nil
	}
	cfg := p.configs[role]
	onCreated := p.OnAgentCreated
	p.mu.Unlock()

	// Build the agent without holding the mutex so callbacks and external
	// factories (which may block or acquire other locks) never deadlock.
	systemPrompt := p.resolveSystemPrompt(role, cfg)
	mdl := p.resolveRoleModel(cfg)
	ac := p.buildAgentConfig(role, mdl, cfg, systemPrompt)
	a := agentic.NewAgent(ac)

	p.mu.Lock()
	if existing, ok := p.agents[role]; ok {
		p.mu.Unlock()
		return existing, nil
	}
	p.agents[role] = a
	p.wireAgentBusLocked(role, a)
	p.mu.Unlock()

	if onCreated != nil {
		onCreated(role, a)
	}
	return a, nil
}

// CreateEphemeralAgent builds a fresh, isolated agent for role that is NOT
// cached and NOT registered on the agent bus. Each call returns a distinct
// *agentic.Agent with its own conversation history and tool instances, so it
// is safe to run concurrently and across many delegations without the
// shared-state leakage of the cached GetOrCreate path.
//
// This is the agent factory for /orchestrate delegation ("delegate to a series
// of workers as required"): every delegate(role, task) call acquires one.
//
// Unlike GetOrCreate/CreateTaskAgent, ephemeral agents:
//   - receive only their allow-listed BASE tools (no WorkflowNextTool, no
//     SendMessageTool), because orchestration specialists are isolated;
//   - are NOT force-tooled (tool_choice=auto), so a specialist can return a
//     plain text answer as its delegation result;
//   - do NOT fire OnAgentCreated, so they do not inherit the foreground
//     orchestrator's companion observer (which would otherwise route their
//     events into the companion renderer — the "companion · cycle" leak).
//
// The caller owns the agent's full lifecycle (it is GC'd once released).
func (p *AgentPool) CreateEphemeralAgent(role string, cfg AgentConfig) (*agentic.Agent, error) {
	systemPrompt := p.resolveSystemPrompt(role, cfg)
	mdl := p.resolveRoleModel(cfg)
	tools := p.baseToolsForRole(role, cfg.AllowedTools)
	ac := p.assembleConfig(mdl, cfg, systemPrompt, tools, false)
	return agentic.NewAgent(ac), nil
}

func (p *AgentPool) wireAgentBusLocked(role string, a *agentic.Agent) {
	if p.agentBus == nil {
		return
	}
	inbox, err := p.agentBus.Register(role)
	if err != nil {
		log.Printf("warning: failed to register %q on agent bus: %v", role, err)
		return
	}
	p.connectors[role] = agentic.NewCommConnector(a, inbox)
}

func (p *AgentPool) resolveSystemPrompt(role string, cfg AgentConfig) string {
	if cfg.SystemPrompt != "" {
		return cfg.SystemPrompt
	}
	if p.PromptRegistry == nil {
		return defaultRolePrompt(role)
	}
	if loaded, err := p.PromptRegistry.Load(role + ".system"); err == nil {
		return loaded
	}
	if loaded, err := p.PromptRegistry.Load(role); err == nil {
		return p.renderRolePrompt(role, loaded)
	}
	if loaded, err := p.PromptRegistry.Render("fallback_role", map[string]string{"Role": role}); err == nil {
		return loaded
	}
	return defaultRolePrompt(role)
}

func (p *AgentPool) renderRolePrompt(role, loaded string) string {
	if role != gorole.Companion || p.PromptRegistry == nil {
		return loaded
	}
	rendered, err := p.PromptRegistry.Render(gorole.Companion, map[string]string{
		"EndDelimiter": DefaultCompanionEndDelimiter,
	})
	if err == nil {
		return rendered
	}
	return loaded
}

func defaultRolePrompt(role string) string {
	return "You are a " + role + " agent. Be concise and thorough."
}

func (p *AgentPool) resolveRoleModel(cfg AgentConfig) provider.Model {
	mdl := p.defaultModel
	if cfg.ProviderID != "" && p.ProviderModelFactory != nil {
		if roleModel, err := p.ProviderModelFactory(cfg.ProviderID, cfg.ModelName); err == nil {
			return roleModel
		}
		log.Printf("warning: ProviderModelFactory failed for provider %q model %q, using default", cfg.ProviderID, cfg.ModelName)
		return mdl
	}
	if cfg.ModelName == "" || p.ModelFactory == nil {
		return mdl
	}
	if roleModel, err := p.ModelFactory(cfg.ModelName); err == nil {
		return roleModel
	}
	log.Printf("warning: ModelFactory failed for model %q, using default", cfg.ModelName)
	return mdl
}

func (p *AgentPool) buildAgentConfig(role string, mdl provider.Model, cfg AgentConfig, systemPrompt string) agentic.Config {
	tools := p.toolsForRole(role, cfg.AllowedTools)
	force := role == gorole.Planner || role == gorole.Coder || role == gorole.Reviewer || role == gorole.Companion
	return p.assembleConfig(mdl, cfg, systemPrompt, tools, force)
}

// assembleConfig builds an agentic.Config from a resolved model, prompt, and
// tool set. forceToolUse sets tool_choice=required and appends the "you MUST
// use these tools" directive (workflow stage agents); orchestration ephemeral
// agents pass false so a specialist can return a plain text answer as its
// delegation result.
func (p *AgentPool) assembleConfig(mdl provider.Model, cfg AgentConfig, systemPrompt string, tools []agentic.Tool, forceToolUse bool) agentic.Config {
	opts := p.defaultOpts
	// Ensure MaxTokens is generous enough for reasoning models (e.g., Qwen)
	// that use tokens for thinking before producing tool calls.
	if opts.MaxTokens == 0 || opts.MaxTokens < 2048 {
		opts.MaxTokens = 4096
	}
	if forceToolUse {
		opts.ToolChoice = "required"
		systemPrompt = appendToolDirective(systemPrompt, tools)
	}
	ac := agentic.Config{
		Model:              mdl,
		APIKey:             opts.APIKey,
		StreamOptions:      opts,
		SystemPrompt:       systemPrompt,
		Tools:              tools,
		AllowEmptyResponse: true,
	}
	if cfg.ReasoningEffort != "" {
		ac.ReasoningEffort = cfg.ReasoningEffort
	}
	p.inheritGoaConfig(&ac)
	return ac
}

// appendToolDirective adds the available-tools list and the "you MUST use
// tools" instruction to the system prompt (workflow stage agents only).
func appendToolDirective(systemPrompt string, tools []agentic.Tool) string {
	toolNames := make([]string, 0, len(tools))
	for _, t := range tools {
		toolNames = append(toolNames, t.Schema().Name)
	}
	return systemPrompt + "\n\nAvailable tools: " + strings.Join(toolNames, ", ") +
		".\nYou MUST use these tools to complete your work — call them by name with the required arguments."
}

func (p *AgentPool) toolsForRole(role string, allowed []string) []agentic.Tool {
	result := p.baseToolsForRole(role, allowed)
	if p.agentBus != nil {
		result = append(result, &agentic.SendMessageTool{
			Bus:      p.agentBus,
			FromName: role,
		})
	}
	// Add WorkflowNextTool so stage agents can advance. The tool validates
	// that actual work was done before allowing advancement — see Execute().
	if p.orch != nil {
		result = append(result, &WorkflowNextTool{
			Orchestrator: p.orch,
		})
	}
	return result
}

// baseToolsForRole returns the allow-listed tool set WITHOUT the workflow and
// companion extras. Used by orchestration ephemeral agents, which are isolated
// single-task workers (not companion or workflow-stage agents). The role
// argument is accepted for symmetry with toolsForRole.
func (p *AgentPool) baseToolsForRole(_ string, allowed []string) []agentic.Tool {
	result := make([]agentic.Tool, 0, len(p.tools))
	for _, t := range p.tools {
		if len(allowed) == 0 || containsString(allowed, t.Schema().Name) {
			result = append(result, t)
		}
	}
	return result
}

// ToolNames returns the names of all tools registered in the pool. The
// names are stable and can be used with AgentConfig.AllowedTools to restrict
// the tools available to a sub-agent.
func (p *AgentPool) ToolNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.tools))
	for _, t := range p.tools {
		names = append(names, t.Schema().Name)
	}
	return names
}

func (p *AgentPool) inheritGoaConfig(ac *agentic.Config) {
	// Safety policy inheritance is independent of p.Config: sub-agents must be
	// subject to the same autonomy/guard/confirm gating as the main agent even
	// when no Goa config is set (C2). Only copy a field when the pool has a
	// source for it; a nil source leaves the agent's zero value (no gating),
	// which callers treat as "not configured" rather than "allow all".
	if p.GetAutonomy != nil {
		ac.GetAutonomy = p.GetAutonomy
	}
	if p.GetGuardConfig != nil {
		ac.GetGuardConfig = p.GetGuardConfig
	}
	if p.ConfirmTool != nil {
		ac.ConfirmTool = p.ConfirmTool
	}
	if p.ProjectDir != "" {
		ac.ProjectDir = p.ProjectDir
	}

	if p.Config == nil {
		return
	}
	if ac.ReasoningEffort == "" {
		ac.ReasoningEffort = agentic.ReasoningEffort(p.Config.GetReasoningEffort())
	}
	ac.MaxToolRepeatTotal = p.Config.Execution.MaxToolRepeatTotal
	ac.MaxToolRepeatConsecutive = p.Config.Execution.MaxToolRepeatConsecutive
	ac.ToolResultAsUser = p.Config.GetToolResultAsUser()
	ac.SkillExecutionMode = agentic.SkillExecutionMode(p.Config.Skills.ExecutionMode)
	if p.Config.ContextCompression.Enabled || p.Config.ContextCompression.MaxTokens > 0 {
		ac.ContextCompression = agentic.ContextCompressionConfig{
			MaxTokens:        p.Config.ContextCompression.MaxTokens,
			ThresholdPercent: p.Config.ContextCompression.ThresholdPercent,
			Thresholds: agentic.CompressionThresholds{
				SoftPercent:    p.Config.ContextCompression.Thresholds.SoftPercent,
				TriggerPercent: p.Config.ContextCompression.Thresholds.TriggerPercent,
				HardPercent:    p.Config.ContextCompression.Thresholds.HardPercent,
			},
			OnContextError:      p.Config.ContextCompression.OnContextError,
			Strategy:            agentic.CompressionStrategy(p.Config.ContextCompression.Strategy),
			PreserveRecentTurns: p.Config.ContextCompression.PreserveRecentTurns,
		}
	}
}

// Get returns an existing agent, or nil if not created yet.
func (p *AgentPool) Get(role string) *agentic.Agent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.agents[role]
}

// Evict removes a one-shot task agent from the pool's cache, releasing the
// reference so the agent (and its provider/observer state) can be garbage-
// collected. It is intended for agents created via CreateTaskAgent with a
// unique per-task role (e.g. "coder-task-<nanos>"); long-lived role agents
// created via GetOrCreate should generally not be evicted.
//
// Evict also tears down any CommConnector wired for the role and drops the
// per-role config, preventing unbounded growth of the agents/configs/connectors
// maps over a long session (BUG-10). It is safe to call concurrently and is a
// no-op for roles that do not exist.
func (p *AgentPool) Evict(role string) {
	p.mu.Lock()
	delete(p.agents, role)
	delete(p.configs, role)
	conn, ok := p.connectors[role]
	if ok {
		delete(p.connectors, role)
	}
	p.mu.Unlock()

	// Stop the connector outside the lock: it calls agent.Run's cancellation
	// path and may block on an in-flight turn.
	if ok && conn != nil {
		conn.Stop()
	}
}

// Roles returns the list of roles that have been created.
func (p *AgentPool) Roles() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var roles []string
	for r := range p.agents {
		roles = append(roles, r)
	}
	return roles
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
