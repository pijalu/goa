// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	agentic "github.com/pijalu/goa/internal/agentic"
)

// Session provider/model binding (bugs.md 2026-08-26, "session restore does
// not bind the session's provider/model"): every session records WHICH
// provider/model produced — and should keep producing — its turns. The
// config cascade only defaults brand-new sessions; restoring must re-bind
// the recorded pair instead of falling back to whatever ~/.goa selected last.
//
// The record rides in the session's own event stream as an EventProgress
// carrying metadata (same shape as the companion_started marker), written
// straight through SessionStore.WriteEvent: it renders nowhere and is never
// re-persisted through the observer pipeline.

const (
	// ModelSelectedEventKey is the Metadata["event"] value marking a
	// main-agent provider/model binding record.
	ModelSelectedEventKey = "model_selected"

	modelMetaModel    = "model"    // bound model ID (config id or resolved name)
	modelMetaProvider = "provider" // bound provider profile ID ("" = unknown)
	modelMetaSource   = "source"   // start | switch

	// ModelMarkerSourceStart marks the binding applied at session creation.
	ModelMarkerSourceStart = "start"
	// ModelMarkerSourceSwitch marks a mid-session model/provider switch.
	ModelMarkerSourceSwitch = "switch"
)

// ModelSelectedMarker builds the persistence-only event that records a
// provider/model binding in the live session file.
func ModelSelectedMarker(source, providerID, modelID string) agentic.OutputEvent {
	return agentic.OutputEvent{
		Type: agentic.EventProgress,
		Metadata: map[string]string{
			"event":           ModelSelectedEventKey,
			modelMetaModel:    modelID,
			modelMetaProvider: providerID,
			modelMetaSource:   source,
		},
	}
}

// isModelSelectedMarker reports whether ev carries a model_selected record.
func isModelSelectedMarker(ev agentic.OutputEvent) bool {
	if ev.Type != agentic.EventProgress || len(ev.Metadata) == 0 {
		return false
	}
	return ev.Metadata["event"] == ModelSelectedEventKey
}

// SessionModelFromEvents scans session events for the LAST model_selected
// marker and returns its (providerID, modelID). Last-write-wins: the newest
// switch is what a restored session must run on. Malformed markers without a
// model are skipped so older valid records still resolve. ok=false for
// sessions with no usable marker (legacy files) — callers keep the current
// selection untouched.
func SessionModelFromEvents(events []agentic.OutputEvent) (providerID, modelID string, ok bool) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if !isModelSelectedMarker(ev) {
			continue
		}
		p := ev.Metadata[modelMetaProvider]
		m := ev.Metadata[modelMetaModel]
		if m == "" {
			continue
		}
		return p, m, true
	}
	return "", "", false
}

// markerPair identifies the couple recorded by the most recent marker write;
// used to dedupe SetModel emissions for an unchanged binding.
type markerPair struct{ providerID, modelID string }

func pairFrom(providerID, modelID string) markerPair { return markerPair{providerID, modelID} }
