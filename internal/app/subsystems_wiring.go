// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/core/tasks"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/role"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
)

func initPromptAndWorkflowLayer(cfg *config.Config, projectDir string) (*prompts.Registry, *multiagent.WorkflowRegistry) {
	promptDir := cfg.Prompts.Dir
	if promptDir == "" {
		promptDir = filepath.Join(projectDir, ".goa", "prompts")
	}
	promptReg := prompts.NewRegistry(prompts.EmbeddedFS(), promptDir, filepath.Join(cfg.ConfigDir, "prompts"))

	workflowReg := multiagent.NewWorkflowRegistry(promptReg)
	if err := workflowReg.LoadWorkflowTree(filepath.Join(projectDir, "workflows")); err != nil {
		log.Printf("Warning: failed to load project workflows: %v\n", err)
	}
	if err := workflowReg.LoadDir(filepath.Join(projectDir, ".goa", "workflows")); err != nil {
		log.Printf("Warning: failed to load .goa/workflows: %v\n", err)
	}
	if err := workflowReg.LoadWorkflowTree(filepath.Join(cfg.ConfigDir, "workflows")); err != nil {
		log.Printf("Warning: failed to load user workflows: %v\n", err)
	}

	return promptReg, workflowReg
}

// registerAgentDrivenTools registers the agent-driven companion tools
// (request_review, delegate_to). Registration is gated by the tools.enabled
// config flags OR active companion intent (companionActive): the companion
// mode gates execution via the tools' Enabled flag, and a user who turned
// companion on must never find its tools missing because a config layer
// persisted a stale `enabled: false`.
func registerAgentDrivenTools(toolRegistry *tools.ToolRegistry, tools []agentic.Tool, cfg *config.Config, companionActive bool) (*multiagent.RequestReviewTool, *multiagent.DelegateTool) {
	var requestReviewTool *multiagent.RequestReviewTool
	var delegateTool *multiagent.DelegateTool
	for _, t := range tools {
		switch v := t.(type) {
		case *multiagent.RequestReviewTool:
			if !cfg.Tools.Enabled.RequestReview && !companionActive {
				continue
			}
			requestReviewTool = v
		case *multiagent.DelegateTool:
			if !cfg.Tools.Enabled.DelegateTo && !companionActive {
				continue
			}
			delegateTool = v
		}
		toolRegistry.Register(t)
	}
	return requestReviewTool, delegateTool
}

func createAgentPool(mdl agenticprovider.Model, providerMgr *provider.ProviderManager, toolRegistry *tools.ToolRegistry, promptReg *prompts.Registry, cfg *config.Config, modeRegistry *core.ModeRegistry, swarmState *swarm.State, taskBus *tasks.Bus, agentMgr *core.AgentManager, eventBus *event.Bus) *multiagent.AgentPool {
	allTools := toolRegistry.All()
	streamOpts := providerMgr.BuildStreamOptions()
	pool := multiagent.NewAgentPool(mdl, streamOpts, allTools)
	pool.PromptRegistry = promptReg
	pool.SetGoaConfig(cfg)

	// Wire sub-agent safety gating (C2): every agent the pool creates inherits
	// the same autonomy/guard/confirm policy and project scope as the main
	// agent, so a spawned coder/explore sub-agent cannot run tools unconfirmed
	// when the session is in an ask/confirm autonomy mode.
	if agentMgr != nil {
		getAutonomy, getGuard, confirm, projectDir := agentMgr.PolicySource()
		pool.GetAutonomy = getAutonomy
		pool.GetGuardConfig = getGuard
		pool.ConfirmTool = confirm
		pool.ProjectDir = projectDir
	}

	pool.ModelFactory = func(modelName string) (agenticprovider.Model, error) {
		return providerMgr.ResolveModelByID(modelName)
	}
	pool.ProviderModelFactory = func(providerID, modelName string) (agenticprovider.Model, error) {
		return providerMgr.ResolveModelForProvider(providerID, modelName)
	}

	configureRoleModels(pool, cfg, modeRegistry)
	// Register AgentTool and AgentSwarmTool with ModeResolver so sub-agents
	// get mode-appropriate prompts, tools, and temperature settings.
	registerSubAgentTools(toolRegistry, pool, modeRegistry, swarmState, taskBus, agentMgr, eventBus, cfg)
	return pool
}

