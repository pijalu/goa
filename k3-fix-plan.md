# Fix Plan: Enable Thinking for Kimi k3

## Root Cause

The Kimi provider profiles (`moonshot.json`, `kimi-code.json`) have `"thinking_format": "none"`
which prevents **all** thinking parameters from being sent in the request body. k3 requires
explicit `reasoning_effort:max` (or `thinking.effort:max`) to emit `reasoning_content` in the
response, but Goa never sends either.

Additionally, `k3` is not in the built-in model registry, so users must manually set
`reasoning: true` in their config YAML.

---

## Flow Trace (before fix)

```
k3 needs reasoning_effort:max in request body
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
  "compat": {
    "supports_store": false,
    "max_tokens_field": "max_tokens",
-   "thinking_format": "none",
+   "thinking_format": "openai",
```

**Rationale:** The `openaiThinking` builder in `thinkingBodyForFormat` produces
`{"reasoning_effort": level}`. Per Kimi docs, k3 accepts `reasoning_effort:max`
(alongside `thinking.effort:max`).

**Safety:** Unknown fields like `reasoning_effort` are silently ignored by providers
that don't support them. Non-reasoning Kimi models are unaffected.

---

### Step 2 — Add k3 to built-in model registry (1 file)

**File:** `internal/agentic/provider/models/models.go`

Insert after the existing `kimi-for-coding` entry (after line 231):

```go
// ── Kimi K3 ──
{
    ID: "k3", Name: "Kimi K3", Api: provider.ApiOpenAICompletions,
    Provider: provider.ProviderKimiCode,
    Reasoning: true, ContextWindow: 128000, MaxTokens: 16384,
    InputTypes: []string{"text"},
    Cost: provider.ModelPricing{Input: 0.000002, Output: 0.000008},
    ThinkingFormat: provider.ThinkingFormatReasoningContent,
},
```

**Rationale:** Without a registry entry, `buildFallbackModel` is used for `k3`,
which doesn't set `Reasoning: true` or `ThinkingFormat`. Users would need
`reasoning: true` in their config YAML — easily missed. A registry entry makes
it work zero-config for anyone who adds k3 as a model.

---

### Step 3 — (Optional) Set default thinking level to "max" (2 files)

k3 currently only supports `max` for thinking effort (low/high coming later).
The default `resolveThinkingLevel` returns `"medium"`. To avoid sending an
unsupported value, add `"thinking": "max"` to the profile defaults.

**Files:**
- `internal/agentic/provider/schema/variants/moonshot.json`
- `internal/agentic/provider/schema/variants/kimi-code.json`

```diff
  "defaults": {
    "temperature": 1.0,
    "max_tokens": 4096,
+   "thinking": "max"
  },
```

**Alternative:** Add a `"kimi"` thinking format builder that normalizes all
effort values to `"max"` (simpler per-model override, but changes `applyThinking`
and `thinkingBodyForFormat`). The profile default is preferred — it's smaller
and doesn't require code changes.

---

## Verification Checklist

| Check | How |
|-------|-----|
| Profile resolution | `ResolveProfile` for `kimi`/`kimi-code` returns `thinking_format: "openai"` |
| Request body | k3 request contains `"reasoning_effort": "max"` |
| Response parsing | `reasoning_content` fields from k3 stream are extracted |
| Event routing | `EventThinkingDelta` → `handleThinkingDelta` → `StateThinking` output |
| Session recording | JSONL contains `State: 1` events with actual thinking text |
| TUI display | Thinking block visible in chat viewport |
| Non-regression | Other OpenAI providers still work (unknown fields ignored) |

---

## Order of Execution

1. `moonshot.json` + `kimi-code.json` — change `thinking_format` to `"openai"`
2. `models.go` — add k3 registry entry
3. Profiles — add `"thinking": "max"` default (optional)

Steps 1+2 are the minimum viable fix:
- Step 1 alone unblocks thinking for users who configure `reasoning: true` + `thinking_level: max`
  in their model config
- Step 2 removes that config burden for k3

---

## After-Fix Flow

```
k3 needs reasoning_effort:max in request body
  → profile says thinking_format="openai" ✓
    → openaiThinking("max") → {"reasoning_effort": "max"}
      → request body has reasoning_effort:max
        → k3 API returns reasoning_content in streaming chunks
          → parseThinkingDeltas() extracts it
            → EventThinkingDelta emitted
              → handleThinkingDelta() → emitEvent(State: StateThinking)
                → TUI displays thinking block
                  → Session records State:1 events with thinking text
```
