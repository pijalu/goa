// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// marshalBody builds a request body exactly the way the protocol serializers
// do: a Go map marshaled by encoding/json (alphabetical key order —
// "messages" first — with a history append landing INSIDE the array). The
// classifier's tests must use this shape, not hand-written strings whose
// byte layout happens to satisfy a byte-level prefix test.
func marshalBody(t *testing.T, body map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return b
}

// serializedTurns returns two consecutive marshaled bodies: turn1, and turn2
// which appends msgs2 to turn1's messages (plus the given extra fields).
func serializedTurns(t *testing.T, msg1, msg2 map[string]any, extra map[string]any) ([]byte, []byte) {
	t.Helper()
	turn1 := marshalBody(t, map[string]any{"messages": []any{msg1}, "model": "m", "stream": true})
	body2 := map[string]any{"messages": []any{msg1, msg2}, "model": "m", "stream": true}
	for k, v := range extra {
		body2[k] = v
	}
	return turn1, marshalBody(t, body2)
}

// TestBuildRequestFingerprintClassifiesRealAppend verifies the classifier on
// serializer-shaped bodies: an append-only turn (messages grow, every other
// field identical) must classify exact_append even though the new body is NOT
// a byte-prefix of the old one — the exact failure observed in the
// 2026-08-19 exports (10 provably append-only pairs all classified
// unexpected_divergence because messages is the first marshaled key).
func TestBuildRequestFingerprintClassifiesRealAppend(t *testing.T) {
	userMsg := map[string]any{"role": "user", "content": "hi"}
	assistantMsg := map[string]any{"role": "assistant", "content": "hello"}
	prev, cur := serializedTurns(t, userMsg, assistantMsg, nil)

	if appendFP := BuildRequestFingerprint("openai", "gpt-5", "session-secret", prev, cur, 2, 1, "sse", "turn-3", false); appendFP.Classification != PrefixExactAppend {
		t.Fatalf("classification = %q, want %q", appendFP.Classification, PrefixExactAppend)
	}

	// Byte-identical retry stays exact_append (fast path).
	if fp := BuildRequestFingerprint("openai", "gpt-5", "s", prev, prev, 2, 1, "sse", "t", false); fp.Classification != PrefixExactAppend {
		t.Fatalf("identical body classification = %q, want %q", fp.Classification, PrefixExactAppend)
	}
}

func TestBuildRequestFingerprintHashesAreBounded(t *testing.T) {
	userMsg := map[string]any{"role": "user", "content": "hi"}
	prev, cur := serializedTurns(t, userMsg, map[string]any{"role": "assistant", "content": "ok"}, nil)
	fp := BuildRequestFingerprint("openai", "gpt-5", "session-secret", prev, cur, 2, 1, "sse", "turn-3", false)
	if fp.SessionKeyHash == "" || strings.Contains(fp.SessionKeyHash, "session-secret") {
		t.Fatalf("session key leaked or was not hashed: %q", fp.SessionKeyHash)
	}
	if strings.Contains(fp.RequestHash, "role") || len(fp.RequestHash) != 64 {
		t.Fatalf("request hash is not bounded SHA-256: %q", fp.RequestHash)
	}
}

// TestBuildRequestFingerprintParamChange pins the new classification: the
// messages still canonically prefix, but a non-message field differs
// (tools/thinking change mid-conversation) — cache-relevant, distinct from a
// history rewrite.
func TestBuildRequestFingerprintParamChange(t *testing.T) {
	userMsg := map[string]any{"role": "user", "content": "hi"}
	prev, cur := serializedTurns(t, userMsg,
		map[string]any{"role": "assistant", "content": "hello"},
		map[string]any{"tools": []any{map[string]any{"name": "bash"}}},
	)
	if fp := BuildRequestFingerprint("zai", "glm-5.3", "s", prev, cur, 2, 1, "sse", "t", false); fp.Classification != PrefixParamChange {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixParamChange)
	}
}

