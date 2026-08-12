// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/team"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// teamSessionController adapts *core.AgentManager + the provider manager to
// team.SessionController (TEAMS.md §4.2 step 3). Model switches go through
// the same path as /model; thinking through RestoreThinkingLevel (team
// overlays never stamp the model's saved level, §3.6).
type teamSessionController struct {
	cfg *config.Config
	pm  core.ProviderManager
	am  *core.AgentManager
}

func (c *teamSessionController) SwitchModel(providerID, modelID string) error {
	pid := providerID
	if pid == "" {
		pid = c.cfg.ActiveProvider
	}
	if _, ok := modelByID(c.cfg, modelID); !ok && modelID != c.cfg.ActiveModel {
		return fmt.Errorf("model %q not found in models list", modelID)
	}
	prevProvider, prevModel := c.cfg.ActiveProvider, c.cfg.ActiveModel
	c.cfg.ActiveProvider, c.cfg.ActiveModel = pid, modelID
	if c.pm == nil || c.am == nil {
		return nil // headless construction: config-level switch only
	}
	if err := c.pm.SetActive(pid, modelID); err != nil {
		c.cfg.ActiveProvider, c.cfg.ActiveModel = prevProvider, prevModel
		return fmt.Errorf("switch to %s: %w", modelID, err)
	}
	if mdl, err := c.pm.ResolveActiveModel(); err == nil {
		c.am.SetModel(mdl)
	}
	c.am.SetStreamOptions(c.pm.BuildStreamOptions())
	return nil
}

func (c *teamSessionController) CurrentModel() (string, string) {
	return c.cfg.ActiveProvider, c.cfg.ActiveModel
}

func (c *teamSessionController) CurrentMode() internal.ModeState {
	if c.am == nil {
		return c.cfg.DefaultModeState()
	}
	return c.am.CurrentMode()
}

func (c *teamSessionController) SetMode(ms internal.ModeState) error {
	if c.am != nil {
		c.am.SetMode(ms)
	}
	return nil
}

func (c *teamSessionController) SetThinkingLevel(level string) error {
	if c.am == nil {
		return nil
	}
	// RestoreThinkingLevel: apply without saving onto the model's config
	// entry — the team's override is an overlay, §3.6.
	return c.am.RestoreThinkingLevel(level)
}

func (c *teamSessionController) CurrentThinkingLevel() string {
	// Resolve from config: the main-agent thinking level reflects runtime
	// /thinking changes (saved per model) and the global defaults — the same
	// value the session restores on model switch.
	return string(c.cfg.GetThinkingLevel("main_agent"))
}

// modelByID reports whether a model ID exists in the config.
func modelByID(cfg *config.Config, id string) (config.ModelConfig, bool) {
	for _, m := range cfg.Models {
		if m.ID == id {
			return m, true
		}
	}
	return config.ModelConfig{}, false
}

// teamPoolConfigurer adapts *multiagent.AgentPool to team.PoolConfigurer.
type teamPoolConfigurer struct {
	pool *multiagent.AgentPool
}

func (p *teamPoolConfigurer) ApplyMember(role string, cfg multiagent.AgentConfig) error {
	if p.pool == nil {
		return nil
	}
	p.pool.SetConfig(role, cfg)
	return nil
}

func (p *teamPoolConfigurer) RoleConfig(role string) multiagent.AgentConfig {
	if p.pool == nil {
		return multiagent.AgentConfig{}
	}
	return p.pool.RoleConfig(role)
}

func (p *teamPoolConfigurer) Evict(role string) {
	if p.pool != nil {
		p.pool.Evict(role)
	}
}

// teamReviewController adapts the companion subsystem (*core.AgentManager +
// *multiagent.ForegroundOrchestrator) to team.ReviewController (§4.2 step 5).
// The gated policy is introduced in Phase 5; applying it now records the
// triggers and behaves like framework until WorkflowCompanionGated lands.
type teamReviewController struct {
	am   *core.AgentManager
	orch *multiagent.ForegroundOrchestrator

	gatedTriggers []string // recorded for Phase 5 wiring
}

