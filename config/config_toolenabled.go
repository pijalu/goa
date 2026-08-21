// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import "gopkg.in/yaml.v3"

type ToolEnabledConfig struct {
	BGExec        bool `yaml:"bg_exec"`
	DelegateTo    bool `yaml:"delegate_to"`
	Goal          bool `yaml:"goal"`
	Memento       bool `yaml:"memento"`
	Terminals     bool `yaml:"terminals"`
	RequestReview bool `yaml:"request_review"`
	SSHBash       bool `yaml:"ssh_bash"`
	WebFetch      bool `yaml:"webfetch"`
	// Verify controls the `verify` tool (run the test suite). Opt-OUT: defaults
	// to true (set in the embedded default config) so the model can run tests
	// unless the user explicitly disables it.
	Verify bool `yaml:"verify"`
	// PythonEnabled controls the embedded `python` gpython tool. Opt-OUT:
	// defaults to true so the model can run Python code unless the user
	// explicitly disables it.
	PythonEnabled bool `yaml:"python"`
	// RunCode controls the `run_code` code-mode dispatch tool (gap TL7): a
	// Python program that performs multiple tool sub-calls through the same
	// guarded permission/jail path as direct calls. Opt-OUT: defaults to true.
	RunCode bool `yaml:"run_code"`
	// Agent controls the `agent` sub-agent tool. Opt-OUT: defaults to true
	// (set in the embedded default config) so the model can spawn sub-agents
	// unless the user explicitly disables it.
	Agent bool `yaml:"agent"`
	// AgentSwarm controls the `agent_swarm` tool. Opt-OUT: defaults to true
	// (set in the embedded default config).
	AgentSwarm bool `yaml:"agent_swarm"`
	// Goa controls the `goa` slash-command tool. Opt-OUT: defaults to true
	// (set in the embedded default config).
	Goa bool `yaml:"goa"`
	// LSP controls the `lsp` code-navigation tool (gopls definition/references/
	// hover/symbols). Opt-OUT: defaults to true (set in the embedded default
	// config) so the model gets precise navigation unless the user disables it
	// or the project has no language server.
	LSP bool `yaml:"lsp"`
	// Todo controls the standalone `todo_list` tool ("todo should be
	// available outside of goal"). Opt-OUT: defaults to true (set in the
	// embedded default config) so the model can track work items without an
	// active goal; when a goal is active the tool is linked to the goal's own
	// todo list.
	Todo bool `yaml:"todo_list"`
	// ClarifyDisabled, when true, removes the ask_user_question tool from the
	// model's toolset. It is an inverted flag: the default (false/unset) leaves
	// the tool ENABLED by default, matching the requested behavior. All other
	// flags here are opt-IN; this one is opt-OUT.
	ClarifyDisabled bool `yaml:"clarify_disabled"`
	// set records which fields were explicitly present in YAML so deep merges
	// can override earlier layers only for those keys.
	set map[string]bool
}

// UnmarshalYAML implements yaml.Unmarshaler and records which keys were set.
func (t *ToolEnabledConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	type raw ToolEnabledConfig
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*t = ToolEnabledConfig(r)
	t.set = make(map[string]bool)
	for i := 0; i < len(node.Content); i += 2 {
		t.set[node.Content[i].Value] = true
	}
	return nil
}

// SetEnabled sets the named tool flag and marks it as explicitly configured.
func (t *ToolEnabledConfig) SetEnabled(name string, value bool) {
	if ptr := t.fieldPtr(name); ptr != nil {
		*ptr = value
	}
	if t.set == nil {
		t.set = make(map[string]bool)
	}
	t.set[name] = true
}

// GetEnabled returns the enabled flag for a known tool name, or false for
// unknown names.
func (t *ToolEnabledConfig) GetEnabled(name string) bool {
	if ptr := t.fieldPtr(name); ptr != nil {
		return *ptr
	}
	return false
}

// fieldPtr returns a pointer to the boolean field for the given tool name.
func (t *ToolEnabledConfig) fieldPtr(name string) *bool {
	fields := map[string]*bool{
		"bg_exec":          &t.BGExec,
		"delegate_to":      &t.DelegateTo,
		"goal":             &t.Goal,
		"memento":          &t.Memento,
		"terminals":        &t.Terminals,
		"request_review":   &t.RequestReview,
		"ssh_bash":         &t.SSHBash,
		"webfetch":         &t.WebFetch,
		"verify":           &t.Verify,
		"python":           &t.PythonEnabled,
		"run_code":         &t.RunCode,
		"agent":            &t.Agent,
		"agent_swarm":      &t.AgentSwarm,
		"goa":              &t.Goa,
		"lsp":              &t.LSP,
		"todo_list":        &t.Todo,
		"clarify_disabled": &t.ClarifyDisabled,
	}
	return fields[name]
}

// ApplyTo copies explicitly set flags from t into target.
func (t *ToolEnabledConfig) ApplyTo(target *ToolEnabledConfig) {
	if t.set == nil || target == nil {
		return
	}
	for name := range t.set {
		if srcPtr := t.fieldPtr(name); srcPtr != nil {
			if dstPtr := target.fieldPtr(name); dstPtr != nil {
				*dstPtr = *srcPtr
			}
		}
	}
	if target.set == nil {
		target.set = make(map[string]bool)
	}
	for k := range t.set {
		target.set[k] = true
	}
}

// EditConfig controls edit tool behavior.
