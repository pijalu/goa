// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package team implements the TeamManager (TEAMS.md §4): it applies named
// team definitions to the existing subsystems (session model/mode/thinking,
// agent-pool role configs, companion review policy) with snapshot/restore
// semantics, drift detection, and goal-scoped overlays. The manager holds no
// agents; teams are configuration applied through narrow dependency
// interfaces (SOLID dependency inversion, mirroring core/orchestrator's
// GoalBinder).
package team

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/multiagent"
)

// Review policy application targets (TEAMS.md §4.2 step 5).
const (
	ReviewApplyOff       = "off"
	ReviewApplyAgent     = "agent"
	ReviewApplyFramework = "framework"
	ReviewApplyGated     = "gated"
)

// SessionController is the session surface the manager drives: active
// provider/model switch, behavioral mode, thinking level, and autonomy.
// Production implementation wraps *core.AgentManager + core.ProviderManager.
type SessionController interface {
	// SwitchModel applies the team's main model/provider to the session
	// (same semantics as /model: next-turn boundary, announced immediately).
	SwitchModel(providerID, modelID string) error
	// CurrentModel returns the config's active provider/model pair.
	CurrentModel() (providerID, modelID string)
	// CurrentMode returns the current mode state (major + autonomy).
	CurrentMode() internal.ModeState
	// SetMode replaces the current mode state (same semantics as /mode).
	SetMode(ms internal.ModeState) error
	// SetThinkingLevel applies a session thinking level (/thinking path).
	SetThinkingLevel(level string) error
	// CurrentThinkingLevel returns the session's effective thinking level.
	CurrentThinkingLevel() string
}

// PoolConfigurer applies team members to the agent pool.
// Production implementation wraps *multiagent.AgentPool.
type PoolConfigurer interface {
	// ApplyMember registers a member as a pool role (memberName == role),
	// honoring per-role model isolation and mode-derived toolsets.
	ApplyMember(role string, cfg multiagent.AgentConfig) error
	// RoleConfig returns the currently configured role (zero value when unset).
	RoleConfig(role string) multiagent.AgentConfig
	// Evict drops a cached agent so the next use rebuilds with the new config.
	Evict(role string)
}

// ReviewController applies the team's review policy to the companion
// subsystem (TEAMS.md §4.2 step 5). Production implementation wraps
// *core.AgentManager + *multiagent.ForegroundOrchestrator.
type ReviewController interface {
	// ApplyReview activates a review policy (off/agent/framework/gated) with
	// the team's gated triggers when policy == gated.
	ApplyReview(policy string, triggers []string) error
	// CurrentReview returns the active policy ("", "agent", "framework",
	// "gated") and configured triggers.
	CurrentReview() (policy string, triggers []string)
}

// MemberApplier resolves a team member into the pool registration inputs:
// model/provider IDs and the mode-derived reasoning effort + tool allowlist.
// Kept narrow so tests can stub mode/thinking resolution.
type MemberApplier interface {
	// MemberConfig builds the pool AgentConfig for a normalized member:
	// ModelName/ProviderID from the member, ReasoningEffort from the team's
	// §3.6 thinking resolution, AllowedTools from the member's mode.
	MemberConfig(def config.TeamDefinition, rm config.ResolvedMember) (multiagent.AgentConfig, error)
}

// sessionSnapshot captures the pre-team session state for restore (§4.2
// step 2). Pool role configs are snapshotted separately per touched role.
type sessionSnapshot struct {
	providerID    string
	modelID       string
	mode          internal.ModeState
	thinkingLevel string
	reviewPolicy  string
	reviewTrigger []string
	roleConfigs   map[string]multiagent.AgentConfig
	roleExisted   map[string]bool
}

