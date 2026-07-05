// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package orchestrator implements Goa's orchestration subsystem.
//
// The orchestrator sits ABOVE the swarm/multiagent layer and runs a set of
// managed sub-agents to satisfy a single objective. It is selected per-run
// from one of three topologies (hub, fanout, pipeline), backed by a bounded
// agent pool with total + per-model caps. Every state transition is appended
// to an event log under .goa/orchestrator/<run-id>/events.jsonl so any run
// is fully resumable.
//
// See docs/ORCHESTRATION-DESIGN.md for the full design.
package orchestrator
