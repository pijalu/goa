// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// CleanupKind describes how a project item relates to the same item in the
// home layer. Conflicts are never removed without explicit confirmation.
type CleanupKind string

const (
	CleanupRedundant CleanupKind = "redundant"
	CleanupConflict  CleanupKind = "conflict"
)

// CleanupCandidate is one project setting or catalog entry that can be removed
// so the corresponding home value applies. Fields contains configuration field
// names only, never values; this makes conflict descriptions safe for credentials.
type CleanupCandidate struct {
	Key    string
	Name   string
	Type   string
	Kind   CleanupKind
	Fields []string

	target cleanupTarget
}

type cleanupTarget struct {
	field   string
	section string
	id      string
}

// CleanupPlan is an immutable comparison of the home and project YAML layers.
// It deliberately excludes embedded defaults, local files, environment and CLI
// overrides, and remote registries. Apply writes only the project layer.
type CleanupPlan struct {
	Candidates []CleanupCandidate

	homePath     string
	projectPath  string
	homeBytes    []byte
	projectBytes []byte
	projectRoot  *yaml.Node
}

// PlanProjectCleanup compares only ~/.goa/config.yaml and .goa/config.yaml.
// Active selections, same-ID provider/model entries, and other shared top-level
// sections are candidates. Entries that exist only in the project are ignored;
// a conflicting shared section is removed only after explicit confirmation.
func (cl *CascadeLoader) PlanProjectCleanup() (*CleanupPlan, error) {
	plan := &CleanupPlan{
		homePath:    cl.HomeConfigPath(),
		projectPath: filepath.Join(cl.projectDir, ".goa", "config.yaml"),
	}
	homeRoot, homeBytes, homeExists, err := readCleanupLayer(plan.homePath, "home")
	if err != nil {
		return nil, err
	}
	projectRoot, projectBytes, projectExists, err := readCleanupLayer(plan.projectPath, "project")
	if err != nil {
		return nil, err
	}
	plan.homeBytes = homeBytes
	plan.projectBytes = projectBytes
	plan.projectRoot = projectRoot
	if !homeExists || !projectExists {
		return plan, nil
	}

	homeMapping := homeRoot.Content[0]
	projectMapping := projectRoot.Content[0]
	for _, field := range []string{"active_provider", "active_model"} {
		if candidate, ok := cleanupSelectionCandidate(homeMapping, projectMapping, field); ok {
			plan.Candidates = append(plan.Candidates, candidate)
		}
	}
	providers, err := cleanupCatalogCandidates[ProviderConfig](homeMapping, projectMapping, "providers")
	if err != nil {
		return nil, err
	}
	plan.Candidates = append(plan.Candidates, providers...)
	models, err := cleanupCatalogCandidates[ModelConfig](homeMapping, projectMapping, "models")
	if err != nil {
		return nil, err
	}
	plan.Candidates = append(plan.Candidates, models...)
	plan.Candidates = append(plan.Candidates, cleanupSectionCandidates(homeMapping, projectMapping)...)
	return plan, nil
}

func readCleanupLayer(path, label string) (*yaml.Node, []byte, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("read %s configuration: %w", label, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, nil, false, fmt.Errorf("parse %s configuration: invalid YAML", label)
	}
	if len(root.Content) == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	if root.Content[0].Kind != yaml.MappingNode {
		return nil, nil, false, fmt.Errorf("parse %s configuration: top level must be a mapping", label)
	}
	// Check typed YAML shapes now, before the wizard offers any changes. Do not
	// include parser text in the error: YAML type errors can quote source values.
	var layer Config
	if err := yaml.Unmarshal(data, &layer); err != nil {
		return nil, nil, false, fmt.Errorf("parse %s configuration: invalid typed values", label)
	}
	return &root, data, true, nil
}

func cleanupSectionCandidates(home, project *yaml.Node) []CleanupCandidate {
	known := cleanupTopLevelFields()
	var candidates []CleanupCandidate
	for i := 0; i < len(project.Content); i += 2 {
		name := project.Content[i].Value
		if !known[name] || cleanupSpecialTopLevelField(name) {
			continue
		}
		homeNode := findYamlNode(home, []string{name})
		if homeNode == nil {
			continue
		}
		projectNode := project.Content[i+1]
		kind := CleanupConflict
		if cleanupNodesEqual(homeNode, projectNode) {
			kind = CleanupRedundant
		}
		candidates = append(candidates, CleanupCandidate{
			Key: "section:" + name, Name: name, Type: "section", Kind: kind,
			Fields: cleanupDifferentMappingFields(homeNode, projectNode),
			target: cleanupTarget{field: name},
		})
	}
	return candidates
}