// Manager applies team definitions to the session with snapshot/restore
// semantics. Exactly one session-level team may be active; additionally one
// goal-scoped overlay may sit on top (TEAMS.md §5.2). All methods are safe
// for concurrent use.
type Manager struct {
	mu sync.Mutex

	cfg     *config.Config
	session SessionController
	pool    PoolConfigurer
	review  ReviewController
	members MemberApplier
	emitLog func(event string, payload map[string]any)

	active          string           // session-level active team ("" = none)
	activeSnapshot  *sessionSnapshot // pre-team baseline for restore
	overlayTeam     string           // goal-scoped overlay ("" = none)
	overlaySnapshot *sessionSnapshot // pre-overlay snapshot
	drifted         bool             // manual override since activation

	// onChange fires after every visibility-relevant transition (activation,
	// deactivation, overlay apply/remove) with the new effective team name and
	// a human-readable reason ("activated", "overlay", "deactivated",
	// "overlay removed"). The app wires it to a chat announcement + footer
	// refresh so a team is never hidden from the user (team UI bug RC-4).
	// Called with the manager lock NOT held; must not re-enter the manager.
	onChange func(effective, reason string)
}

// SetChangeCallback installs the transition observer (nil disables).
func (m *Manager) SetChangeCallback(cb func(effective, reason string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = cb
}

// notifyChange invokes the change callback outside the manager lock.
func (m *Manager) notifyChange(reason string) {
	m.mu.Lock()
	cb := m.onChange
	effective := m.overlayTeam
	if effective == "" {
		effective = m.active
	}
	m.mu.Unlock()
	if cb != nil {
		cb(effective, reason)
	}
}

// NewManager builds a TeamManager. Nil dependencies are tolerated so the
// manager can be constructed before all subsystems exist; operations that
// need a missing dependency return an error.
func NewManager(cfg *config.Config, session SessionController, pool PoolConfigurer, review ReviewController, members MemberApplier, emitLog func(string, map[string]any)) *Manager {
	return &Manager{
		cfg: cfg, session: session, pool: pool, review: review,
		members: members, emitLog: emitLog,
	}
}

// Active returns the session-level active team name ("" = none).
func (m *Manager) Active() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// OverlayTeam returns the goal-scoped overlay team name ("" = none).
func (m *Manager) OverlayTeam() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.overlayTeam
}

// EffectiveTeam returns the team currently governing the session: the
// goal overlay when present, else the session-level team (§5.2 precedence).
func (m *Manager) EffectiveTeam() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.overlayTeam != "" {
		return m.overlayTeam
	}
	return m.active
}

// Drifted reports whether the active team has been manually overridden
// (model/mode/thinking/companion change after activation).
func (m *Manager) Drifted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.drifted
}

// MarkDrift flags the active team as manually overridden (§4.4).
func (m *Manager) MarkDrift() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != "" || m.overlayTeam != "" {
		m.drifted = true
	}
}

// Resolve returns a defined team by name.
func (m *Manager) Resolve(name string) (config.TeamDefinition, bool) {
	def, ok := m.cfg.Teams.Definitions[name]
	return def, ok
}

// Activate applies a session-level team (§4.2). Re-activation restores the
// pre-team baseline first so nested activation cannot leak state. On any
// failure the previous state is restored and the error returned.
func (m *Manager) Activate(name string) error {
	m.mu.Lock()
	if m.overlayTeam != "" {
		m.mu.Unlock()
		return fmt.Errorf("cannot switch team while goal overlay %q is active", m.overlayTeam)
	}
	if m.active != "" {
		var drop string
		if err := m.restoreLocked(&drop); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("restore previous team: %w", err)
		}
	}
	err := m.applyLocked(name, false)
	m.mu.Unlock()
	if err == nil {
		m.notifyChange("activated")
	}
	return err
}

// Deactivate restores the pre-team session state (§4.2).
func (m *Manager) Deactivate() error {
	m.mu.Lock()
	var reason string
	err := m.restoreLocked(&reason)
	m.mu.Unlock()
	if err == nil && reason != "" {
		m.notifyChange(reason)
	}
	return err
}

// ApplyOverlay applies a team for the duration of a goal (§5.2). The
// current session state (including any session-level team) is snapshotted
// and restored by RemoveOverlay.
func (m *Manager) ApplyOverlay(name string) error {
	m.mu.Lock()
	if m.overlayTeam != "" {
		m.mu.Unlock()
		return fmt.Errorf("goal overlay %q already active", m.overlayTeam)
	}
	err := m.applyLocked(name, true)
	m.mu.Unlock()
	if err == nil {
		m.notifyChange("overlay")
	}
	return err
}

