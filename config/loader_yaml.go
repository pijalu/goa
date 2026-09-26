// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func deleteYamlNode(node *yaml.Node, path []string) {
	if node.Kind != yaml.MappingNode || len(path) == 0 {
		return
	}
	key := path[0]
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		if len(path) == 1 {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
		deleteYamlNode(node.Content[i+1], path[1:])
		if len(node.Content[i+1].Content) == 0 {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
		}
		return
	}
}
func setYamlNode(node *yaml.Node, path []string, value interface{}) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node at %q", strings.Join(path, "."))
	}
	key := path[0]
	var child *yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			child = node.Content[i+1]
			break
		}
	}
	if child == nil {
		child = &yaml.Node{Kind: yaml.MappingNode}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	}
	if len(path) == 1 {
		child.Kind = yaml.ScalarNode
		child.Tag = ""
		child.Value = fmt.Sprintf("%v", value)
		return nil
	}
	if child.Kind != yaml.MappingNode {
		child.Kind = yaml.MappingNode
	}
	return setYamlNode(child, path[1:], value)
}
func setYamlNodeValue(node *yaml.Node, path []string, value any) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node at %q", strings.Join(path, "."))
	}
	key := path[0]
	idx := -1
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			idx = i + 1
			break
		}
	}
	if len(path) == 1 {
		encoded, err := valueToYAMLNode(value)
		if err != nil {
			return err
		}
		if idx >= 0 {
			node.Content[idx] = encoded
		} else {
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, encoded)
		}
		return nil
	}
	var child *yaml.Node
	if idx >= 0 {
		child = node.Content[idx]
		if child.Kind != yaml.MappingNode {
			child.Kind = yaml.MappingNode
			child.Content = nil
		}
	} else {
		child = &yaml.Node{Kind: yaml.MappingNode}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	}
	return setYamlNodeValue(child, path[1:], value)
}
func valueToYAMLNode(value any) (*yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("reparse value: %w", err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	}
	return doc.Content[0], nil
}

// checkActivityPairLayer validates the stall-timing pair WITHIN a single cascade
// layer: when one file/config source sets BOTH execution.activity_timeout and
// execution.activity_warn_after, the warning lead must be shorter than the retry
// window — a contradictory explicit pair in one source is a configuration
// mistake, and reporting it at load time (naming the file) is far cheaper than
// silently deriving a different lead at runtime.
//
// The check is deliberately layer-scoped, NOT applied to the merged Config:
// the two keys cascade independently, so a home pin of activity_timeout: 30s
// combined with the shipped activity_warn_after: 30s is a perfectly valid
// install and must not refuse to start (rejecting the merged pair was observed
// to break startup against a real ~/.goa/config.yaml). Cross-layer combinations
// are therefore accepted and resolved at use: Agent.effectiveStallWarnAfter
// derives two thirds of the effective window whenever the configured lead is
// unset, at, or beyond it.
func checkActivityPairLayer(exec ExecutionConfig, source string) error {
	if exec.ActivityTimeout == "" || exec.ActivityWarnAfter == "" {
		return nil
	}
	timeout, err := time.ParseDuration(exec.ActivityTimeout)
	if err != nil {
		return nil // shape errors are reported by Config.Validate with its own wording
	}
	warn, err := time.ParseDuration(exec.ActivityWarnAfter)
	if err != nil {
		return nil
	}
	if warn >= timeout {
		return fmt.Errorf("execution.activity_warn_after (%s) must be shorter than execution.activity_timeout (%s) in %s",
			exec.ActivityWarnAfter, exec.ActivityTimeout, source)
	}
	return nil
}
