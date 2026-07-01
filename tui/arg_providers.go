// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
)

// ArgProvider provides argument completions for a command's parameter phase.
type ArgProvider interface {
	// Complete returns completions for the given command name and arg prefix.
	// cmdName includes the leading slash (e.g., "/model").
	// argPrefix is the text the user has typed after the last ":".
	Complete(cmdName, argPrefix string) []Completion
}

// ModelProvider defines the interface the ModelArgProvider needs to query models.
type ModelProvider interface {
	ListModels() []string
}

// ModelArgProvider completes model names for the /model command.
type ModelArgProvider struct {
	provider ModelProvider
}

// NewModelArgProvider creates a ModelArgProvider.
func NewModelArgProvider(p ModelProvider) *ModelArgProvider {
	return &ModelArgProvider{provider: p}
}

// Complete returns model names matching the arg prefix.
func (p *ModelArgProvider) Complete(cmdName, argPrefix string) []Completion {
	models := p.provider.ListModels()
	if len(models) == 0 {
		return nil
	}
	var result []Completion
	for _, m := range models {
		if strings.HasPrefix(m, argPrefix) {
			result = append(result, Completion{Value: m, Display: m})
		}
	}
	return result
}