func (r *teamReviewController) ApplyReview(policy string, triggers []string) error {
	r.gatedTriggers = append([]string(nil), triggers...)
	if r.orch == nil || r.am == nil {
		return nil
	}
	switch policy {
	case team.ReviewApplyOff:
		r.orch.SetMode(multiagent.WorkflowInactive)
		// Fully tear down the companion/agent-driven state, not just the
		// orchestrator mode: a prior agent/framework apply enabled agent-driven
		// (persisted), and leaving it set re-asserts companion on every restart.
		if err := r.am.SetAgentDrivenEnabled(false); err != nil {
			return err
		}
		return r.am.InjectCompanionReview(false)
	case team.ReviewApplyAgent:
		r.orch.SetMode(multiagent.WorkflowAgentDriven)
		if err := r.am.SetAgentDrivenEnabled(true); err != nil {
			return err
		}
		return r.am.InjectCompanionReview(true)
	case team.ReviewApplyFramework, team.ReviewApplyGated:
		// Phase 5 replaces this with WorkflowCompanionGated when policy ==
		// gated; framework semantics are the safe interim.
		r.orch.SetMode(multiagent.WorkflowCompanionMinor)
		if err := r.am.SetAgentDrivenEnabled(true); err != nil {
			return err
		}
		return r.am.InjectCompanionReview(false)
	default:
		return fmt.Errorf("unknown review policy %q", policy)
	}
}

func (r *teamReviewController) CurrentReview() (string, []string) {
	if r.orch == nil {
		return "", nil
	}
	switch r.orch.Mode() {
	case multiagent.WorkflowAgentDriven:
		return team.ReviewApplyAgent, nil
	case multiagent.WorkflowCompanionMinor:
		if len(r.gatedTriggers) > 0 {
			return team.ReviewApplyGated, append([]string(nil), r.gatedTriggers...)
		}
		return team.ReviewApplyFramework, nil
	default:
		return "", nil
	}
}

// teamMemberApplier builds pool AgentConfigs for team members: per-member
// model/provider, §3.6 thinking resolution, and mode-derived tool allowlist
// (mirrors configureRoleModels' resolveAllowed).
type teamMemberApplier struct {
	cfg          *config.Config
	modeRegistry *core.ModeRegistry
}

func (a *teamMemberApplier) MemberConfig(def config.TeamDefinition, rm config.ResolvedMember) (multiagent.AgentConfig, error) {
	if rm.Member.Model == "" {
		return multiagent.AgentConfig{}, fmt.Errorf("member %q has no model", rm.Name)
	}
	ac := multiagent.AgentConfig{
		ModelName:       rm.Member.Model,
		ProviderID:      rm.Member.Provider,
		ReasoningEffort: agentic.ReasoningEffort(a.cfg.TeamMemberThinking(def, rm)),
	}
	if rm.Member.Mode != "" && a.modeRegistry != nil {
		if spec, err := a.modeRegistry.Resolve(internal.MajorMode(rm.Member.Mode)); err == nil && len(spec.AllowedTools) > 0 {
			ac.AllowedTools = spec.AllowedTools
		}
	}
	return ac, nil
}

// newTeamManager constructs the TeamManager and its adapters (TEAMS-PLAN
// Phase 2 step 13). Nil pool/orchestrator/agentMgr are tolerated for
// headless construction; activation then reduces to config-level switches.
func newTeamManager(cfg *config.Config, pm core.ProviderManager, agentMgr *core.AgentManager, pool *multiagent.AgentPool, orch *multiagent.ForegroundOrchestrator, modeRegistry *core.ModeRegistry, emitLog func(string, map[string]any)) *team.Manager {
	return team.NewManager(
		cfg,
		&teamSessionController{cfg: cfg, pm: pm, am: agentMgr},
		&teamPoolConfigurer{pool: pool},
		&teamReviewController{am: agentMgr, orch: orch},
		&teamMemberApplier{cfg: cfg, modeRegistry: modeRegistry},
		emitLog,
	)
}
