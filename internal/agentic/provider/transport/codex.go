// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package transport

import (
	"encoding/json"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// CodexEvent represents an event from the Codex WebSocket protocol.
type CodexEvent struct {
	Type    string `json:"type"`
	Delta   string `json:"delta,omitempty"`
	ItemID  string `json:"item_id,omitempty"`
	Content string `json:"content,omitempty"`
}

// MapCodexEvent converts a Codex event to canonical assistant events.
func MapCodexEvent(raw []byte) ([]schema.AssistantMessageEvent, error) {
	var ev CodexEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, err
	}

	switch ev.Type {
	case "response.output_text.delta":
		return []schema.AssistantMessageEvent{{
			Type:  schema.EventTextDelta,
			Delta: ev.Delta,
		}}, nil
	case "response.completed":
		return []schema.AssistantMessageEvent{{
			Type:       schema.EventDone,
			StopReason: schema.StopReasonEndTurn,
		}}, nil
	default:
		return nil, nil
	}
}
