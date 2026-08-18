// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func (cl *CascadeLoader) SaveHomeField(path []string, value any) error {
	return cl.saveField(filepath.Join(cl.homeDir, ".goa"), "config.yaml", "home", path, value)
}
func (cl *CascadeLoader) SaveProjectField(path []string, value any) error {
	return cl.saveField(filepath.Join(cl.projectDir, ".goa"), "config.yaml", "project", path, value)
}
func (cl *CascadeLoader) saveField(dir, file, label string, path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create %s config dir: %w", label, err)
	}
	p := filepath.Join(dir, file)
	root, err := loadYAMLDocument(p, label)
	if err != nil {
		return err
	}
	if err = setYamlNode(root.Content[0], path, value); err != nil {
		return err
	}
	return writeYAMLDocument(p, label, root)
}
func loadYAMLDocument(path, label string) (*yaml.Node, error) {
	var root yaml.Node
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s config: %w", label, err)
		}
		root.Kind = yaml.DocumentNode
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	} else if err = yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("unmarshal %s config: %w", label, err)
	}
	if len(root.Content) == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	if root.Content[0].Kind != yaml.MappingNode {
		root.Content[0].Kind = yaml.MappingNode
	}
	return &root, nil
}
func writeYAMLDocument(path, label string, root *yaml.Node) error {
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal %s config: %w", label, err)
	}
	if err = os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write %s config: %w", label, err)
	}
	return nil
}
