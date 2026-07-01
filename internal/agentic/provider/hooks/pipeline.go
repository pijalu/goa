// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"fmt"
	"sort"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// Hook is a pluggable transformation step in the provider request/response
// pipeline.
type Hook interface {
	Name() string
	Order() int
	Init(profile schema.VariantProfile) error
	ApplyRequest(ctx *RequestContext) error
	ApplyResponse(ctx *ResponseContext) error
	ApplyError(ctx *ErrorContext) error
}

// Pipeline is an ordered collection of hooks.
type Pipeline struct {
	hooks []Hook
}

// BuildPipeline creates the canonical hook pipeline for a model.
func BuildPipeline(model schema.Model) *Pipeline {
	return NewPipeline(
		&AuthHook{},
		&SdkKeyHook{},
		&ThinkingHook{},
		&CacheHook{},
		&ToolHook{},
		&MessageHook{},
		&ErrorHook{},
	)
}

// NewPipeline creates a pipeline from the provided hooks and sorts them by
// order.
func NewPipeline(hooks ...Hook) *Pipeline {
	ordered := make([]Hook, len(hooks))
	copy(ordered, hooks)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Order() < ordered[j].Order()
	})
	return &Pipeline{hooks: ordered}
}

// Init initializes all hooks with the given profile.
func (p *Pipeline) Init(profile schema.VariantProfile) error {
	for _, h := range p.hooks {
		if err := h.Init(profile); err != nil {
			return fmt.Errorf("init hook %s: %w", h.Name(), err)
		}
	}
	return nil
}

// ApplyRequest runs all hooks against the request context in order.
func (p *Pipeline) ApplyRequest(ctx *RequestContext) error {
	for _, h := range p.hooks {
		if err := h.ApplyRequest(ctx); err != nil {
			return fmt.Errorf("hook %s: %w", h.Name(), err)
		}
	}
	return nil
}

// ApplyResponse runs all hooks against the response context in order.
func (p *Pipeline) ApplyResponse(ctx *ResponseContext) error {
	for _, h := range p.hooks {
		if err := h.ApplyResponse(ctx); err != nil {
			return fmt.Errorf("hook %s: %w", h.Name(), err)
		}
	}
	return nil
}

// ApplyError runs all hooks against the error context in order.
func (p *Pipeline) ApplyError(ctx *ErrorContext) error {
	for _, h := range p.hooks {
		if err := h.ApplyError(ctx); err != nil {
			return fmt.Errorf("hook %s: %w", h.Name(), err)
		}
	}
	return nil
}

// HookNames returns the ordered names of hooks in the pipeline.
func (p *Pipeline) HookNames() []string {
	names := make([]string, len(p.hooks))
	for i, h := range p.hooks {
		names[i] = h.Name()
	}
	return names
}
