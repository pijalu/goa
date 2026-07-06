// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"encoding/json"
	"path/filepath"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/logs/export"
)

// orchestratorRunRelDir is the project-relative root under which orchestrator
// run event logs live (events.jsonl per run-id). It mirrors the default
// OrchestrateCommand.RootDir.
const orchestratorRunRelDir = ".goa/orchestrator"

func init() {
	// Register the orchestrator as a diagnostic-bundle contributor. This is
	// the composition hook that bridges the orchestrator domain and the
	// (domain-agnostic) export bundler. Registering here keeps both packages
	// decoupled: export never imports core/orchestrator, and core/orchestrator
	// never imports export. core/commands is imported by every entry point
	// (TUI and headless export), so the contributor is always available.
	export.RegisterContributor(orchestratorRunArtifacts)
}

// orchestratorRunArtifacts contributes the most recent orchestrator run's
// event log plus an agent-friendly JSON summary to the diagnostic bundle, so
// an orchestration crash can be diagnosed from its export (R11).
//
// It depends only on the orchestrator domain's snapshot abstractions
// (ListRuns / ReplaySnapshot), never on event JSON internals — Open/Closed
// for the bundler and Dependency Inversion for the domain.
func orchestratorRunArtifacts(projectDir string) []export.Artifact {
	root := filepath.Join(projectDir, orchestratorRunRelDir)
	runs, err := orchestrator.ListRuns(root)
	if err != nil || len(runs) == 0 {
		return nil
	}
	run := runs[0] // ListRuns is newest-first
	eventsPath := filepath.Join(root, run.RunID, "events.jsonl")
	arts := []export.Artifact{{Name: "orchestrator/events.jsonl", Path: eventsPath}}
	if data := orchestratorRunSummaryJSON(root, run.RunID); data != nil {
		arts = append(arts, export.Artifact{Name: "diagnostics/orchestrator.json", Data: data})
	}
	return arts
}

// orchestratorSummaryAgent and orchestratorSummaryRun are the JSON shapes the
// diagnostic bundle exposes. They are presentation types built from the
// domain's RunSnapshot, kept here (the adapter layer) rather than in the
// domain, which owns only the structured snapshot.
type orchestratorSummaryAgent struct {
	ID     string `json:"agentId"`
	Role   string `json:"role"`
	Model  string `json:"model,omitempty"`
	Status string `json:"status"`
}

type orchestratorSummaryRun struct {
	RunID     string                  `json:"runId"`
	Name      string                  `json:"name,omitempty"`
	Objective string                  `json:"objective,omitempty"`
	Topology  string                  `json:"topology,omitempty"`
	Finished  bool                    `json:"finished"`
	Agents    []orchestratorSummaryAgent `json:"agents"`
}

// orchestratorRunSummaryJSON builds a compact JSON summary of one run from its
// replayed snapshot. Returns nil when the run cannot be replayed.
func orchestratorRunSummaryJSON(root, runID string) []byte {
	snap, err := orchestrator.ReplaySnapshot(orchestrator.NewFileEventStore(root, runID))
	if err != nil || snap == nil {
		return nil
	}
	s := orchestratorSummaryRun{
		RunID:     snap.RunID,
		Name:      snap.Name,
		Objective: snap.Objective,
		Topology:  string(snap.Topology),
		Finished:  snap.Finished,
	}
	for _, a := range snap.Agents {
		s.Agents = append(s.Agents, orchestratorSummaryAgent{
			ID: a.ID, Role: a.Role, Model: a.Model, Status: string(a.Status),
		})
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil
	}
	return data
}
