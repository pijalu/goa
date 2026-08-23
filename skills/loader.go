// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package skills implements the skill system: discovery, loading, and
// execution. Skills are defined as SKILL.md files with YAML frontmatter.
// They can be inline (injected into system prompt) or sub-agent (run as
// a separate agent via SkillRunner).
package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pijalu/goa/internal/embeddoc"
)

// SkillMeta holds the parsed metadata from a skill's frontmatter.
type SkillMeta struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Command     string         `yaml:"command"`
	Inline      bool           `yaml:"inline"`
	Mode        string         `yaml:"mode"`
	Category    string         `yaml:"category"` // "knowledge" or "action" (default)
	Autonomy    string         `yaml:"autonomy,omitempty"`
	MaxTokens   int            `yaml:"max_tokens,omitempty"`
	Temperature float64        `yaml:"temperature"`
	Tools       []string       `yaml:"tools"`
	Skills      []string       `yaml:"skills"`
	SubSkills   []string       `yaml:"sub-skills"`
	InputSchema map[string]any `yaml:"input-schema"`
	Hidden      bool           `yaml:"hidden"`
	// Sticky marks a knowledge skill as always-on: its body is persisted into
	// every agent's conversation history once per session (and re-persisted
	// after any context compression that may have dropped it), instead of
	// requiring explicit /skill:run activation. Restricted to
	// knowledge-category skills — sticky action skills are ignored.
	Sticky bool `yaml:"sticky"`
	// ModelInvocable reports whether the skill may be invoked by the model
	// through the <available_skills> catalog listing.
	// Defaults to true when omitted.
	ModelInvocable bool `yaml:"model_invocable"`
	// UserInvocable reports whether the skill may be invoked by the user from
	// the TUI skill menu (/skills). Defaults to true when omitted.
	UserInvocable bool `yaml:"user_invocable"`
}

// UnmarshalYAML defaults the invocation policy to fully invocable when the
// frontmatter omits either flag: a bare bool zero value would otherwise mean
// "not invocable" for every skill without an explicit policy. Explicit
// `model_invocable: false` / `user_invocable: false` still win after the
// defaults are set.
func (m *SkillMeta) UnmarshalYAML(value *yaml.Node) error {
	m.ModelInvocable = true
	m.UserInvocable = true
	type rawSkillMeta SkillMeta
	return value.Decode((*rawSkillMeta)(m))
}

// Skill represents a fully loaded skill with metadata and instructions.
type Skill struct {
	Meta     SkillMeta
	Body     string // markdown body (after frontmatter)
	Source   string // "embedded", "home", "project"
	FilePath string
}

// LinkedMode returns the major mode this skill is linked to, or empty string.
func (s *Skill) LinkedMode() string {
	return s.Meta.Mode
}

// SuggestedSkills returns the skills this skill suggests activating.
func (s *Skill) SuggestedSkills() []string {
	return s.Meta.Skills
}

// ImportedSkills returns the names of skills imported by this skill for use
// inside its sub-agent.
func (s *Skill) ImportedSkills() []string {
	return s.Meta.Skills
}

// HasSubSkills reports whether the skill references sub-skills that must be
// executed inside a sub-agent.
func (s *Skill) HasSubSkills() bool {
	return len(s.Meta.SubSkills) > 0
}

// IsInline returns true if the named skill is an inline skill.
func (r *SkillRegistry) IsInline(name string) bool {
	s, ok := r.skills[name]
	return ok && s.Meta.Inline
}

// IsSticky reports whether the named skill is sticky: an always-on knowledge
// skill whose body is auto-persisted into agent history. Sticky is only
// honored for knowledge-category skills; a sticky action skill is a config
// error and reports false.
func (s *Skill) IsSticky() bool {
	return s.Meta.Sticky && s.Meta.Category != SkillCategoryAction && !s.Meta.Hidden
}