func TestBuildRequestFingerprint_ToolPolicyTransition(t *testing.T) {
	prev := marshalBody(t, map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"function": map[string]any{"name": "bash"}}},
	})
	cur := marshalBody(t, map[string]any{
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}, map[string]any{"role": "assistant", "content": "done"}},
		"tool_choice": "none",
	})
	fp := BuildRequestFingerprint("kimi-code", "k3", "s", prev, cur, 1, 0, "sse", "t", false)
	if fp.Classification != PrefixToolPolicyTransition {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixToolPolicyTransition)
	}
}

// TestBuildRequestFingerprint_ToolChoiceCollapse pins the post-2026-10-05
// collapse shape: the tool surface is byte-identical, only the prompt-neutral
// tool_choice control field moves. That is cache-neutral by construction, so
// it must never be reported as a miss cause (PrefixToolChoiceCollapse, not
// PrefixToolPolicyTransition and not an opaque param_change).
func TestBuildRequestFingerprint_ToolChoiceCollapse(t *testing.T) {
	tools := []any{map[string]any{"function": map[string]any{"name": "bash"}}}
	messages := []any{map[string]any{"role": "user", "content": "hi"}}
	prev := marshalBody(t, map[string]any{"messages": messages, "tools": tools, "model": "k3"})
	cur := marshalBody(t, map[string]any{
		"messages":    append(append([]any{}, messages...), map[string]any{"role": "assistant", "content": "done"}),
		"tools":       tools,
		"model":       "k3",
		"tool_choice": "none",
	})
	fp := BuildRequestFingerprint("kimi-code", "k3", "s", prev, cur, 2, 0, "sse", "t", false)
	if fp.Classification != PrefixToolChoiceCollapse {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixToolChoiceCollapse)
	}

	// A collapse that also dropped the tools array is the pre-fix shape and
	// stays flagged as the cache-busting tool_policy_transition.
	broken := marshalBody(t, map[string]any{
		"messages":    append(append([]any{}, messages...), map[string]any{"role": "assistant", "content": "done"}),
		"model":       "k3",
		"tool_choice": "none",
	})
	if fp := BuildRequestFingerprint("kimi-code", "k3", "s", prev, broken, 2, 0, "sse", "t", false); fp.Classification != PrefixToolPolicyTransition {
		t.Fatalf("dropped-tools collapse classified %q, want %q", fp.Classification, PrefixToolPolicyTransition)
	}

	// A different non-message field changing alongside tool_choice is not the
	// collapse: it stays an opaque param_change.
	mixed := marshalBody(t, map[string]any{
		"messages":    append(append([]any{}, messages...), map[string]any{"role": "assistant", "content": "done"}),
		"tools":       tools,
		"model":       "k3-other",
		"tool_choice": "none",
	})
	if fp := BuildRequestFingerprint("kimi-code", "k3", "s", prev, mixed, 2, 0, "sse", "t", false); fp.Classification != PrefixParamChange {
		t.Fatalf("mixed-param collapse classified %q, want %q", fp.Classification, PrefixParamChange)
	}

	// The per-flavor text-only spellings are recognized too: Anthropic's
	// {"type":"none"} object and Google's NONE function-calling mode.
	for name, choice := range map[string]any{
		"anthropic-object": map[string]any{"type": "none"},
		"google-mode":      map[string]any{"functionCallingConfig": map[string]any{"mode": "NONE"}},
	} {
		object := marshalBody(t, map[string]any{
			"messages":    append(append([]any{}, messages...), map[string]any{"role": "assistant", "content": "done"}),
			"tools":       tools,
			"model":       "k3",
			"tool_choice": choice,
		})
		if fp := BuildRequestFingerprint("kimi-code", "k3", "s", prev, object, 2, 0, "sse", "t", false); fp.Classification != PrefixToolChoiceCollapse {
			t.Fatalf("%s classified %q, want %q", name, fp.Classification, PrefixToolChoiceCollapse)
		}
	}
}

