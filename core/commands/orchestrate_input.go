// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/config"
)

// OrchestrateInput is the parsed form of a /orchestrate colon command.
// It is produced by parseOrchestrateInput and consumed by the command handler.
type OrchestrateInput struct {
	Subcommand string
	Name       string // custom friendly name for new runs
	Topology   string // hub, fanout, pipeline
	Objective  string // run objective for new runs
	ID         string // run ID for resume/delete, agent target for steer
	Message    string // steering message
	Confirm    bool   // delete bypass flag
	Goal       string // deprecated/forward-compat goal binding
	Tab        string // tab key/index for the tab subcommand
}

// orchestrateKnownKeys maps each subcommand to the set of keys it accepts.
// The order matters: earlier keys take precedence when scanning a raw string.
var orchestrateKnownKeys = map[string][]string{
	"new":     {"topology", "name", "goal", "objective"},
	"resume":  {"id"},
	"delete":  {"id", "confirm"},
	"steer":   {"id", "message"},
	"list":    {},
	"tab":     {},
	"browser": {},
}

// parseOrchestrateInput parses the colon-separated arguments after the
// /orchestrate command name. The first argument is the subcommand; the rest
// are key=value pairs, either colon-separated or comma-separated.
//
// Examples:
//   ["new", "topology=hub,objective=Build auth"]
//   ["delete", "id=happy.hare", "confirm=true"]
//   ["steer", "id=coder-1,message=fix the bug"]
//
// Values may contain commas as long as the comma is not followed by another
// known key. The function returns an error for invalid syntax, not for
// missing required fields (those are handled by the command layer).
func parseOrchestrateInput(args []string) (OrchestrateInput, error) {
	var in OrchestrateInput
	if len(args) == 0 {
		return in, nil // bare /orchestrate → interactive menu
	}

	sub := strings.ToLower(args[0])
	if !isKnownSubcommand(sub) {
		return in, nil
	}
	in.Subcommand = sub

	// "tab" takes a single positional argument (a tab key or 1-based index),
	// not key=value pairs, so handle it before the generic kv parser.
	if sub == "tab" {
		in.Tab = strings.TrimSpace(strings.Join(args[1:], " "))
		return in, nil
	}

	keys := orchestrateKnownKeys[in.Subcommand]
	raw := strings.Join(args[1:], ",")
	pairs, err := splitKeyValuePairs(raw, keys)
	if err != nil {
		return in, err
	}
	return applyPairs(in, pairs)
}

func isKnownSubcommand(sub string) bool {
	switch sub {
	case "new", "resume", "delete", "steer", "list", "tab", "browser":
		return true
	}
	return false
}

func applyPairs(in OrchestrateInput, pairs []keyValuePair) (OrchestrateInput, error) {
	for _, p := range pairs {
		key := strings.ToLower(p.key)
		val := p.value
		switch key {
		case "topology":
			in.Topology = strings.ToLower(val)
		case "name":
			in.Name = strings.ToLower(val)
		case "objective":
			in.Objective = val
		case "goal":
			in.Goal = val
		case "id":
			in.ID = val
		case "message":
			in.Message = val
		case "confirm":
			in.Confirm = strings.EqualFold(val, "true") || val == "1"
		default:
			return in, fmt.Errorf("unknown argument %q for /orchestrate:%s", key, in.Subcommand)
		}
	}
	return in, nil
}

// keyValuePair is one parsed key=value fragment.
type keyValuePair struct {
	key   string
	value string
}

// splitKeyValuePairs parses a comma-separated string of key=value pairs. It
// tolerates commas inside values unless the comma is immediately followed by a
// known key and an equals sign.
func splitKeyValuePairs(raw string, keys []string) ([]keyValuePair, error) {
	if raw == "" {
		return nil, nil
	}
	known := make(map[string]bool, len(keys))
	for _, k := range keys {
		known[k] = true
	}
	known["objective"] = true // objective is always allowed to contain commas

	isKnownPrefix := func(s string) (string, bool) {
		key, _, hasEq := strings.Cut(s, "=")
		if !hasEq {
			return "", false
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if known[key] {
			return key, true
		}
		return "", false
	}

	parts := strings.Split(raw, ",")
	var pairs []keyValuePair
	for _, part := range parts {
		part = strings.TrimPrefix(part, "/") // tolerate leading slash if any
		key, ok := isKnownPrefix(part)
		if ok {
			val := strings.TrimSpace(part[len(key)+1:])
			pairs = append(pairs, keyValuePair{key: key, value: val})
			continue
		}
		if len(pairs) == 0 {
			return nil, fmt.Errorf("expected key=value, got %q", part)
		}
		last := &pairs[len(pairs)-1]
		last.value += "," + part
	}
	return pairs, nil
}

// normalizeTopology returns a valid topology or the default if the input is
// empty or invalid. It returns an error only for non-empty invalid values.
func normalizeTopology(topology string, cfg *config.Config) (string, error) {
	if topology == "" {
		return cfg.Orchestrator.Defaults.Topology, nil
	}
	low := strings.ToLower(topology)
	switch low {
	case config.OrchestratorTopologyHub, config.OrchestratorTopologyFanout, config.OrchestratorTopologyPipeline:
		return low, nil
	}
	return "", fmt.Errorf("unknown topology %q (use hub, fanout, or pipeline)", topology)
}