// IsModelInvocable reports whether the skill may be invoked by the model via
// the <available_skills> catalog listing. The model-facing predicate requires
// BOTH flags (P16) and NOT hidden: hidden/internal skills (e.g. dream) stay
// loaded for internal features but are never advertised to the model.
func (s *Skill) IsModelInvocable() bool {
	return s.Meta.ModelInvocable && s.Meta.UserInvocable && !s.Meta.Hidden
}

// IsUserInvocable reports whether the skill may be invoked by the user from
// the TUI skill menu (/skills). A model_invocable:false skill remains
// user-invocable and still runs from the UI (P16 acceptance).
func (s *Skill) IsUserInvocable() bool {
	return s.Meta.UserInvocable
}

// StickySkills returns all sticky knowledge skills, sorted by name for
// byte-stable rendering. Hidden and action-category skills are excluded even
// when they declare sticky: true. Like the other registry accessors, this
// reads the load-time map without locking — registries are populated by
// LoadAll before use.
func (r *SkillRegistry) StickySkills() []*Skill {
	var out []*Skill
	for _, s := range r.skills {
		if s.IsSticky() {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.Name < out[j].Meta.Name })
	return out
}

// StickyBodies renders each sticky skill as a labelled instruction block for
// history persistence, in sorted order. The output is byte-stable for an
// unchanged registry so callers can dedup re-persistence by string compare.
func (r *SkillRegistry) StickyBodies() []string {
	sticky := r.StickySkills()
	blocks := make([]string, 0, len(sticky))
	for _, s := range sticky {
		blocks = append(blocks, fmt.Sprintf("<sticky_skill name=%q>\n%s\n</sticky_skill>", s.Meta.Name, strings.TrimSpace(s.Body)))
	}
	return blocks
}

// SubSkills returns the sub-skills registered for the named skill, or nil
// if the skill has no sub-skills or is not found.
func (r *SkillRegistry) SubSkills(name string) []*Skill {
	return r.subSkills[name]
}

// ImportedSkills returns the skills imported by the named skill for use
// inside its sub-agent, or nil if the skill is not found.
func (r *SkillRegistry) ImportedSkills(name string) []*Skill {
	s, ok := r.skills[name]
	if !ok {
		return nil
	}
	var out []*Skill
	for _, impName := range s.Meta.Skills {
		if imp, ok := r.skills[impName]; ok {
			out = append(out, imp)
		}
	}
	return out
}

// HasSubSkills reports whether the named skill has sub-skills, either from
// frontmatter or from a skills/ subdirectory.
func (r *SkillRegistry) HasSubSkills(name string) bool {
	return r.hasAnySubSkill(name)
}

// SkillSummary is a lightweight skill description for listing.
type SkillSummary struct {
	Name             string
	Description      string
	Inline           bool
	Category         string
	FilePath         string
	RequiresSubAgent bool
	// Sticky reports the EFFECTIVE always-on state (frontmatter sticky
	// combined with the skills.sticky / skills.sticky_off config overrides)
	// for banners and listings; the category/hidden gates of IsSticky apply.
	Sticky bool
	// Source is the origin of the skill: "embedded" for compiled-in skills,
	// "file" for skills loaded from a directory (home/project/plugin dirs).
	Source string
	// ModelInvocable / UserInvocable mirror the frontmatter invocation
	// policy (P16), both defaulting to true. Consumers filter by the
	// predicate matching their surface: model-facing catalogs use
	// IsModelInvocable, the user-facing TUI menu uses IsUserInvocable.
	ModelInvocable bool
	UserInvocable  bool
	// Hidden marks internal skills (e.g. dream): loaded for internal feature
	// use, but never advertised to or invocable by the model.
	Hidden bool
}

// IsModelInvocable reports whether the skill may be advertised to the model.
// The model-facing predicate requires BOTH flags (P16) and NOT hidden: a
// hidden skill is internal-only, regardless of its invocation flags.
func (s SkillSummary) IsModelInvocable() bool {
	return s.ModelInvocable && s.UserInvocable && !s.Hidden
}

// IsUserInvocable reports whether the skill may appear in the user-facing
// TUI skill menu. model_invocable:false skills remain user-invocable.
func (s SkillSummary) IsUserInvocable() bool {
	return s.UserInvocable
}

