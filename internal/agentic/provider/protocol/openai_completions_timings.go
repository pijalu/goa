// SPDX-License-Identifier: GPL-3.0-or-later

package protocol

func mergeRootTimings(raw map[string]any) *parserTimings {
	var timings *parserTimings
	if timingsRaw, ok := raw["timings"].(map[string]any); ok {
		timings = parseTimings(timingsRaw)
	}
	if usageRaw, ok := raw["usage"].(map[string]any); ok {
		if t := parseOpenAIUsage(usageRaw); t != nil {
			if timings == nil {
				timings = t
			} else {
				mergeTimings(timings, t)
			}
		}
	}
	return timings
}

func parseTimings(raw map[string]any) *parserTimings {
	t := &parserTimings{}
	if v, ok := raw["prompt_n"].(float64); ok {
		t.PromptN = int(v)
	}
	if v, ok := raw["predicted_n"].(float64); ok {
		t.PredictedN = int(v)
	}
	if v, ok := raw["prompt_ms"].(float64); ok {
		t.PromptMs = v
	}
	if v, ok := raw["predicted_ms"].(float64); ok {
		t.PredictedMs = v
	}
	if v, ok := raw["prompt_per_second"].(float64); ok {
		t.PromptPerSecond = v
	}
	if v, ok := raw["predicted_per_second"].(float64); ok {
		t.PredictedPerSecond = v
	}
	if t.PromptN == 0 && t.PredictedN == 0 && t.PromptMs == 0 && t.PredictedMs == 0 {
		return nil
	}
	return t
}

func parseOpenAIUsage(raw map[string]any) *parserTimings {
	t := &parserTimings{}
	rawPromptN := readRawPromptTokens(raw, t)
	readCacheReadTokens(raw, t)
	completionCacheTokens := readCompletionCacheTokens(raw)
	readCacheWriteTokens(raw, t)
	computePromptN(rawPromptN, t)
	if completionCacheTokens > 0 {
		t.CacheReadTokens += completionCacheTokens
	}
	applyTimingEstimate(raw, t)
	if rawPromptN == 0 && t.PredictedN == 0 && t.CacheReadTokens == 0 && t.CacheWriteTokens == 0 {
		return nil
	}
	return t
}

func readRawPromptTokens(raw map[string]any, t *parserTimings) int {
	var rawPromptN int
	if v, ok := raw["prompt_tokens"].(float64); ok {
		rawPromptN = int(v)
	}
	if v, ok := raw["completion_tokens"].(float64); ok {
		t.PredictedN = int(v)
	}
	if rawPromptN == 0 && t.PredictedN == 0 {
		if v, ok := raw["total_tokens"].(float64); ok {
			rawPromptN = int(v)
		}
	}
	return rawPromptN
}

func readCacheReadTokens(raw map[string]any, t *parserTimings) {
	candidates := []struct{ detailsKey, fieldKey string }{
		{"prompt_tokens_details", "cached_tokens"},
		{"input_tokens_details", "cached_tokens"},
	}
	for _, c := range candidates {
		if cached := readNestedFloat(raw, c.detailsKey, c.fieldKey); cached > 0 && t.CacheReadTokens == 0 {
			t.CacheReadTokens = int(cached)
		}
	}
	for _, key := range []string{"prompt_cache_hit_tokens", "tokens_cached"} {
		if cached := readFloat(raw, key); cached > 0 && t.CacheReadTokens == 0 {
			t.CacheReadTokens = int(cached)
		}
	}
}

func readCompletionCacheTokens(raw map[string]any) int {
	if cached := readNestedFloat(raw, "completion_tokens_details", "cached_tokens"); cached > 0 {
		return int(cached)
	}
	return 0
}

func readCacheWriteTokens(raw map[string]any, t *parserTimings) {
	if writeTokens := readNestedFloat(raw, "prompt_tokens_details", "cache_write_tokens"); writeTokens > 0 {
		t.CacheWriteTokens = int(writeTokens)
	}
}

func computePromptN(rawPromptN int, t *parserTimings) {
	if rawPromptN <= 0 {
		return
	}
	t.PromptN = rawPromptN - t.CacheReadTokens - t.CacheWriteTokens
	if t.PromptN < 0 {
		t.PromptN = 0
	}
}

func applyTimingEstimate(raw map[string]any, t *parserTimings) {
	if v, ok := raw["time_per_output_token_ms"].(float64); ok && v > 0 && t.PredictedN > 0 {
		t.PredictedMs = v * float64(t.PredictedN)
	}
}

func applyRootTokenCounts(raw map[string]any, timings **parserTimings) int {
	var rootPromptTokens int
	if pv, ok := raw["prompt_tokens"].(float64); ok && pv > 0 {
		rootPromptTokens = int(pv)
	}
	if cv, ok := raw["completion_tokens"].(float64); ok && cv > 0 {
		ensureTimings(timings)
		if (*timings).PredictedN == 0 {
			(*timings).PredictedN = int(cv)
		}
	}
	return rootPromptTokens
}

func applyRootCacheFields(raw map[string]any, timings **parserTimings) {
	if cachedVal, ok := raw["tokens_cached"].(float64); ok && cachedVal > 0 {
		ensureTimings(timings)
		(*timings).CacheReadTokens = int(cachedVal)
	}
	if cachedVal, ok := raw["prompt_cache_hit_tokens"].(float64); ok && cachedVal > 0 {
		ensureTimings(timings)
		if (*timings).CacheReadTokens == 0 {
			(*timings).CacheReadTokens = int(cachedVal)
		}
	}
}

func computeRootPromptN(rootPromptTokens int, timings **parserTimings) {
	if rootPromptTokens == 0 || *timings == nil {
		return
	}
	t := *timings
	if t.PromptN == 0 || t.CacheReadTokens > 0 || t.CacheWriteTokens > 0 {
		if t.PromptN == 0 || t.PromptN == rootPromptTokens {
			rawPrompt := rootPromptTokens
			if t.PromptN > 0 {
				rawPrompt = t.PromptN + t.CacheReadTokens + t.CacheWriteTokens
			}
			t.PromptN = rawPrompt - t.CacheReadTokens - t.CacheWriteTokens
			if t.PromptN < 0 {
				t.PromptN = 0
			}
		}
	}
}

func ensureTimings(timings **parserTimings) {
	if *timings == nil {
		*timings = &parserTimings{}
	}
}

func mergeTimings(dst, src *parserTimings) {
	if src.PromptN > 0 {
		dst.PromptN = src.PromptN
	}
	if src.PredictedN > 0 {
		dst.PredictedN = src.PredictedN
	}
	if src.PromptMs > 0 {
		dst.PromptMs = src.PromptMs
	}
	if src.PredictedMs > 0 {
		dst.PredictedMs = src.PredictedMs
	}
	if src.PromptPerSecond > 0 {
		dst.PromptPerSecond = src.PromptPerSecond
	}
	if src.PredictedPerSecond > 0 {
		dst.PredictedPerSecond = src.PredictedPerSecond
	}
	if src.CacheReadTokens > 0 {
		dst.CacheReadTokens = src.CacheReadTokens
	}
	if src.CacheWriteTokens > 0 {
		dst.CacheWriteTokens = src.CacheWriteTokens
	}
}

func readFloat(raw map[string]any, key string) float64 {
	if v, ok := raw[key].(float64); ok {
		return v
	}
	return 0
}

func readNestedFloat(raw map[string]any, mapKey, fieldKey string) float64 {
	m, ok := raw[mapKey].(map[string]any)
	if !ok {
		return 0
	}
	return readFloat(m, fieldKey)
}