func configureRoleModels(pool *multiagent.AgentPool, cfg *config.Config, modeRegistry *core.ModeRegistry) {
	resolveAllowed := func(major internal.MajorMode) []string {
		if modeRegistry == nil {
			return nil
		}
		if spec, err := modeRegistry.Resolve(major); err == nil && len(spec.AllowedTools) > 0 {
			return spec.AllowedTools
		}
		return nil
	}
	if cfg.MultiAgent.CompanionModel != "" {
		allowed := resolveAllowed(internal.MajorReviewer)
		pool.SetConfig("companion", multiagent.AgentConfig{
			ModelName:       cfg.MultiAgent.CompanionModel,
			ProviderID:      cfg.MultiAgent.CompanionProvider,
			ReasoningEffort: agentic.ReasoningEffort(cfg.GetThinkingLevel("companion")),
			AllowedTools:    allowed,
		})
	}
	if cfg.MultiAgent.PlannerModel != "" {
		allowed := resolveAllowed(internal.MajorPlanner)
		pool.SetConfig("planner", multiagent.AgentConfig{
			ModelName:       cfg.MultiAgent.PlannerModel,
			ReasoningEffort: agentic.ReasoningEffort(cfg.GetThinkingLevel("planner")),
			AllowedTools:    allowed,
		})
	}
	if cfg.MultiAgent.CoderModel != "" {
		allowed := resolveAllowed(internal.MajorCoder)
		pool.SetConfig("coder", multiagent.AgentConfig{
			ModelName:       cfg.MultiAgent.CoderModel,
			ReasoningEffort: agentic.ReasoningEffort(cfg.GetThinkingLevel("coder")),
			AllowedTools:    allowed,
		})
	}
}

func wireForegroundOrchestrator(pool *multiagent.AgentPool, promptReg *prompts.Registry, agentMgr *core.AgentManager, cfg *config.Config, workflowReg *multiagent.WorkflowRegistry, sessionStore core.SessionStoreAPI) *multiagent.ForegroundOrchestrator {
	orch := multiagent.NewForegroundOrchestrator(pool)
	pool.SetOrchestrator(orch)
	orch.SetPromptRegistry(promptReg)
	orch.SetSteeringQueue(agentMgr.SteeringQueue())
	orch.ModeSwitchCallback = makeModeSwitchCallback(agentMgr)
	agentMgr.SetForegroundOrchestrator(orch)
	// Record every pool sub-agent's complete event exchange next to the main
	// session file (RC-6: exports must contain the full multi-agent
	// conversation). The directory derives from the CURRENT session at record
	// time so /new and session restore rotate it automatically.
	if sessionStore != nil {
		orch.SetRoleRecorder(multiagent.NewRoleSessionRecorder(func() string {
			path := sessionStore.CurrentSessionPath()
			if path == "" {
				return ""
			}
			return filepath.Join(filepath.Dir(path), sessionStore.SessionID(), "agents")
		}))
	}
	return orch
}

func makeModeSwitchCallback(agentMgr *core.AgentManager) func(string) {
	return func(agentName string) {
		var major internal.MajorMode
		switch agentName {
		case role.Planner:
			major = internal.MajorPlanner
		case role.Coder:
			major = internal.MajorCoder
		case role.Reviewer, role.Companion:
			major = internal.MajorReviewer
		}
		if major != "" {
			cur := agentMgr.CurrentMode()
			agentMgr.SetMode(cur.WithMajor(major))
		}
	}
}