// RemoveOverlay tears down the goal overlay, restoring the session-level
// state (§5.2).
func (m *Manager) RemoveOverlay() error {
	m.mu.Lock()
	var reason string
	err := m.restoreLocked(&reason)
	m.mu.Unlock()
	if err == nil && reason != "" {
		m.notifyChange(reason)
	}
	return err
}

// Sync re-applies the effective team definition, clearing drift (§4.4).
func (m *Manager) Sync() error {
	m.mu.Lock()
	name := m.overlayTeam
	overlay := true
	if name == "" {
		name, overlay = m.active, false
	}
	if name == "" {
		m.mu.Unlock()
		return errors.New("no team active")
	}
	var drop string
	if err := m.restoreLocked(&drop); err != nil {
		m.mu.Unlock()
		return err
	}
	err := m.applyLocked(name, overlay)
	m.mu.Unlock()
	if err == nil {
		m.notifyChange("synced")
	}
	return err
}

// applyLocked snapshots then applies a team definition. overlay selects the
// goal-overlay slot vs the session slot.
func (m *Manager) applyLocked(name string, overlay bool) error {
	def, ok := m.cfg.Teams.Definitions[name]
	if !ok {
		return fmt.Errorf("team %q not defined (defined: %v)", name, m.cfg.TeamNames())
	}
	snap, err := m.snapshotLocked(def)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	// Suppress model persistence for the team's duration (RC-5): the team's
	// model switch must never be written back as the user's saved model.
	m.setModelPersistenceSuppressedLocked(true)
	if err := m.applyDefinitionLocked(def); err != nil {
		// All-or-nothing: attempt to restore whatever was applied.
		_ = m.restoreSnapshotLocked(snap)
		m.setModelPersistenceSuppressedLocked(m.overlaySnapshot != nil || m.activeSnapshot != nil)
		return fmt.Errorf("apply team %q: %w", name, err)
	}
	if overlay {
		m.overlayTeam, m.overlaySnapshot = name, snap
	} else {
		m.active, m.activeSnapshot = name, snap
	}
	m.drifted = false
	m.emitLocked("team.activated", map[string]any{
		"team": name, "overlay": overlay, "review": def.EffectiveReview(),
	})
	return nil
}

// restoreLocked restores the most recent application (overlay first, then
// the session-level team). It reports what it restored via the reason out
// param ("overlay removed" / "deactivated" / "" when nothing was active) so
// callers can notify after releasing the lock.
func (m *Manager) restoreLocked(reason *string) error {
	switch {
	case m.overlaySnapshot != nil:
		if err := m.restoreSnapshotLocked(m.overlaySnapshot); err != nil {
			return err
		}
		m.emitLocked("team.deactivated", map[string]any{"team": m.overlayTeam, "overlay": true})
		m.overlayTeam, m.overlaySnapshot = "", nil
		*reason = "overlay removed"
	case m.activeSnapshot != nil:
		if err := m.restoreSnapshotLocked(m.activeSnapshot); err != nil {
			return err
		}
		m.emitLocked("team.deactivated", map[string]any{"team": m.active, "overlay": false})
		m.active, m.activeSnapshot = "", nil
		*reason = "deactivated"
	}
	m.drifted = false
	// Persistence guard follows the remaining team state: when nothing is
	// active anymore the user's own model choices may be saved again (RC-5).
	m.setModelPersistenceSuppressedLocked(m.overlaySnapshot != nil || m.activeSnapshot != nil)
	return nil
}

// setModelPersistenceSuppressedLocked toggles model persistence on the
// session controller when it supports the guard (team.Manager drives this so
// the overlay model never leaks into the user's saved config, RC-5).
// Lock held by caller.
func (m *Manager) setModelPersistenceSuppressedLocked(suppressed bool) {
	if g, ok := m.session.(ModelPersistenceGuard); ok {
		g.SuppressModelPersistence(suppressed)
	}
}

// ModelPersistenceGuard is an optional SessionController extension: while a
// team governs the session model, saving active_provider/active_model must be
// suppressed so the team's model is never persisted as the user's choice.
type ModelPersistenceGuard interface {
	SuppressModelPersistence(suppressed bool)
}