const (
	// SkillCategoryAction indicates a skill that performs actions and must be
	// explicitly invoked via /skill:run or the run_skill tool.
	SkillCategoryAction = "action"

	// SkillCategoryKnowledge indicates a skill that provides knowledge or
	// instructions that can be injected into the system prompt.
	SkillCategoryKnowledge = "knowledge"
)

// TrustChecker decides whether a filesystem skill is trusted. Embedded
// skills are always trusted regardless of this checker.
type TrustChecker interface {
	IsTrusted(name, filePath string) (bool, error)
}

// SkillRegistry manages discovery and loading of skills from embedded
// (compiled-in), home (~/.agents/skills/), and project (.agents/skills/)
// directories. Embedded skills are discovered by walking the embedded
// filesystem directory tree — no manual registration needed.
type SkillRegistry struct {
	skills       map[string]*Skill
	subSkills    map[string][]*Skill // parent skill name → sub-skills loaded from skills/ subdir
	dirs         []string
	embedFS      fs.FS        // optional embedded filesystem for built-in skills
	trustChecker TrustChecker // nil means all filesystem skills are trusted
	disabled     map[string]bool
	enabled      map[string]bool // non-nil → allowlist; only listed skills load
	// embeddedDefaultDisabled lists embedded skills that are OFF by default
	// (all embedded skills except telegram). A default-off skill
	// loads only when the user explicitly opts it back in via the embedded
	// opt-in list (embeddedEnabled) or the global Enabled allowlist. It
	// applies ONLY to the embedded source — home/project/plugin file skills
	// are never affected.
	embeddedDefaultDisabled map[string]bool
	// embeddedEnabled is the embedded-scoped opt-in list (skills.embedded_enabled).
	embeddedEnabled map[string]bool
	// stickyOn / stickyOff are the config-level sticky overrides
	// (skills.sticky / skills.sticky_off): on forces a knowledge skill
	// sticky, off forces a frontmatter-sticky skill back to on-demand (off
	// wins). Applied to Meta.Sticky during LoadAll.
	stickyOn  map[string]bool
	stickyOff map[string]bool
	// stickyFront records the pristine frontmatter sticky flag per loaded
	// skill (before overrides) so toggles can write the minimal config list.
	stickyFront map[string]bool
	homeDir     string // home dir path for source labeling ("home")
	projectDir  string // project dir path for source labeling ("project")
}

// NewSkillRegistry creates a registry that scans the given directories.
func NewSkillRegistry(dirs []string) *SkillRegistry {
	return &SkillRegistry{
		skills:    make(map[string]*Skill),
		subSkills: make(map[string][]*Skill),
		dirs:      dirs,
	}
}

// SetEmbeddedFS sets the embedded filesystem for built-in skills.
// Skills are discovered by walking the FS for */SKILL.md entries.
func (r *SkillRegistry) SetEmbeddedFS(efs fs.FS) {
	r.embedFS = efs
}

// SetDisabled marks skill names as disabled for ALL sources (embedded and
// file-based): they are skipped during LoadAll, so they never reach the system
// prompt listing, the skills banner, or the <available_skills> catalog. A name
// in both Enabled and Disabled is disabled (explicit off wins). Must be called
// before LoadAll.
func (r *SkillRegistry) SetDisabled(names []string) {
	if len(names) == 0 {
		return
	}
	if r.disabled == nil {
		r.disabled = make(map[string]bool, len(names))
	}
	for _, n := range names {
		r.disabled[n] = true
	}
}

// SetEnabled installs an allowlist of skill names for ALL sources (embedded
// and file-based): when non-empty, only the listed skills are registered
// during LoadAll. Empty means all skills are eligible. Must be called before
// LoadAll.
func (r *SkillRegistry) SetEnabled(names []string) {
	if len(names) == 0 {
		return
	}
	r.enabled = make(map[string]bool, len(names))
	for _, n := range names {
		r.enabled[n] = true
	}
}

