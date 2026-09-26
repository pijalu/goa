// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// RepairLayerFile rewrites an invalid config layer file into one the loader
// accepts (bugs.md 2026-09-26: goa self-heals with user guidance — the UI
// asks for confirmation, this performs the confirmed repair):
//
//   - unparseable YAML → an empty mapping (every key falls back to defaults)
//   - a bare stall value ("60") → canonical "60s"
//   - a stall value nothing can parse ("abc") → the key is removed, so the
//     cascade default applies
//   - a contradictory stall pair → the warn key is removed
//
// The original bytes are backed up to <path>.bak-<timestamp> before any
// write. Returns the backup path, or "" when the file was already loadable
// and nothing was changed. An error means the file could not be made
// loadable — it is left untouched.
func (cl *CascadeLoader) RepairLayerFile(path string) (string, error) {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	root := parseConfigDocument(data)
	changed := false
	if root == nil {
		root = emptyMappingDocument()
		changed = true
	} else if repairStallNodes(root, path) {
		changed = true
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("marshal repaired %s: %w", path, err)
	}
	if desc, bad := pairViolationIn(out); bad {
		deleteYamlNode(root, []string{"execution", "activity_warn_after"})
		if out, err = yaml.Marshal(root); err != nil {
			return "", fmt.Errorf("marshal repaired %s: %w", path, err)
		}
		changed = true
		fmt.Fprintf(os.Stderr, "Warning: %s in %s — dropping the stale override so the file becomes loadable\n", desc, path)
	}
	if err := validateConfigBytes(out, filepath.Base(path)); err != nil {
		return "", fmt.Errorf("cannot auto-repair %s: %w — fix the file by hand (it has not been modified)", path, err)
	}
	if !changed {
		return "", nil
	}

	backup := path + ".bak-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(backup, data, 0o644); err != nil {
		return "", fmt.Errorf("back up %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		_ = os.Remove(backup) // do not leave a stray backup without the repair
		return "", fmt.Errorf("write repaired %s: %w", path, err)
	}
	return backup, nil
}

// parseConfigDocument parses data into a mapping document node, returning
// nil when the bytes are not a YAML mapping (the repair replaces the whole
// document in that case).
func parseConfigDocument(data []byte) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return doc.Content[0]
}

func emptyMappingDocument() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode}
}

// repairStallNodes fixes the stall-timing values in the document tree in
// place: bare integers are canonicalized, unparseable values are removed so
// the cascade default applies. Reports whether anything changed.
func repairStallNodes(root *yaml.Node, source string) bool {
	changed := false
	for _, key := range []string{"activity_timeout", "activity_warn_after"} {
		node := findYamlNode(root, []string{"execution", key})
		if node == nil || node.Kind != yaml.ScalarNode {
			continue
		}
		switch {
		case bareDurationPattern.MatchString(node.Value):
			fmt.Fprintf(os.Stderr, "Warning: execution.%s in %s has no time unit — corrected to %q\n", key, source, node.Value+"s")
			node.Value += "s"
			node.Tag = "!!str"
			changed = true
		case node.Value != "":
			if _, err := time.ParseDuration(node.Value); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: execution.%s in %s: %q is not a duration — removing the key so the default applies\n", key, source, node.Value)
				deleteYamlNode(root, []string{"execution", key})
				changed = true
			}
		}
	}
	return changed
}