// snapshotLocked captures the pre-application session state, including the
// pool configs of every role the team will touch.
func (m *Manager) snapshotLocked(def config.TeamDefinition) (*sessionSnapshot, error) {
	if m.session == nil {
		return nil, errors.New("no session controller")
	}
	snap := &sessionSnapshot{
		mode:          m.session.CurrentMode(),
		thinkingLevel: m.session.CurrentThinkingLevel(),
		roleConfigs:   map[string]multiagent.AgentConfig{},
		roleExisted:   map[string]bool{},
	}
	snap.providerID, snap.modelID = m.session.CurrentModel()
	if m.review != nil {
		snap.reviewPolicy, snap.reviewTrigger = m.review.CurrentReview()
	}
	m.snapshotPoolRolesLocked(def, snap)
	return snap, nil
}

// snapshotPoolRolesLocked captures the pool configs of every role the team
// will touch (member names + the companion alias for the first reviewer).
func (m *Manager) snapshotPoolRolesLocked(def config.TeamDefinition, snap *sessionSnapshot) {
	if m.pool == nil {
		return
	}
	members, err := def.ResolvedMembers()
	if err != nil {
		return
	}
	for _, rm := range members {
		roles := []string{rm.Name}
		if isFirstReviewer(def, rm) {
			roles = append(roles, "companion")
		}
		for _, role := range roles {
			if _, seen := snap.roleExisted[role]; seen {
				continue
			}
			rc := m.pool.RoleConfig(role)
			snap.roleConfigs[role] = rc
			snap.roleExisted[role] = rc.ModelName != "" || rc.ProviderID != ""
		}
	}
}

// applyDefinitionLocked applies the normalized members to the session.
func (m *Manager) applyDefinitionLocked(def config.TeamDefinition) error {
	main, ok := def.MainMember()
	if !ok {
		return errors.New("team has no main member")
	}
	if err := m.applyMainMemberLocked(def, main); err != nil {
		return err
	}
	if err := m.applyPoolMembersLocked(def); err != nil {
		return err
	}
	return m.applyReviewPolicyLocked(def)
}

// applyMainMemberLocked implements §4.2 step 3: the main member drives the
// session's model, mode (with team autonomy default), and thinking level.
func (m *Manager) applyMainMemberLocked(def config.TeamDefinition, main config.ResolvedMember) error {
	if err := m.session.SwitchModel(main.Member.Provider, main.Member.Model); err != nil {
		return fmt.Errorf("switch main model: %w", err)
	}
	if err := m.applyMainModeLocked(def, main); err != nil {
		return err
	}
	if tl := main.Member.ThinkingLevel; tl != "" {
		// Member override wins (§3.6); the model's own saved level already
		// applies via SwitchModel (SetModel restores the per-model level).
		if err := m.session.SetThinkingLevel(tl); err != nil {
			return fmt.Errorf("apply thinking level: %w", err)
		}
	}
	return nil
}

// applyMainModeLocked switches the session mode when the main member names
// one, and applies the team's autonomy default when set.
func (m *Manager) applyMainModeLocked(def config.TeamDefinition, main config.ResolvedMember) error {
	cur := m.session.CurrentMode()
	next := internal.ModeState{Major: cur.Major, Skills: cur.Skills, Autonomy: cur.Autonomy}
	if main.Member.Mode != "" {
		next.Major = internal.MajorMode(main.Member.Mode)
	}
	if def.Defaults.Autonomy != "" {
		next.Autonomy = internal.AutonomyLevel(def.Defaults.Autonomy)
	}
	if reflect.DeepEqual(next, cur) {
		return nil
	}
	if err := m.session.SetMode(next); err != nil {
		return fmt.Errorf("switch mode: %w", err)
	}
	return nil
}