// SetEmbeddedDefaultDisabled marks embedded skill names as OFF by default.
// These are skipped during the embedded scan ONLY when the user has not
// explicitly opted them back in (via the embedded opt-in list or the global
// Enabled allowlist). File-based skills from home/project/plugin dirs are
// never affected. Must be called before LoadAll.
func (r *SkillRegistry) SetEmbeddedDefaultDisabled(names []string) {
	if len(names) == 0 {
		return
	}
	if r.embeddedDefaultDisabled == nil {
		r.embeddedDefaultDisabled = make(map[string]bool, len(names))
	}
	for _, n := range names {
		r.embeddedDefaultDisabled[n] = true
	}
}

// SetEmbeddedEnabled installs the embedded-scoped opt-in list: names here
// re-enable a default-off embedded skill WITHOUT activating the global
// Enabled allowlist (which would suppress file-based skills). Must be called
// before LoadAll.
func (r *SkillRegistry) SetEmbeddedEnabled(names []string) {
	if len(names) == 0 {
		return
	}
	r.embeddedEnabled = make(map[string]bool, len(names))
	for _, n := range names {
		r.embeddedEnabled[n] = true
	}
}

// SetStickyOverrides installs the config-level sticky overrides applied
// during LoadAll: names in off force a frontmatter-sticky skill back to
// on-demand (off wins over on, mirroring the Disabled semantics), names in
// on force a plain knowledge skill sticky. Only knowledge-category skills
// are affected — the action/hidden gates of Skill.IsSticky still apply on
// top. Must be called before LoadAll.
func (r *SkillRegistry) SetStickyOverrides(on, off []string) {
	if len(on) > 0 {
		r.stickyOn = make(map[string]bool, len(on))
		for _, n := range on {
			r.stickyOn[n] = true
		}
	}
	if len(off) > 0 {
		r.stickyOff = make(map[string]bool, len(off))
		for _, n := range off {
			r.stickyOff[n] = true
		}
	}
}

// applyStickyOverride records the pristine frontmatter sticky flag of a
// loaded skill (so toggles can decide which config list to edit) and then
// applies the config overrides to the in-memory Meta.Sticky.
func (r *SkillRegistry) applyStickyOverride(skill *Skill) {
	if skill == nil {
		return
	}
	if r.stickyFront == nil {
		r.stickyFront = make(map[string]bool)
	}
	r.stickyFront[skill.Meta.Name] = skill.Meta.Sticky
	switch {
	case r.stickyOff[skill.Meta.Name]:
		skill.Meta.Sticky = false
	case r.stickyOn[skill.Meta.Name]:
		skill.Meta.Sticky = true
	}
}

// FrontmatterSticky reports the pristine frontmatter sticky flag recorded at
// load time — BEFORE any skills.sticky / skills.sticky_off override was
// applied — plus whether the name is known. The sticky toggle uses it to
// write the minimal config entry: turning a frontmatter-sticky skill off
// writes sticky_off (not removing a sticky entry that never existed), and
// turning a plain skill on writes sticky.
func (r *SkillRegistry) FrontmatterSticky(name string) (bool, bool) {
	fm, ok := r.stickyFront[name]
	return fm, ok
}

// embeddedDefaultOff reports whether an embedded skill is suppressed by the
// default-off policy: it is in the default-disabled set AND the user has not
// explicitly opted it back in. Re-enable paths (either is sufficient):
//   - the embedded opt-in list (embeddedEnabled) — embedded-scoped, no
//     side-effects on file skills, or
//   - the global Enabled allowlist (explicitly naming the skill).
//
// Explicitly Disabled skills are already excluded by allowed() regardless.
func (r *SkillRegistry) embeddedDefaultOff(name string) bool {
	if !r.embeddedDefaultDisabled[name] {
		return false
	}
	if r.embeddedEnabled[name] {
		return false
	}
	// A global allowlist that explicitly names the skill also re-enables it.
	if len(r.enabled) > 0 && r.enabled[name] {
		return false
	}
	return true
}