// TestBuildRequestFingerprintDivergenceAndReplacement covers the negative
// paths: rewriting an already-sent message is unexpected_divergence; the
// replacement flag marks deliberate resets; key-order differences alone
// (provider-side re-serialization) must NOT read as divergence.
func TestBuildRequestFingerprintDivergenceAndReplacement(t *testing.T) {
	// Divergence: turn 1 sends [user "hi"], turn 2 REWRITES that already-sent
	// message (compaction/elision shape) — not an append under any reading.
	prev := marshalBody(t, map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"model":    "m", "stream": true,
	})
	cur := marshalBody(t, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "NEW INSTRUCTIONS"},
			map[string]any{"role": "assistant", "content": "hello"},
		},
		"model": "m", "stream": true,
	})
	if fp := BuildRequestFingerprint("openai", "gpt-5", "s", prev, cur, 3, 2, "sse", "turn-4", false); fp.Classification != PrefixDivergence {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixDivergence)
	}

	// Compaction: shorter history than the predecessor → flagged replacement.
	shorter := marshalBody(t, map[string]any{"messages": []any{map[string]any{"role": "system", "content": "summary"}}, "model": "m", "stream": true})
	if fp := BuildRequestFingerprint("openai", "gpt-5", "s", prev, shorter, 4, 3, "sse", "turn-5", true); fp.Classification != PrefixReplacement {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixReplacement)
	}

	// Same messages, keys re-ordered in the raw bytes (whitespace/key order
	// churn from a proxy) — still an exact append canonically.
	reordered := []byte(`{"stream":true,"model":"m","messages":[{"content":"hi","role":"user"},{"content":"hello","role":"assistant"}]}`)
	if fp := BuildRequestFingerprint("openai", "gpt-5", "s", prev, reordered, 2, 1, "sse", "t", false); fp.Classification != PrefixExactAppend {
		t.Fatalf("key-order churn classification = %q, want %q (canonical comparison)", fp.Classification, PrefixExactAppend)
	}
}

func TestBuildRequestFingerprintNoPredecessor(t *testing.T) {
	fp := BuildRequestFingerprint("p", "m", "s", nil, []byte(`{"messages":[],"model":"m"}`), 0, 0, "sse", "t", false)
	if fp.Classification != PrefixNoPredecessor {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixNoPredecessor)
	}
	if fp.InputPrefixHash != "" {
		t.Fatalf("empty predecessor hash = %q", fp.InputPrefixHash)
	}
	// Flagged reset with no predecessor is a replacement, not no_predecessor.
	if fp := BuildRequestFingerprint("p", "m", "s", nil, []byte(`{}`), 0, 0, "sse", "t", true); fp.Classification != PrefixReplacement {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixReplacement)
	}
}

// TestBuildRequestFingerprintNonJSONFallback keeps the historical byte-level
// semantics for transports whose payload is not a JSON object with a
// messages array: a byte-prefix (identical retry) stays exact_append;
// anything else diverges or, flagged, is a replacement.
func TestBuildRequestFingerprintNonJSONFallback(t *testing.T) {
	prev := []byte("raw-bytes-request")
	if fp := BuildRequestFingerprint("p", "m", "s", prev, append(append([]byte(nil), prev...), []byte("-more")...), 0, 0, "ws", "t", false); fp.Classification != PrefixExactAppend {
		t.Fatalf("non-JSON byte-prefix classification = %q, want %q", fp.Classification, PrefixExactAppend)
	}
	if fp := BuildRequestFingerprint("p", "m", "s", prev, []byte("different"), 0, 0, "ws", "t", false); fp.Classification != PrefixDivergence {
		t.Fatalf("non-JSON divergence classification = %q, want %q", fp.Classification, PrefixDivergence)
	}
}
