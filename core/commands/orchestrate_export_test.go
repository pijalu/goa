// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorRunArtifacts_BundlesLatestRun is the RED→GREEN test for R11
// (SOLID): the diagnostic contributor bundles the most recent orchestrator
// run's event log + an agent-friendly JSON summary, using the domain's
// snapshot abstraction (no event-JSON parsing in the export package).
func TestOrchestratorRunArtifacts_BundlesLatestRun(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, orchestratorRunRelDir)
	store := orchestrator.NewFileEventStore(root, "run-1")
	mustAppend := func(typ orchestrator.EventType, agentID, role string, payload map[string]any) {
		_ = store.Append(orchestrator.Event{Type: typ, RunID: "run-1", AgentID: agentID, Role: role, Payload: payload})
	}
	mustAppend(orchestrator.EventRunStarted, "", "", map[string]any{"objective": "ship it", "topology": "hub"})
	mustAppend(orchestrator.EventAgentStarted, "coder-1", "coder", map[string]any{"model": "gemma"})
	mustAppend(orchestrator.EventAgentFinished, "coder-1", "coder", map[string]any{"outcome": "ok"})
	mustAppend(orchestrator.EventRunFinished, "", "", map[string]any{"ok": true})
	store.Flush()

	arts := orchestratorRunArtifacts(dir)
	if len(arts) != 2 {
		t.Fatalf("artifacts = %d, want 2 (events + summary): %+v", len(arts), arts)
	}

	var eventPath string
	var summaryData []byte
	for i := range arts {
		switch arts[i].Name {
		case "orchestrator/events.jsonl":
			eventPath = arts[i].Path
		case "diagnostics/orchestrator.json":
			summaryData = arts[i].Data
		}
	}
	if eventPath == "" {
		t.Error("missing orchestrator/events.jsonl artifact")
	}
	if len(summaryData) == 0 {
		t.Fatal("missing/empty diagnostics/orchestrator.json artifact")
	}

	got := string(summaryData)
	for _, want := range []string{`"runId": "run-1"`, `"objective": "ship it"`, `"topology": "hub"`, `"finished": true`, `"coder-1"`, `"coder"`} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
}

// TestOrchestratorRunArtifacts_NoRunsReturnsNil asserts the contributor is a
// no-op (not an error) when no orchestrator run exists.
func TestOrchestratorRunArtifacts_NoRunsReturnsNil(t *testing.T) {
	if arts := orchestratorRunArtifacts(t.TempDir()); arts != nil {
		t.Errorf("expected no artifacts without runs, got %d", len(arts))
	}
}