// allowed reports whether a skill with the given name may be loaded:
// not disabled, and (no allowlist OR listed in the allowlist).
func (r *SkillRegistry) allowed(name string) bool {
	if r.disabled[name] {
		return false
	}
	if len(r.enabled) == 0 {
		return true
	}
	return r.enabled[name]
}

// SetHomeDir records the home directory path so skills loaded from it
// are labeled with source "home" instead of the generic "file".
func (r *SkillRegistry) SetHomeDir(dir string) {
	r.homeDir = dir
}

// SetProjectDir records the project directory path so skills loaded from it
// are labeled with source "project" instead of the generic "file".
func (r *SkillRegistry) SetProjectDir(dir string) {
	r.projectDir = dir
}

// SetTrustChecker installs the trust gate used for filesystem skills.
// Embedded skills are always trusted and never checked.
func (r *SkillRegistry) SetTrustChecker(tc TrustChecker) {
	r.trustChecker = tc
}

// LoadAll discovers and loads all skills from:
//  1. Embedded filesystem (if set via SetEmbeddedFS)
//  2. Filesystem directories (dirs)
//
// Later sources override earlier ones on name collision.
func (r *SkillRegistry) LoadAll() error {
	// Load embedded skills first (lowest priority)
	if r.embedFS != nil {
		if err := r.scanEmbeddedFS(); err != nil {
			return fmt.Errorf("scan embedded skills: %w", err)
		}
	}

	// Scan dirs in order (later wins on name collision)
	for _, dir := range r.dirs {
		if err := r.scanDir(dir, "file"); err != nil {
			continue
		}
	}

	return nil
}

func (r *SkillRegistry) isTrusted(name, filePath, source string) bool {
	if source == "embedded" {
		return true
	}
	if r.trustChecker == nil {
		return true
	}
	ok, err := r.trustChecker.IsTrusted(name, filePath)
	if err != nil {
		return false
	}
	return ok
}

// scanEmbeddedFS walks the embedded filesystem for */SKILL.md entries.
func (r *SkillRegistry) scanEmbeddedFS() error {
	err := fs.WalkDir(r.embedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		// path is <name>/SKILL.md
		name := filepath.Dir(path)
		if name == "." {
			return nil
		}
		if !r.allowed(name) || r.embeddedDefaultOff(name) {
			return nil
		}
		data, err := fs.ReadFile(r.embedFS, path)
		if err != nil {
			return nil
		}
		skill := parseSkill(name, string(data), "embedded", "skills/"+path)
		if skill != nil {
			r.applyStickyOverride(skill)
			r.skills[name] = skill
			r.scanEmbeddedSubSkills(name, path)
		}
		return nil
	})
	return err
}

// scanEmbeddedSubSkills loads sub-skills from a skills/ subdirectory inside
// the embedded parent skill directory.
func (r *SkillRegistry) scanEmbeddedSubSkills(parentName, parentPath string) {
	if r.embedFS == nil {
		return
	}
	subDir := filepath.Join(filepath.Dir(parentPath), "skills")
	entries, err := fs.ReadDir(r.embedFS, subDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(subDir, entry.Name(), "SKILL.md")
		data, err := fs.ReadFile(r.embedFS, skillPath)
		if err != nil {
			continue
		}
		if !r.allowed(entry.Name()) || r.embeddedDefaultOff(entry.Name()) {
			continue
		}
		skill := parseSkill(entry.Name(), string(data), "embedded", "skills/"+skillPath)
		if skill != nil {
			r.subSkills[parentName] = append(r.subSkills[parentName], skill)
		}
	}
}

func (r *SkillRegistry) scanDir(dir, source string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		name := entry.Name()
		if !r.allowed(name) {
			continue
		}
		if !r.isTrusted(name, skillPath, source) {
			continue
		}
		skill := parseSkill(name, string(data), source, skillPath)
		if skill != nil {
			r.applyStickyOverride(skill)
			r.skills[name] = skill
			r.scanSubSkills(dir, name, source)
		}
	}
	return nil
}

