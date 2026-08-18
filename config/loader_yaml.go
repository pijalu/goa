// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"strings"
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