func wireCompanionCreation(pool *multiagent.AgentPool, agentMgr *core.AgentManager, snap core.SessionStateSnapshot) {
	origOnCreated := pool.OnAgentCreated
	pool.OnAgentCreated = func(role string, agent *agentic.Agent) {
		if origOnCreated != nil {
			origOnCreated(role, agent)
		}
		if role != "companion" {
			return
		}
		agentMgr.SetCompanionAgent(agent)
		restoreCompanionHistory(agent, snap.CompanionHistory)
		// F6: log the companion model identity (the orchestrator logs
		// agent_started.model; the companion path had no equivalent).
		agentMgr.LogCompanionStarted(agent)
	}
}

func restoreCompanionHistory(agent *agentic.Agent, rawHistory []json.RawMessage) {
	if len(rawHistory) == 0 {
		return
	}
	var history []agentic.Message
	for _, raw := range rawHistory {
		var msg agentic.Message
		if err := json.Unmarshal(raw, &msg); err == nil {
			history = append(history, msg)
		}
	}
	if len(history) > 0 {
		agent.SetHistory(history)
	}
}

// registerSkillRunner registers the run_skill tool so the model can execute
// action skills in any execution mode. In inline mode the tool returns the
// skill instructions as its result; in sub-agent mode it spawns a dedicated
// sub-agent via the pool.
func registerSkillRunner(toolRegistry *tools.ToolRegistry, skillRegistry *skills.SkillRegistry, pool *multiagent.AgentPool, promptReg *prompts.Registry, cfg *config.Config) {
	inline := cfg.Skills.ExecutionMode != config.AgenticSkillModeSubAgent
	skillRunner := skills.NewSkillRunnerTool(skillRegistry, pool, promptReg, inline)
	toolRegistry.Register(skillRunner)
}

func registerBuiltinWorkflows(workflowReg *multiagent.WorkflowRegistry) {
	for _, w := range multiagent.BuiltinPipelines() {
		workflowReg.Register(w)
	}
}

func restoreSessionState(agentMgr *core.AgentManager, snap core.SessionStateSnapshot, requestReviewTool *multiagent.RequestReviewTool, delegateTool *multiagent.DelegateTool, cfg *config.Config) {
	agentMgr.SetAgentDrivenChangeCallback(func(enabled bool) {
		if requestReviewTool != nil {
			requestReviewTool.Enabled = enabled
		}
		if delegateTool != nil {
			delegateTool.Enabled = enabled
		}
	})

	// Only an explicit companion minor mode restores the companion minor-mode
	// label. A bare AgentDrivenEnabled (e.g. left over from a team review
	// apply) must NOT force the companion minor mode — agent-driven tools
	// being on is independent of the companion minor-mode display, and
	// treating it as companion made it impossible to disable (bug).
	if snap.MinorMode == "companion" {
		if err := agentMgr.SetMinorMode("companion", true); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore minor mode: %v\n", err)
		}
	} else if snap.AgentDrivenEnabled {
		// Restore agent-driven tool availability without the companion label.
		if err := agentMgr.SetAgentDrivenEnabled(true); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore agent-driven state: %v\n", err)
		}
	}

	level := string(cfg.GetThinkingLevel("main_agent"))
	if snap.ThinkingLevel != "" {
		level = snap.ThinkingLevel
	}
	// RestoreThinkingLevel (not SetThinkingLevel): startup restore must not
	// re-save the value to the active model's config entry — a stale snapshot
	// value would otherwise be stamped onto a model the user never changed.
	if err := agentMgr.RestoreThinkingLevel(level); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to restore thinking level: %v\n", err)
	}
}

func wireAgentBus(agentMgr *core.AgentManager, pool *multiagent.AgentPool, orch *multiagent.ForegroundOrchestrator, maxCompanionCycles int) {
	agentBus := agentMgr.AgentBus()
	pool.SetAgentBus(agentBus)
	orch.SetAgentBus(agentBus)
	orch.SetCompanionMaxMessages(maxCompanionCycles)
}

func attachAgentDrivenToolPools(tools []agentic.Tool, pool *multiagent.AgentPool) {
	for _, t := range tools {
		switch v := t.(type) {
		case *multiagent.RequestReviewTool:
			v.Pool = pool
		case *multiagent.DelegateTool:
			v.Pool = pool
		}
	}
}
