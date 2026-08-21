// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"strings"
	"testing"
)

func TestNewCacheKeyContextAndGenerationIsolation(t *testing.T) {
	base := CacheIdentity{ContextID: "ctx-a", Generation: 1, Provider: "openai", Model: "gpt-5", ToolSchemaHash: "tools"}
	if got := NewCacheKey(base); !strings.HasPrefix(got, "goa_") || len(got) != 68 {
		t.Fatalf("key = %q, want opaque goa_ SHA-256 key", got)
	}
	firstKey := NewCacheKey(base)
	if firstKey != NewCacheKey(base) {
		t.Fatal("same identity produced different keys")
	}
	for name, other := range map[string]CacheIdentity{
		"context":    {ContextID: "ctx-b", Generation: 1, Provider: "openai", Model: "gpt-5", ToolSchemaHash: "tools"},
		"generation": {ContextID: "ctx-a", Generation: 2, Provider: "openai", Model: "gpt-5", ToolSchemaHash: "tools"},
		"model":      {ContextID: "ctx-a", Generation: 1, Provider: "openai", Model: "gpt-5-mini", ToolSchemaHash: "tools"},
		"tools":      {ContextID: "ctx-a", Generation: 1, Provider: "openai", Model: "gpt-5", ToolSchemaHash: "other"},
	} {
		if NewCacheKey(base) == NewCacheKey(other) {
			t.Errorf("%s boundary reused cache key", name)
		}
	}
	if strings.Contains(NewCacheKey(base), "ctx-a") || strings.Contains(NewCacheKey(base), "gpt-5") {
		t.Fatal("cache key contains raw identity data")
	}
}
