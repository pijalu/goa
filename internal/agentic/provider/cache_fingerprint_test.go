// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"strings"
	"testing"
)

func TestBuildRequestFingerprintClassifiesAppendAndReplacement(t *testing.T) {
	previous := []byte(`{"input":[{"role":"user","content":"hi"}]}`)
	appendRequest := append(append([]byte(nil), previous...), []byte(`,{"role":"assistant"}]}`)...)
	appendFP := BuildRequestFingerprint("openai", "gpt-5", "session-secret", previous, appendRequest, 2, 1, "sse", "turn-3", false)
	if appendFP.Classification != PrefixExactAppend {
		t.Fatalf("classification = %q, want %q", appendFP.Classification, PrefixExactAppend)
	}
	if appendFP.SessionKeyHash == "" || strings.Contains(appendFP.SessionKeyHash, "session-secret") {
		t.Fatalf("session key leaked or was not hashed: %q", appendFP.SessionKeyHash)
	}
	if strings.Contains(appendFP.RequestHash, "role") || len(appendFP.RequestHash) != 64 {
		t.Fatalf("request hash is not bounded SHA-256: %q", appendFP.RequestHash)
	}

	replacement := BuildRequestFingerprint("openai", "gpt-5", "session-secret", previous, []byte(`{"input":[{"role":"user","content":"new"}]}`), 3, 2, "sse", "turn-4", true)
	if replacement.Classification != PrefixReplacement {
		t.Fatalf("classification = %q, want %q", replacement.Classification, PrefixReplacement)
	}
	unexpected := BuildRequestFingerprint("openai", "gpt-5", "session-secret", previous, []byte(`{"input":[]}`), 2, 1, "sse", "turn-4", false)
	if unexpected.Classification != PrefixDivergence {
		t.Fatalf("classification = %q, want %q", unexpected.Classification, PrefixDivergence)
	}
}

func TestBuildRequestFingerprintNoPredecessor(t *testing.T) {
	fp := BuildRequestFingerprint("p", "m", "s", nil, []byte("request"), 0, 0, "sse", "t", false)
	if fp.Classification != PrefixNoPredecessor {
		t.Fatalf("classification = %q, want %q", fp.Classification, PrefixNoPredecessor)
	}
	if fp.InputPrefixHash != "" {
		t.Fatalf("empty predecessor hash = %q", fp.InputPrefixHash)
	}
}
