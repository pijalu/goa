// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"encoding/json"
	"sync"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// wsSessionBaseline is the per-session record of the last full WebSocket
// request and the last successful response, used by later incremental-delta
// tasks (6b.2/6b.3) to decide whether a turn can be sent as a delta chained
// by previous_response_id. All slices are deep copies owned by the baseline;
// the response id is treated as opaque (hashed in any diagnostic).
type wsSessionBaseline struct {
	// LastInput is the last full request input (the conversation messages the
	// provider was asked to send), as an owned deep copy.
	LastInput []provider.Message
	// ResponseID is the server-issued id of the last successful WS response.
	ResponseID string
	// AddedItems are the output items the last response appended (the
	// assistant/tool items that extend the conversation), as an owned deep copy.
	AddedItems []provider.Message
	// Fingerprint is the property fingerprint of the full-history request that
	// opened this baseline. The next turn can chain by previous_response_id
	// only when its request properties match this fingerprint exactly (6b.2).
	Fingerprint requestFingerprint
}

// wsBaselineRegistry keeps one baseline per session so concurrent Codex
// sessions never cross-contaminate. The registry itself is package-global
// (the provider types are stateless value objects, instantiated per call), but
// each entry is strictly keyed by the session cache key.
var wsBaselines = &wsBaselineStore{bySession: map[string]*wsSessionBaseline{}}

type wsBaselineStore struct {
	mu        sync.Mutex
	bySession map[string]*wsSessionBaseline
}

// wsBaselineSessionKey derives the registry key for a request. Prefers the
// explicit prompt_cache_key (the Codex session id); falls back to SessionID.
// Empty means the caller cannot be pinned to a session — capture is skipped.
func wsBaselineSessionKey(opts provider.StreamOptions) string {
	if opts.PromptCacheKey != "" {
		return opts.PromptCacheKey
	}
	return opts.SessionID
}

// recordWSBaseline stores the baseline for a successful Codex WS turn. All
// inputs are deep-copied so later agent/transport operations cannot mutate
// the captured state.
func recordWSBaseline(sessionKey string, lastInput []provider.Message, responseID string, added []provider.Message, fingerprint requestFingerprint) {
	if sessionKey == "" {
		return
	}
	wsBaselines.mu.Lock()
	defer wsBaselines.mu.Unlock()
	wsBaselines.bySession[sessionKey] = &wsSessionBaseline{
		LastInput:   deepCopyMessages(lastInput),
		ResponseID:  responseID,
		AddedItems:  deepCopyMessages(added),
		Fingerprint: fingerprint,
	}
}

// wsBaseline returns a deep copy of the stored baseline for sessionKey, or nil
// when none was recorded. Callers receive an owned copy so they can compare or
// build deltas without racing a concurrent capture.
func wsBaseline(sessionKey string) *wsSessionBaseline {
	if sessionKey == "" {
		return nil
	}
	wsBaselines.mu.Lock()
	defer wsBaselines.mu.Unlock()
	b := wsBaselines.bySession[sessionKey]
	if b == nil {
		return nil
	}
	return &wsSessionBaseline{
		LastInput:   deepCopyMessages(b.LastInput),
		ResponseID:  b.ResponseID,
		AddedItems:  deepCopyMessages(b.AddedItems),
		Fingerprint: b.Fingerprint,
	}
}

// resetWSBaselines wipes the registry. Test helper (mirrors
// provider.ResetCacheForensics) so capture assertions stay isolated.
func resetWSBaselines() {
	wsBaselines.mu.Lock()
	defer wsBaselines.mu.Unlock()
	wsBaselines.bySession = map[string]*wsSessionBaseline{}
}

// resetWSSessionState wipes both the baseline registry and the per-session
// WS-fallback marks. Test helper so the WS send-path tests stay isolated.
func resetWSSessionState() {
	resetWSBaselines()
	resetWSFallbacks()
}

// deepCopyMessages returns an owned deep copy of msgs (Content blocks and
// Extra maps included) so no slice/map is shared with the caller.
func deepCopyMessages(msgs []provider.Message) []provider.Message {
	if msgs == nil {
		return nil
	}
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		out[i] = deepCopyMessage(m)
	}
	return out
}

func deepCopyMessage(m provider.Message) provider.Message {
	c := m
	if m.Content != nil {
		c.Content = make([]provider.ContentBlock, len(m.Content))
		copy(c.Content, m.Content)
	}
	if m.Usage != nil {
		u := *m.Usage
		c.Usage = &u
	}
	if m.Extra != nil {
		extra := make(map[string]interface{}, len(m.Extra))
		for k, v := range m.Extra {
			extra[k] = v
		}
		c.Extra = extra
	}
	return c
}

// completedResponse is the subset of the response.completed payload needed for
// the WS baseline: the response id and the output items it appended.
type completedResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Output []struct {
		Type   string          `json:"type"`
		ID     string          `json:"id"`
		Role   string          `json:"role"`
		Name   string          `json:"name"`
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		// message items carry content parts
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

// toAddedItems converts the completed response's output array into provider
// messages (one per output item). Function-call items become tool-call
// messages; message items become assistant text messages. Unknown item types
// are skipped.
func (r *completedResponse) toAddedItems() []provider.Message {
	var out []provider.Message
	for _, it := range r.Output {
		switch it.Type {
		case "function_call":
			out = append(out, provider.Message{
				Role: provider.RoleAssistant,
				Content: []provider.ContentBlock{{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    it.ID,
					ToolName:      it.Name,
					ToolArguments: string(it.Input),
				}},
			})
		case "message":
			var blocks []provider.ContentBlock
			for _, c := range it.Content {
				if c.Type == "output_text" && c.Text != "" {
					blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockText, Text: c.Text})
				}
			}
			if len(blocks) > 0 {
				out = append(out, provider.Message{Role: provider.RoleAssistant, Content: blocks})
			}
		}
	}
	return out
}
