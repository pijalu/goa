# Fix Plan: Enable Thinking for Kimi k3

## Root Cause

The Kimi provider profiles (`moonshot.json`, `kimi-code.json`) have `"thinking_format": "none"`
which prevents **all** thinking parameters from being sent in the request body. k3 requires
explicit `reasoning_effort:max` (or `thinking.effort:max`) to emit `reasoning_content` in the
response, but Goa never sends either.

Additionally, `k3` is not in the built-in model registry, so users must manually set
`reasoning: true` in their config YAML.

---

## k3 Specifics (from Kimi docs)

### Reasoning Effort Mapping

k3 maps effort values as follows:

| Sent value | k3 maps to | Notes |
|------------|-----------|-------|
| `null` / `undefined` | `max` | k3 default when unset |
| `max` / `ultra` / `xhigh` | `max` | |
| `high` / `medium` | `high` | |
| `low` / `minimum` / `light` | `low` | |
| `none` | thinking disabled | |
| **anything else** | **HTTP 400** | k3 rejects unknown values |

**Critical:** `"medium"` is valid (maps to `high`), but only the exact values listed
above are accepted. `"minimal"` would cause HTTP 400.

### Context Window

k3 supports up to **1M tokens** (1048576). The registry entry must set this.

---

## Flow Trace (before fix)

```
k3 needs reasoning_effort in request body
  → profile says thinking_format="none"
    → applyThinking() has no builder for "none"
      → for k,v := range nil → zero thinking params in request body
        → k3 API receives no thinking opt-in
          → k3 returns no reasoning_content in streaming chunks
            → parseThinkingDeltas() has nothing to extract
              → no EventThinkingDelta events
                → handleThinkingDelta() never called
                  → no StateThinking output events
```

---

## Changes

### Step 1 — Fix profiles (2 files)

**Files:**
- `internal/agentic/provider/schema/variants/moonshot.json` (line 8)
- `internal/agentic/provider/schema/variants/kimi-code.json` (line 8)

```diff
  "defaults": {
    "temperature": 1.0,
    "max_tokens": 4096,
+   "thinking": "max"        ← k3 default when unset; avoids "medium" surprises
  },
  "compat": {
    "supports_store": false,
    "max_tokens_field": "max_tokens",
-   "thinking_format": "none",
+   "thinking_format": "openai",
```

**Rationale:** The `openaiThinking` builder in `thinkingBodyForFormat` produces
`{"reasoning_effort": level}`. Per Kimi docs, k3 accepts `reasoning_effort:max`.
The default `"thinking": "max"` mirrors k3's own default (null→max) and avoids
sending `"medium"` which, while valid (maps to `high`), is not the optimal default
for k3's current capabilities.

**Safety of `"openai"` format:** Unknown fields like `reasoning_effort` are silently
ignored by providers that don't support them. Non-reasoning Kimi models are unaffected.

**Safety of `"thinking": "max"` default:** `resolveThinkingLevel` checks
`profile.Defaults.Thinking` before falling back to `"medium"`. Setting `"max"` here
means `null/undefined` in k3 terms gets `"max"` which is the documented default
behavior. User-configured thinking levels still override this.

---

### Step 2 — Add k3 to built-in model registry (1 file)

**File:** `internal/agentic/provider/models/models.go`

Insert after the existing `kimi-for-coding` entry (after line 231):

```go
// ── Kimi K3 ──
{
    ID: "k3", Name: "Kimi K3", Api: provider.ApiOpenAICompletions,
    Provider: provider.ProviderKimiCode,
    Reasoning: true, ContextWindow: 1048576, MaxTokens: 16384,
    InputTypes: []string{"text"},
    Cost: provider.ModelPricing{Input: 0.000002, Output: 0.000008},
    ThinkingFormat: provider.ThinkingFormatReasoningContent,
},
```

**Rationale:**
- Without a registry entry, `buildFallbackModel` is used, which doesn't set
  `Reasoning: true` or `ThinkingFormat`. Users would need `reasoning: true`
  in their config YAML — easily missed. A registry entry makes it work zero-config.
- `ContextWindow: 1048576` matches k3's documented 1M token capacity.
  Without this, other contexts (like compression triggers) use the wrong budget.

---

### Step 3 — Map `"minimal"` to `"low"` for k3 (optional, defensive)

k3 rejects unknown effort values with HTTP 400. If a user configures `thinking_level: minimal`,
Goa would send `reasoning_effort:minimal` which is not in k3's mapping → HTTP 400.

Two approaches:

**Option A** — Add a `ThinkingLevelMap` to the k3 profile:

In `moonshot.json` / `kimi-code.json`:
```json
"defaults": {
    "temperature": 1.0,
    "max_tokens": 4096,
    "thinking": "max",
    "thinking_level_map": {
        "minimal": "low",
        "low": "low",
        "medium": "high",
        "high": "high",
        "xhigh": "max",
        "max": "max"
    }
},
```

**Option B** — Add a `"kimi"` thinking format builder in
`internal/agentic/provider/protocol/openai_completions.go` that applies the mapping:

```go
func kimiThinking(level string) map[string]any {
    mapped := map[string]string{
        "minimal": "low",
        "low":     "low",
        "medium":  "high",
        "high":    "high",
        "xhigh":   "max",
        "max":     "max",
    }
    if v, ok := mapped[level]; ok {
        return map[string]any{"reasoning_effort": v}
    }
    return map[string]any{"reasoning_effort": "max"}  // default to max
}
```

And register it in `thinkingBodyForFormat`:
```go
"kimi": kimiThinking,
```

**Recommendation:** Option A (ThinkingLevelMap in profile) is simpler and doesn't
require Go code changes. Option B is more correct (catches any future unknown values)
but adds code.

---

## Verification Checklist

| Check | How |
|-------|-----|
| Profile resolution | `ResolveProfile` for `kimi`/`kimi-code` returns `thinking_format: "openai"` |
| Request body | k3 request contains `"reasoning_effort": "max"` |
| Unknown effort | `thinking_level: minimal` sends `"reasoning_effort": "low"` (not HTTP 400) |
| Response parsing | `reasoning_content` fields from k3 stream are extracted |
| Event routing | `EventThinkingDelta` → `handleThinkingDelta` → `StateThinking` output |
| Session recording | JSONL contains `State: 1` events with actual thinking text |
| TUI display | Thinking block visible in chat viewport |
| Non-regression | Other OpenAI providers still work (unknown fields ignored) |
| Context window | k3 model reports `ContextWindow: 1048576` |

---

## Order of Execution

1. `moonshot.json` + `kimi-code.json` — change `thinking_format` to `"openai"`
   and add `"thinking": "max"` default
2. `models.go` — add k3 registry entry with 1M context
3. ThinkingLevelMap in profiles or kimi format builder (defensive, optional)

Steps 1+2 are the minimum viable fix:
- Step 1 alone unblocks thinking for users who configure `reasoning: true` + `thinking_level: max`
  in their model config
- Step 2 removes that config burden for k3

---

## After-Fix Flow

```
k3 needs reasoning_effort in request body
  → profile says thinking_format="openai" ✓
    → defaults.thinking="max" → resolveThinkingLevel → "max"
    → openaiThinking("max") → {"reasoning_effort": "max"}
      → request body has reasoning_effort:max
        → k3 API returns reasoning_content in streaming chunks
          → parseThinkingDeltas() extracts it
            → EventThinkingDelta emitted
              → handleThinkingDelta() → emitEvent(State: StateThinking)
                → TUI displays thinking block
                  → Session records State:1 events with thinking text
```