// applyPoolMembersLocked implements §4.2 step 4: every non-main member
// registers under its member name; the first reviewer additionally registers
// under the backward-compatible "companion" role so existing companion call
// sites keep working. Evict precedes apply (Evict drops the cached agent AND
// its config, so applying first would be wiped).
func (m *Manager) applyPoolMembersLocked(def config.TeamDefinition) error {
	if m.pool == nil || m.members == nil {
		return nil
	}
	members, err := def.ResolvedMembers()
	if err != nil {
		return err
	}
	for _, rm := range members {
		if rm.Member.Role == config.TeamRoleMain {
			continue // the main member drives the session, no pool role
		}
		ac, err := m.members.MemberConfig(def, rm)
		if err != nil {
			return fmt.Errorf("member %q: %w", rm.Name, err)
		}
		m.pool.Evict(rm.Name)
		if err := m.pool.ApplyMember(rm.Name, ac); err != nil {
			return fmt.Errorf("member %q: %w", rm.Name, err)
		}
		if isFirstReviewer(def, rm) {
			m.pool.Evict("companion")
			if err := m.pool.ApplyMember("companion", ac); err != nil {
				return fmt.Errorf("member %q (companion alias): %w", rm.Name, err)
			}
		}
	}
	return nil
}

// applyReviewPolicyLocked implements §4.2 step 5.
func (m *Manager) applyReviewPolicyLocked(def config.TeamDefinition) error {
	if m.review == nil {
		return nil
	}
	policy := def.EffectiveReview()
	if policy == "" {
		policy = ReviewApplyOff
	}
	if err := m.review.ApplyReview(policy, def.ReviewGates.Triggers); err != nil {
		return fmt.Errorf("apply review policy: %w", err)
	}
	return nil
}

// restoreSnapshotLocked re-applies a captured snapshot.
func (m *Manager) restoreSnapshotLocked(snap *sessionSnapshot) error {
	if err := m.restorePoolRolesLocked(snap); err != nil {
		return err
	}
	if err := m.restoreReviewLocked(snap); err != nil {
		return err
	}
	return m.restoreSessionLocked(snap)
}

// restorePoolRolesLocked re-applies snapshotted pool configs. Evict first
// (drops cached agent AND current config), then re-apply — same ordering as
// the apply path.
func (m *Manager) restorePoolRolesLocked(snap *sessionSnapshot) error {
	if m.pool == nil {
		return nil
	}
	for role, rc := range snap.roleConfigs {
		m.pool.Evict(role)
		if snap.roleExisted[role] {
			if err := m.pool.ApplyMember(role, rc); err != nil {
				return fmt.Errorf("restore pool role %q: %w", role, err)
			}
		}
	}
	return nil
}

// restoreReviewLocked re-applies the snapshotted review policy.
func (m *Manager) restoreReviewLocked(snap *sessionSnapshot) error {
	if m.review == nil {
		return nil
	}
	policy := snap.reviewPolicy
	if policy == "" {
		policy = ReviewApplyOff
	}
	if err := m.review.ApplyReview(policy, snap.reviewTrigger); err != nil {
		return fmt.Errorf("restore review policy: %w", err)
	}
	return nil
}

// restoreSessionLocked re-applies the snapshotted model/mode/thinking.
func (m *Manager) restoreSessionLocked(snap *sessionSnapshot) error {
	if err := m.session.SwitchModel(snap.providerID, snap.modelID); err != nil {
		return fmt.Errorf("restore model: %w", err)
	}
	if err := m.session.SetMode(snap.mode); err != nil {
		return fmt.Errorf("restore mode: %w", err)
	}
	if snap.thinkingLevel != "" {
		if err := m.session.SetThinkingLevel(snap.thinkingLevel); err != nil {
			return fmt.Errorf("restore thinking level: %w", err)
		}
	}
	return nil
}

// emitLocked appends to the team event log when configured (§9).
func (m *Manager) emitLocked(event string, payload map[string]any) {
	if m.emitLog != nil {
		m.emitLog(event, payload)
	}
}

// isFirstReviewer reports whether rm is the team's first reviewer (the one
// aliased to the backward-compatible "companion" pool role).
func isFirstReviewer(def config.TeamDefinition, rm config.ResolvedMember) bool {
	if rm.Member.Role != config.TeamRoleReviewer {
		return false
	}
	reviewers := def.Reviewers()
	return len(reviewers) > 0 && reviewers[0].Name == rm.Name
}

// SortedMemberNames returns normalized member names in sorted order (for
// status rendering and tests).
func SortedMemberNames(def config.TeamDefinition) []string {
	members, err := def.ResolvedMembers()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(members))
	for _, rm := range members {
		names = append(names, rm.Name)
	}
	sort.Strings(names)
	return names
}