func cleanupTopLevelFields() map[string]bool {
	fields := make(map[string]bool)
	typeOfConfig := reflect.TypeOf(Config{})
	for i := 0; i < typeOfConfig.NumField(); i++ {
		name := strings.Split(typeOfConfig.Field(i).Tag.Get("yaml"), ",")[0]
		if name != "" && name != "-" {
			fields[name] = true
		}
	}
	return fields
}

func cleanupSpecialTopLevelField(name string) bool {
	return name == "active_provider" || name == "active_model" || name == "providers" || name == "models"
}

func cleanupNodesEqual(home, project *yaml.Node) bool {
	var homeValue, projectValue any
	if home.Decode(&homeValue) != nil || project.Decode(&projectValue) != nil {
		return false
	}
	return reflect.DeepEqual(homeValue, projectValue)
}

func cleanupDifferentMappingFields(home, project *yaml.Node) []string {
	if home.Kind != yaml.MappingNode || project.Kind != yaml.MappingNode {
		return nil
	}
	var fields []string
	for i := 0; i < len(project.Content); i += 2 {
		name := project.Content[i].Value
		homeNode := findYamlNode(home, []string{name})
		if homeNode != nil && !cleanupNodesEqual(homeNode, project.Content[i+1]) {
			fields = append(fields, name)
		}
	}
	return fields
}

func cleanupSelectionCandidate(home, project *yaml.Node, field string) (CleanupCandidate, bool) {
	homeNode := findYamlNode(home, []string{field})
	projectNode := findYamlNode(project, []string{field})
	if homeNode == nil || projectNode == nil || homeNode.Value == "" || projectNode.Value == "" {
		return CleanupCandidate{}, false
	}
	kind := CleanupConflict
	if homeNode.Value == projectNode.Value {
		kind = CleanupRedundant
	}
	return CleanupCandidate{
		Key: field, Name: field, Type: "setting", Kind: kind,
		target: cleanupTarget{field: field},
	}, true
}

type cleanupCatalogEntry[T any] struct {
	id    string
	value T
}

func cleanupCatalogCandidates[T any](home, project *yaml.Node, section string) ([]CleanupCandidate, error) {
	homeEntries, err := decodeCleanupCatalog[T](home, section)
	if err != nil {
		return nil, err
	}
	projectEntries, err := decodeCleanupCatalog[T](project, section)
	if err != nil {
		return nil, err
	}
	homeByID := make(map[string]T, len(homeEntries))
	for _, entry := range homeEntries {
		homeByID[entry.id] = entry.value
	}
	projectByID := make(map[string]T, len(projectEntries))
	for _, entry := range projectEntries {
		projectByID[entry.id] = entry.value
	}

	itemType := strings.TrimSuffix(section, "s")
	seen := make(map[string]bool, len(projectEntries))
	var candidates []CleanupCandidate
	for _, projectEntry := range projectEntries {
		if seen[projectEntry.id] {
			continue
		}
		seen[projectEntry.id] = true
		homeEntry, inherited := homeByID[projectEntry.id]
		effectiveProjectEntry := projectByID[projectEntry.id]
		if !inherited {
			continue
		}
		kind := CleanupConflict
		if reflect.DeepEqual(homeEntry, effectiveProjectEntry) {
			kind = CleanupRedundant
		}
		candidates = append(candidates, CleanupCandidate{
			Key: itemType + ":" + projectEntry.id, Name: projectEntry.id, Type: itemType,
			Kind: kind, Fields: cleanupDifferentFields(homeEntry, effectiveProjectEntry),
			target: cleanupTarget{section: section, id: projectEntry.id},
		})
	}
	return candidates, nil
}

func decodeCleanupCatalog[T any](root *yaml.Node, section string) ([]cleanupCatalogEntry[T], error) {
	sequence := findYamlNode(root, []string{section})
	if sequence == nil {
		return nil, nil
	}
	if sequence.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("parse configuration: %s must be a sequence", section)
	}
	entries := make([]cleanupCatalogEntry[T], 0, len(sequence.Content))
	for _, node := range sequence.Content {
		var value T
		if err := node.Decode(&value); err != nil {
			return nil, fmt.Errorf("parse configuration: invalid %s entry", section)
		}
		idNode := findYamlNode(node, []string{"id"})
		if idNode == nil || idNode.Value == "" {
			continue
		}
		entries = append(entries, cleanupCatalogEntry[T]{id: idNode.Value, value: value})
	}
	return entries, nil
}

