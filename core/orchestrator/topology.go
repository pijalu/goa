// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"errors"
	"fmt"
	"strings"
)

// Topology selects how agents are driven within a run.
type Topology string

const (
	// TopologyHub routes work through a single orchestrator agent that
	// delegates to managed agents via a DelegateTool.
	TopologyHub Topology = "hub"
	// TopologyFanout runs all configured agents in parallel against the
	// objective (reuses swarm runAll).
	TopologyFanout Topology = "fanout"
	// TopologyPipeline runs agents in ordered stages, each feeding the next.
	TopologyPipeline Topology = "pipeline"
)

// ParseTopology parses a topology name. The empty string resolves to the
// provided default (so config defaults flow through cleanly).
func ParseTopology(name, fallback string) (Topology, error) {
	if name == "" {
		name = fallback
	}
	switch strings.ToLower(name) {
	case "", "hub":
		return TopologyHub, nil
	case "fanout":
		return TopologyFanout, nil
	case "pipeline":
		return TopologyPipeline, nil
	default:
		return "", fmt.Errorf("orchestrator: unknown topology %q (want hub, fanout, or pipeline)", name)
	}
}

// ErrUnknownTopology is returned when a topology name cannot be resolved.
var ErrUnknownTopology = errors.New("orchestrator: unknown topology")
