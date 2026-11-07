// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// defaultSaveDocument returns the baseline the saver compares against: the
// embedded defaults run through the SAME normalization pipeline a real load
// applies (env interpolation, env/CLI overrides, legacy migrations, dangling
// reference sanitizers). Comparing a plain parse of default.yaml would treat
// derived values — mode.defaults.coder produced from a legacy execution.mode,
// for instance — as user choices and persist them.
//
// Remote provider registries are deliberately not fetched here: a config save
// must never depend on the network, and registry-provided providers are not
// shipped defaults.
func (cl *CascadeLoader) defaultSaveDocument() (*yaml.Node, error) {
	cfg, err := cl.loadDefaults()
	if err != nil {
		return nil, err
	}
	if err := cl.normalize(cfg); err != nil {
		return nil, err
	}
	cfg.FirstRun = false
	cfg.ConfigDir = ""
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal embedded defaults: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("reparse embedded defaults: %w", err)
	}
	return &doc, nil
}

// marshalConfigDelta renders cfg as the home-layer document: values equal to
// the shipped defaults are pruned (see pruneShippedDefaults), so the file
// holds only real user choices.
func (cl *CascadeLoader) marshalConfigDelta(cfg *Config) ([]byte, error) {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("reparse config for default pruning: %w", err)
	}
	if err := cl.pruneShippedDefaults(&doc); err != nil {
		return nil, fmt.Errorf("prune default values: %w", err)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("marshal config delta: %w", err)
	}
	return out, nil
}

// pruneShippedDefaults removes every node from the save document whose value
// equals the shipped default, so the home layer stores only what the user
// actually chose.
//
// Why: CascadeLoader.Save used to write the whole in-memory config, which
// froze every current value — including values the user never touched — as an
// explicit home pin. A shipped-default change then never reached an existing
// install (tools.enabled.mcp stayed false after the opt-out flip because a
// snapshot taken under the old opt-in default pinned it). Dropping
// default-equal nodes makes the file a delta: untouched settings keep
// following the shipped defaults, explicit choices keep winning.
//
// A document whose every value equals the defaults still writes an empty
// mapping: the file's existence is what ends first-run detection.
func (cl *CascadeLoader) pruneShippedDefaults(doc *yaml.Node) error {
	def, err := cl.defaultSaveDocument()
	if err != nil {
		return err
	}
	if doc == nil || len(doc.Content) == 0 || len(def.Content) == 0 {
		return nil
	}
	pruneDefaultsNode(doc.Content[0], def.Content[0])
	return nil
}

// pruneDefaultsNode deletes from node every child whose counterpart in def is
// deeply equal. Mapping children are compared recursively; sequence and scalar
// children as a whole.
func pruneDefaultsNode(node, def *yaml.Node) {
	if node == nil || def == nil {
		return
	}
	if node.Kind != yaml.MappingNode || def.Kind != yaml.MappingNode {
		return
	}
	kept := make([]*yaml.Node, 0, len(node.Content))
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		defVal := mappingValue(def, key.Value)
		if defVal != nil && val.Kind == yaml.MappingNode && defVal.Kind == yaml.MappingNode {
			pruneDefaultsNode(val, defVal)
			if len(val.Content) == 0 {
				continue // every entry was a default: the map itself is redundant
			}
			kept = append(kept, key, val)
			continue
		}
		if defVal != nil && yamlNodesEqual(val, defVal) {
			continue // value is the shipped default: not a user choice
		}
		kept = append(kept, key, val)
	}
	node.Content = kept
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// yamlNodesEqual reports deep equality, ignoring style and position (the two
// sides come from independent marshals of the same struct shape).
func yamlNodesEqual(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !sameNodeHeader(a, b) {
		return false
	}
	if a.Kind == yaml.MappingNode {
		return mappingNodesEqual(a, b)
	}
	return sequenceNodesEqual(a, b)
}

// sameNodeHeader compares the node's own identity — kind, tag, scalar value and
// child count — without looking at children.
func sameNodeHeader(a, b *yaml.Node) bool {
	return a.Kind == b.Kind &&
		a.Tag == b.Tag &&
		a.Value == b.Value &&
		len(a.Content) == len(b.Content)
}

// mappingNodesEqual compares two mappings by key: key order can differ between
// marshals, so positional comparison would report false differences.
func mappingNodesEqual(a, b *yaml.Node) bool {
	for i := 0; i+1 < len(a.Content); i += 2 {
		other := mappingValue(b, a.Content[i].Value)
		if other == nil {
			return false
		}
		if !yamlNodesEqual(a.Content[i+1], other) {
			return false
		}
	}
	return true
}

// sequenceNodesEqual compares two sequences positionally.
func sequenceNodesEqual(a, b *yaml.Node) bool {
	for i := range a.Content {
		if !yamlNodesEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}