// scanSubSkills loads sub-skills from a skills/ subdirectory inside the
// parent skill directory. Sub-skills are hidden from the main agent and are
// only available to the sub-agent executing the parent skill.
func (r *SkillRegistry) scanSubSkills(dir, parentName, source string) {
	subDir := filepath.Join(dir, parentName, "skills")
	entries, err := os.ReadDir(subDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(subDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		name := entry.Name()
		if !r.allowed(name) {
			continue
		}
		if !r.isTrusted(name, skillPath, source) {
			continue
		}
		skill := parseSkill(name, string(data), source, skillPath)
		if skill != nil {
			r.subSkills[parentName] = append(r.subSkills[parentName], skill)
		}
	}
}

// Get returns a skill by name.
func (r *SkillRegistry) Get(name string) (*Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// SourceOf reports the origin of a skill by name, scanning candidate
// locations (embedded FS + configured dirs) WITHOUT the enabled/disabled gate
// so disabled skills are still found. It is used to route skill toggles to
// the right config layer (embedded → global/home, file → project). Returns
// ("embedded", true) for compiled-in skills, ("file", true) for directory
// skills, and ("", false) when the name matches no discoverable skill.
func (r *SkillRegistry) SourceOf(name string) (string, bool) {
	if r.embedFS != nil {
		path := filepath.Join(name, "SKILL.md")
		if _, err := fs.Stat(r.embedFS, path); err == nil {
			return "embedded", true
		}
	}
	for _, dir := range r.dirs {
		if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err == nil {
			return "file", true
		}
	}
	return "", false
}

// ListEmbeddedDiscoverable returns summaries of EVERY skill discoverable in
// the embedded filesystem, including default-off and explicitly-disabled ones
// that LoadAll skipped. The /config skill toggle uses it so a default-off
// embedded skill (e.g. review) still appears in the menu and can be re-enabled;
// the agent never sees it (Get/List exclude it until enabled).
func (r *SkillRegistry) ListEmbeddedDiscoverable() []SkillSummary {
	if r.embedFS == nil {
		return nil
	}
	var out []SkillSummary
	_ = fs.WalkDir(r.embedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		name := filepath.Dir(path)
		if name == "." || filepath.Dir(name) != "." {
			return nil // top-level skills only (sub-skills live under <parent>/skills/)
		}
		data, err := fs.ReadFile(r.embedFS, path)
		if err != nil {
			return nil
		}
		if skill := parseSkill(name, string(data), "embedded", "skills/"+path); skill != nil {
			out = append(out, SkillSummary{
				Name:        skill.Meta.Name,
				Description: skill.Meta.Description,
				Inline:      skill.Meta.Inline,
				Category:    categoryOrDefault(skill.Meta.Category),
				FilePath:    skill.FilePath,
				Source:      "embedded",
				// Invocation policy defaults are applied by parseSkill's
				// SkillMeta.UnmarshalYAML (both true when omitted).
				ModelInvocable: skill.Meta.ModelInvocable,
				UserInvocable:  skill.Meta.UserInvocable,
				Hidden:         skill.Meta.Hidden,
			})
		}
		return nil
	})
	return out
}

// EmbeddedDefaultDisabled reports whether the named skill is suppressed by the
// embedded default-off policy right now (in the default-off set AND not
// explicitly re-enabled). The config menu uses it to show the correct on/off
// state for discoverable-but-inactive embedded skills.
func (r *SkillRegistry) EmbeddedDefaultDisabled(name string) bool {
	return r.embeddedDefaultOff(name)
}

// IsEmbeddedDefaultOff reports whether the named skill is a MEMBER of the
// embedded default-off set (all embedded skills except telegram), regardless
// of whether the user has since re-enabled it. Unlike EmbeddedDefaultDisabled
// (the current suppressed state), this is stable across a toggle, so the
// disable path can tell a default-off skill (disabling = drop the opt-in)
// from a default-ON one (disabling = write an explicit Disabled entry).
func (r *SkillRegistry) IsEmbeddedDefaultOff(name string) bool {
	return r.embeddedDefaultDisabled[name]
}

// DefaultOnEmbeddedSkill is the single agent-facing embedded skill that stays
// ON by default; every other agent-facing embedded skill is OFF by default
const DefaultOnEmbeddedSkill = "telegram"

// DefaultEmbeddedOffNames returns the names of all embedded skills that are
// OFF by default: every agent-facing embedded skill except
// DefaultOnEmbeddedSkill (telegram). Two kinds are excluded and stay ON:
//   - telegram (the one kept agent-facing skill), and
//   - hidden/internal skills (e.g. dream): they are never listed to the agent
//     (Meta.Hidden) but internal features load them by name via Get, so
//     defaulting them off would break those features for zero prompt savings.
//
// Derived from the embedded FS so it never drifts as skills are added.
func DefaultEmbeddedOffNames(efs fs.FS) []string {
	var out []string
	if efs == nil {
		return nil
	}
	for _, n := range EmbeddedSkillNames(efs) {
		if n == DefaultOnEmbeddedSkill {
			continue
		}
		hidden := false
		if data, err := fs.ReadFile(efs, filepath.Join(n, "SKILL.md")); err == nil {
			if s := parseSkill(n, string(data), "embedded", ""); s != nil {
				hidden = s.Meta.Hidden
			}
		}
		if !hidden {
			out = append(out, n)
		}
	}
	return out
}

// EmbeddedSkillNames returns the names of every top-level skill discoverable
// in the embedded filesystem (sorted), regardless of enabled/disabled state.
// It lets the wiring layer derive the default-off set ("all embedded skills
// except telegram") without a hardcoded list that drifts as skills are added.
func EmbeddedSkillNames(efs fs.FS) []string {
	if efs == nil {
		return nil
	}
	var names []string
	_ = fs.WalkDir(efs, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		name := filepath.Dir(path)
		if name == "." || filepath.Dir(name) != "." {
			return nil // top-level skills only
		}
		names = append(names, name)
		return nil
	})
	sort.Strings(names)
	return names
}