func cleanupDifferentFields(home, project any) []string {
	homeValue := reflect.ValueOf(home)
	projectValue := reflect.ValueOf(project)
	typeOfValue := homeValue.Type()
	var fields []string
	for i := 0; i < homeValue.NumField(); i++ {
		field := typeOfValue.Field(i)
		name := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if name == "-" || reflect.DeepEqual(homeValue.Field(i).Interface(), projectValue.Field(i).Interface()) {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields = append(fields, name)
	}
	return fields
}

// Apply removes only approved candidate keys, in one project-file write. Empty
// approval is a byte-for-byte no-op. If either layer changed after planning,
// Apply refuses to overwrite newer data and asks the caller to run cleanup again.
func (p *CleanupPlan) Apply(approved []string) (int, error) {
	if len(approved) == 0 {
		return 0, nil
	}
	byKey := make(map[string]CleanupCandidate, len(p.Candidates))
	for _, candidate := range p.Candidates {
		byKey[candidate.Key] = candidate
	}
	selected := make(map[string]bool, len(approved))
	for _, key := range approved {
		if _, ok := byKey[key]; !ok {
			return 0, fmt.Errorf("unknown cleanup candidate %q", key)
		}
		selected[key] = true
	}
	if err := p.ensureUnchanged(); err != nil {
		return 0, err
	}
	for _, candidate := range p.Candidates {
		if selected[candidate.Key] {
			removeCleanupCandidate(p.projectRoot.Content[0], candidate.target)
		}
	}
	if err := writeYAMLDocument(p.projectPath, "project", p.projectRoot, nil); err != nil {
		return 0, fmt.Errorf("write cleaned project configuration: %w", err)
	}
	return len(selected), nil
}

func (p *CleanupPlan) ensureUnchanged() error {
	homeChanged, err := cleanupFileChanged(p.homePath, p.homeBytes)
	if err != nil {
		return fmt.Errorf("read home configuration before cleanup: %w", err)
	}
	projectChanged, err := cleanupFileChanged(p.projectPath, p.projectBytes)
	if err != nil {
		return fmt.Errorf("read project configuration before cleanup: %w", err)
	}
	if homeChanged || projectChanged {
		return fmt.Errorf("configuration changed during cleanup; rerun goa cleanup")
	}
	return nil
}

func cleanupFileChanged(path string, expected []byte) (bool, error) {
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return len(expected) != 0, nil
	}
	if err != nil {
		return false, err
	}
	return !bytes.Equal(current, expected), nil
}

func deleteCleanupTopLevelField(root *yaml.Node, field string) {
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != field {
			continue
		}
		leadingComment := cleanupLeadingComment(root, i)
		root.Content = append(root.Content[:i], root.Content[i+2:]...)
		restoreCleanupLeadingComment(root, leadingComment)
		return
	}
}

func cleanupLeadingComment(root *yaml.Node, index int) string {
	if index != 0 {
		return ""
	}
	if comment := root.Content[index].HeadComment; comment != "" {
		return comment
	}
	return root.Content[index+1].HeadComment
}

func restoreCleanupLeadingComment(root *yaml.Node, comment string) {
	if comment == "" {
		return
	}
	if len(root.Content) == 0 {
		root.HeadComment = comment
		return
	}
	if existing := root.Content[0].HeadComment; existing != "" && existing != comment {
		comment += "\n" + existing
	}
	root.Content[0].HeadComment = comment
}

func removeCleanupCandidate(root *yaml.Node, target cleanupTarget) {
	if target.field != "" {
		deleteCleanupTopLevelField(root, target.field)
		return
	}
	sequence := findYamlNode(root, []string{target.section})
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return
	}
	kept := sequence.Content[:0]
	for _, entry := range sequence.Content {
		id := findYamlNode(entry, []string{"id"})
		if id != nil && id.Value == target.id {
			continue
		}
		kept = append(kept, entry)
	}
	sequence.Content = kept
	if len(kept) == 0 {
		deleteCleanupTopLevelField(root, target.section)
	}
}
