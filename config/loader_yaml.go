// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
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

// Heal records one value the loader corrected in memory while reading a config
// layer: what was written, why it could not be used, and what goa used instead.
// The UI renders it verbatim (Describe) and uses Source as the file an accepted
// repair must rewrite.
type Heal struct {
	// Source is the config file carrying the offending value.
	Source string
	// Key is the dotted config key that was corrected.
	Key string
	// Bad is the value as written in the file.
	Bad string
	// Fixed is the value goa loaded instead. Empty when the key was dropped so
	// the cascade default (or the runtime-derived value) applies.
	Fixed string
	// Reason explains in one clause why Bad could not be used.
	Reason string
}

// Describe renders a heal for the user: the error, the correction and where.
func (h Heal) Describe() string {
	fix := "loaded as " + quoteHealValue(h.Fixed)
	if h.Fixed == "" {
		fix = "override dropped, goa derives the value automatically"
	}
	return fmt.Sprintf("%s in %s: %s is invalid — %s; %s", h.Key, h.Source, quoteHealValue(h.Bad), h.Reason, fix)
}

func quoteHealValue(v string) string {
	if v == "" {
		return "(empty)"
	}
	return strconv.Quote(v)
}

// sanitizeBareStallDurations corrects stall-timing values written without a
// time unit ("60" instead of "60s"). The /config UI deliberately speaks
// plain seconds (bugs.md BUG-5), so hand-edits copy that style into the
// config file — where units are required — and the next start died with
// "missing unit in duration". Bare integers have exactly one sane reading
// (seconds), so heal instead of refusing: correct the value in memory with a
// visible stderr warning naming the file and the correction, and return a
// Heal record so the UI can name the key, the error and the correction.
// Values that are not bare integers ("60s", "2m", "abc") pass through
// untouched; "abc"-style garbage keeps belonging to Config.Validate.
//
// Layer-scoped like the pair heal: applied per cascade layer before the
// merge, so cross-layer combinations stay coherent and the pair check sees
// the corrected values.
func sanitizeBareStallDurations(exec *ExecutionConfig, source string) []Heal {
	var heals []Heal
	for _, key := range stallTimingKeys {
		value := execStallValue(exec, key)
		if !bareDurationPattern.MatchString(value) {
			continue
		}
		fixed := value + "s"
		setExecStallValue(exec, key, fixed)
		heals = append(heals, Heal{
			Source: source,
			Key:    "execution." + key,
			Bad:    value,
			Fixed:  fixed,
			Reason: "durations in config files need a time unit",
		})
		warnBareStallDuration(source, "execution."+key, fixed)
	}
	return heals
}

// stallTimingKeys are the two stall-timing config keys that accept durations.
var stallTimingKeys = []string{"activity_timeout", "activity_warn_after"}

// execStallValue / setExecStallValue address the stall keys by name so the
// bare-value heal walks one list instead of repeating the same block twice.
func execStallValue(exec *ExecutionConfig, key string) string {
	if key == "activity_warn_after" {
		return exec.ActivityWarnAfter
	}
	return exec.ActivityTimeout
}

func setExecStallValue(exec *ExecutionConfig, key, value string) {
	if key == "activity_warn_after" {
		exec.ActivityWarnAfter = value
		return
	}
	exec.ActivityTimeout = value
}

func warnBareStallDuration(source, key, corrected string) {
	fmt.Fprintf(os.Stderr, "Warning: %s in %s has no time unit — corrected to %q (config files require durations like %q; the /config UI accepts plain seconds)\n", key, source, corrected, corrected)
}

// sanitizeActivityPairLayer corrects a contradictory stall-timing pair WITHIN
// a single cascade layer: when one file/config source sets BOTH
// execution.activity_timeout and execution.activity_warn_after and the warning
// lead is NOT shorter than the event stall the agent retries on, the layer's
// warn override is dropped — with a visible stderr warning naming the file — so
// startup proceeds and the stall warning falls back to the cascade default or
// the runtime's derived two-thirds lead. Rejecting the pair outright orphaned
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
// its own wording. Returns one Heal record per corrected key, nil when the pair
// is already coherent.
func sanitizeActivityPairLayer(exec *ExecutionConfig, source string) []Heal {
	desc, bad := ActivityPairViolation(*exec)
	if !bad {
		return nil
	}
	dropped := exec.ActivityWarnAfter
	exec.ActivityWarnAfter = ""
	fmt.Fprintf(os.Stderr, "Warning: %s in %s — dropping the override so the stall warning leads the auto-retry (derived 2/3 of the window)\n", desc, source)
	// Fixed stays empty: the override is dropped, not rewritten.
	return []Heal{{
		Source: source,
		Key:    "execution.activity_warn_after",
		Bad:    dropped,
		Reason: desc,
	}}
}

// ActivityPairViolation reports whether a config's stall-timing pair is
// contradictory — BOTH keys set, both parseable, and the warning lead at or
// beyond the event stall derived from the retry window — returning a
// human-readable description when it is. Empty or unparseable values are NOT a
// violation: shape errors belong to Config.Validate, and half-set pairs are
// legitimate (the keys cascade independently; the runtime derives the lead at
// use). Shared by the load-time heal (sanitizeActivityPairLayer) and the
// write-path guard (validateConfigBytes) so both sides agree on what
// "contradictory" means.
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
	// Compare against the EVENT stall (three quarters of activity_timeout),
	// not the raw byte budget: that is the deadline the agent actually retries
	// on, so a lead between the two could never fire. EventStallTimeout is the
	// single source of that split — the agent calls the same function.
	stall := agenticprovider.EventStallTimeout(timeout)
	if warn < stall {
		return "", false
	}
	return fmt.Sprintf("execution.activity_warn_after (%s) is not shorter than the %s stall window of execution.activity_timeout (%s)", warn, stall, timeout), true
}