// List returns summaries of all registered skills.
func (r *SkillRegistry) List() []SkillSummary {
	var summaries []SkillSummary
	for _, s := range r.skills {
		summaries = append(summaries, SkillSummary{
			Name:             s.Meta.Name,
			Description:      s.Meta.Description,
			Inline:           s.Meta.Inline,
			Category:         categoryOrDefault(s.Meta.Category),
			FilePath:         s.FilePath,
			RequiresSubAgent: r.hasAnySubSkill(s.Meta.Name),
			Sticky:           s.IsSticky(),
			Source:           s.Source,
			ModelInvocable:   s.Meta.ModelInvocable,
			UserInvocable:    s.Meta.UserInvocable,
			Hidden:           s.Meta.Hidden,
		})
	}
	return summaries
}

// hasAnySubSkill reports whether the named skill has sub-skills from either
// frontmatter or a skills/ subdirectory.
func (r *SkillRegistry) hasAnySubSkill(name string) bool {
	if s, ok := r.skills[name]; ok && len(s.Meta.SubSkills) > 0 {
		return true
	}
	return len(r.subSkills[name]) > 0
}

// parseSkill parses a SKILL.md file with YAML frontmatter.
// filePath is the location used in <available_skills> listings; pass an
// empty string only when the location is genuinely unknown.
func parseSkill(name, content, source, filePath string) *Skill {
	meta := SkillMeta{
		Name: name,
	}
	body := content

	// Extract YAML frontmatter between --- markers
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) == 3 {
			if err := yaml.Unmarshal([]byte(parts[1]), &meta); err == nil {
				body = strings.TrimSpace(parts[2])
			}
		}
	}

	// Skill bodies are model-facing (inline expansion, sub-agent prompts):
	// HTML comments must not consume LLM context.
	body = string(embeddoc.StripHTMLComments([]byte(body)))

	return &Skill{
		Meta:     meta,
		Body:     body,
		Source:   source,
		FilePath: filePath,
	}
}

// categoryOrDefault returns the category if non-empty, otherwise "action".
func categoryOrDefault(category string) string {
	if category == "" {
		return SkillCategoryAction
	}
	return category
}
