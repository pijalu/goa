// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"fmt"
	"os"
	"regexp"
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

// bareDurationPattern matches a bare non-negative integer ("60"): plain
// seconds exactly as the /config UI speaks them. yaml.v3 decodes both the
// unquoted `60` and the quoted "60" into the same Go string, so one pattern
// covers both spellings.
var bareDurationPattern = regexp.MustCompile(`^[0-9]+$`)

// sanitizeBareStallDurations corrects stall-timing values written without a
// time unit ("60" instead of "60s"). The /config UI deliberately speaks
// plain seconds (bugs.md BUG-5), so hand-edits copy that style into the
// config file — where units are required — and the next start died with
// "missing unit in duration". Bare integers have exactly one sane reading
// (seconds), so heal instead of refusing: correct the value in memory with a
// visible stderr warning naming the file and the correction. Values that are
// not bare integers ("60s", "2m", "abc") pass through untouched; "abc"-style
// garbage keeps belonging to Config.Validate.
//
// Layer-scoped like the pair heal: applied per cascade layer before the
// merge, so cross-layer combinations stay coherent and the pair check sees
// the corrected values.
func sanitizeBareStallDurations(exec *ExecutionConfig, source string) bool {
	healed := false
	if bareDurationPattern.MatchString(exec.ActivityTimeout) {
		exec.ActivityTimeout += "s"
		warnBareStallDuration(source, "execution.activity_timeout", exec.ActivityTimeout)
		healed = true
	}
	if bareDurationPattern.MatchString(exec.ActivityWarnAfter) {
		exec.ActivityWarnAfter += "s"
		warnBareStallDuration(source, "execution.activity_warn_after", exec.ActivityWarnAfter)
		healed = true
	}
	return healed
}

func warnBareStallDuration(source, key, corrected string) {
	fmt.Fprintf(os.Stderr, "Warning: %s in %s has no time unit — corrected to %q (config files require durations like %q; the /config UI accepts plain seconds)\n", key, source, corrected, corrected)
}

// sanitizeActivityPairLayer corrects a contradictory stall-timing pair WITHIN
// a single cascade layer: when one file/config source sets BOTH
// execution.activity_timeout and execution.activity_warn_after and the warning
// lead is NOT shorter than the retry window, the layer's warn override is
// dropped — with a visible stderr warning naming the file — so startup
// proceeds and the stall warning falls back to the cascade default or the
// runtime's derived two-thirds lead. Rejecting the pair outright orphaned
// existing installs the moment the check landed (observed: a real
// ~/.goa/config.yaml with the 45s/45s pair refused to start, and the fatal
// message never even reached the terminal).
//
// The heal is deliberately layer-scoped, NOT applied to the merged Config:
// the two keys cascade independently, so a home pin of activity_timeout: 30s
// combined with the shipped activity_warn_after: 30s is a perfectly valid
// install and must not be rewritten (cross-layer combinations are accepted and
// resolved at use: Agent.effectiveStallWarnAfter derives two thirds of the
// effective window whenever the configured lead is unset, at, or beyond it).
// Invalid duration shapes are ignored here — Config.Validate reports them with
// its own wording. Returns true when a correction was applied.
func sanitizeActivityPairLayer(exec *ExecutionConfig, source string) bool {
	desc, bad := ActivityPairViolation(*exec)
	if !bad {
		return false
	}
	exec.ActivityWarnAfter = ""
	fmt.Fprintf(os.Stderr, "Warning: %s in %s — dropping the override so the stall warning leads the auto-retry (derived 2/3 of the window)\n", desc, source)
	return true
}

// ActivityPairViolation reports whether a config's stall-timing pair is
// contradictory — BOTH keys set, both parseable, and the warning lead at or
// beyond the retry window — returning a human-readable description when it
// is. Empty or unparseable values are NOT a violation: shape errors belong to
// Config.Validate, and half-set pairs are legitimate (the keys cascade
// independently; the runtime derives the lead at use). Shared by the
// load-time heal (sanitizeActivityPairLayer) and the write-path guard
// (validateConfigBytes) so both sides agree on what "contradictory" means.
func ActivityPairViolation(exec ExecutionConfig) (string, bool) {
	if exec.ActivityTimeout == "" || exec.ActivityWarnAfter == "" {
		return "", false
	}
	timeout, err := time.ParseDuration(exec.ActivityTimeout)
	if err != nil {
		return "", false
	}
	warn, err := time.ParseDuration(exec.ActivityWarnAfter)
	if err != nil {
		return "", false
	}
	if warn < timeout {
		return "", false
	}
	return fmt.Sprintf("execution.activity_warn_after (%s) is not shorter than execution.activity_timeout (%s)", warn, timeout), true
}
