// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

const thinkingOrder = 300

// ThinkingHook performs cross-model thinking block transformations.
type ThinkingHook struct {
	profile schema.VariantProfile
}

// Name returns the hook name.
func (h *ThinkingHook) Name() string { return "thinking" }

// Order returns the hook order.
func (h *ThinkingHook) Order() int { return thinkingOrder }

// Init initializes the hook with the variant profile.
func (h *ThinkingHook) Init(profile schema.VariantProfile) error {
	h.profile = profile
	return nil
}

// ApplyRequest converts thinking blocks to text when the target model cannot
// consume native thinking content.
func (h *ThinkingHook) ApplyRequest(ctx *RequestContext) error {
	if h.profile.Compat.ThinkingFormat == "" || h.profile.Compat.ThinkingFormat == "none" {
		ctx.Context.Messages = convertThinkingToText(ctx.Context.Messages)
	}
	return nil
}

// ApplyResponse is a no-op for thinking.
func (h *ThinkingHook) ApplyResponse(ctx *ResponseContext) error { return nil }

// ApplyError is a no-op for thinking.
func (h *ThinkingHook) ApplyError(ctx *ErrorContext) error { return nil }

func convertThinkingToText(messages []schema.Message) []schema.Message {
	return transformThinkingToText(messages)
}

func transformThinkingToText(messages []schema.Message) []schema.Message {
	out := make([]schema.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if len(m.Content) == 0 {
			continue
		}
		converted := make([]schema.ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if b.Type == schema.ContentBlockThinking {
				converted = append(converted, schema.ContentBlock{
					Type: schema.ContentBlockText,
					Text: strings.TrimSpace(b.Thinking),
				})
				continue
			}
			converted = append(converted, b)
		}
		out[i].Content = converted
	}
	return out
}
