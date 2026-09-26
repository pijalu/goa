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
	return writeYAMLDocument(p, label, root, path)
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

// writeYAMLDocument marshals root and lands it on disk — but never an
// invalid document (bugs.md: goa never writes a config it cannot load). The
// bytes are validated BEFORE the write. A document carrying the contradictory
// stall pair is handled according to the edit: stall-key edits are REFUSED
// (an explicitly typed timing choice is never second-guessed silently), while
// any other edit HEALS the pair exactly like the loader does at startup — the
// stale lead is dropped with a warning — so one hand-edited mistake cannot
// wedge every future settings change. Bare stall values without a unit are
// canonicalized first (same heal the loader applies at startup), and the
// final bytes must pass the duration-shape checks too — a raw "60" from a
// caller that skipped parsing must never reach the disk (bugs.md
// 2026-09-26). editedPath is the field being written (nil for edits that are
// not field-shaped).
func writeYAMLDocument(path, label string, root *yaml.Node, editedPath []string) error {
	healBareStallNodes(root.Content[0], label)
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal %s config: %w", label, err)
	}
	if desc, bad := pairViolationIn(out); bad {
		if isStallFieldPath(editedPath) {
			return fmt.Errorf("written %s config is invalid: %s", label, desc)
		}
		deleteYamlNode(root.Content[0], []string{"execution", "activity_warn_after"})
		if out, err = yaml.Marshal(root); err != nil {
			return fmt.Errorf("marshal healed %s config: %w", label, err)
		}
		fmt.Fprintf(os.Stderr, "Warning: %s in %s — dropping the stale override so the file stays loadable\n", desc, label)
	}
	if err := validateConfigBytes(out, label); err != nil {
		return fmt.Errorf("refusing to write %s config: %w", label, err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write %s config: %w", label, err)
	}
	return nil
}

// healBareStallNodes canonicalizes stall-timing scalars written without a
// time unit ("60" → "60s") directly in the YAML tree, mirroring the
// loader's sanitizeBareStallDurations so an unrelated settings write into a
// hand-edited file fixes it instead of persisting the poison (or refusing
// and wedging every future write).
func healBareStallNodes(root *yaml.Node, label string) {
	for _, key := range []string{"activity_timeout", "activity_warn_after"} {
		path := []string{"execution", key}
		node := findYamlNode(root, path)
		if node == nil || node.Kind != yaml.ScalarNode || !bareDurationPattern.MatchString(node.Value) {
			continue
		}
		fmt.Fprintf(os.Stderr, "Warning: execution.%s in %s has no time unit — corrected to %q so the file stays loadable\n", key, label, node.Value+"s")
		node.Value += "s"
		node.Tag = "!!str"
	}
}

// findYamlNode returns the node at path, or nil when any segment is missing.
func findYamlNode(node *yaml.Node, path []string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode || len(path) == 0 {
		return nil
	}
	key := path[0]
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		if len(path) == 1 {
			return node.Content[i+1]
		}
		return findYamlNode(node.Content[i+1], path[1:])
	}
	return nil
}

// pairViolationIn reports the contradictory-stall-pair description carried by
// marshaled config bytes, if any. Parse errors are not reported here — they
// are validateConfigBytes' job.
func pairViolationIn(out []byte) (string, bool) {
	layer := &Config{}
	if err := yaml.Unmarshal(out, layer); err != nil {
		return "", false
	}
	return ActivityPairViolation(layer.Execution)
}

// isStallFieldPath reports whether the edited field is one of the stall
// timing keys, whose writes carry the user's explicit timing choice.
func isStallFieldPath(path []string) bool {
	if len(path) != 2 || path[0] != "execution" {
		return false
	}
	return path[1] == "activity_timeout" || path[1] == "activity_warn_after"
}

// existingConfigBytes snapshots the current file for rollback.
func existingConfigBytes(path string) (prev []byte, hadPrev bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

// rollbackConfigFile restores the previous content, removing a file that did
// not exist before the failed write.
func rollbackConfigFile(path string, prev []byte, hadPrev bool) {
	if !hadPrev {
		_ = os.Remove(path)
		return
	}
	_ = os.WriteFile(path, prev, 0644)
}

// validateConfigBytes is the writer-side backstop of "goa never writes a
// config it cannot load" (bugs.md): the marshaled bytes must parse back into
// a Config, carry no layer-level stall-pair contradiction, AND pass the
// duration-shape checks the loader enforces (a bare "60" must never reach
// the disk — bugs.md 2026-09-26). Layer-scoped like the heals: cross-layer
// combinations remain legal.
func validateConfigBytes(out []byte, label string) error {
	layer := &Config{}
	if err := yaml.Unmarshal(out, layer); err != nil {
		return fmt.Errorf("written %s config does not parse: %w", label, err)
	}
	if desc, bad := ActivityPairViolation(layer.Execution); bad {
		return fmt.Errorf("written %s config is invalid: %s", label, desc)
	}
	if err := durationShapeError(layer); err != nil {
		return fmt.Errorf("written %s config is invalid: %v", label, err)
	}
	return nil
}
