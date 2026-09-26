// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func (cl *CascadeLoader) SaveProjectFieldValue(path []string, value any) error {
	return cl.editProjectConfig(path, func(doc *yaml.Node) error { return setYamlNodeValue(doc, path, value) })
}
func (cl *CascadeLoader) DeleteProjectField(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	return cl.editProjectConfig(path, func(doc *yaml.Node) error { deleteYamlNode(doc, path); return nil })
}
func (cl *CascadeLoader) SaveHomeFieldValue(path []string, value any) error {
	return cl.editHomeConfig(path, func(doc *yaml.Node) error { return setYamlNodeValue(doc, path, value) })
}
func (cl *CascadeLoader) SaveLocalFieldValue(path []string, value any) error {
	return cl.editLocalConfig(path, func(doc *yaml.Node) error { return setYamlNodeValue(doc, path, value) })
}
func (cl *CascadeLoader) DeleteHomeField(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	return cl.editHomeConfig(path, func(doc *yaml.Node) error { deleteYamlNode(doc, path); return nil })
}
func (cl *CascadeLoader) editHomeConfig(editedPath []string, edit func(*yaml.Node) error) error {
	return cl.editConfigFile(filepath.Join(cl.homeDir, ".goa"), "config.yaml", "home", editedPath, edit)
}
func (cl *CascadeLoader) editProjectConfig(editedPath []string, edit func(*yaml.Node) error) error {
	return cl.editConfigFile(filepath.Join(cl.projectDir, ".goa"), "config.yaml", "project", editedPath, edit)
}
func (cl *CascadeLoader) editLocalConfig(editedPath []string, edit func(*yaml.Node) error) error {
	return cl.editConfigFile(filepath.Join(cl.projectDir, ".goa"), "config.local.yaml", "local", editedPath, edit)
}
func (cl *CascadeLoader) editConfigFile(dir, file, label string, editedPath []string, edit func(*yaml.Node) error) error {
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
	if err = edit(root.Content[0]); err != nil {
		return err
	}
	return writeYAMLDocument(p, label, root, editedPath)
}
