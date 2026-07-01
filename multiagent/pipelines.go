// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import "github.com/pijalu/goa/internal/role"

// BuiltinPipelines returns the set of built-in multi-agent pipelines.
func BuiltinPipelines() []Pipeline {
	return []Pipeline{
		implementFeaturePipeline(),
		reviewChangesPipeline(),
	}
}

// implementFeaturePipeline: planner → coder → reviewer
func implementFeaturePipeline() Pipeline {
	return Pipeline{
		ID:          "implement-feature",
		Name:        "Implement Feature",
		Description: "Plan, implement, and review a feature",
		Stages: []PipelineStage{
			{
				ID:     "plan",
				Name:   "Plan",
				Agent:  role.Planner,
				Prompt: "Analyze the requirements and create a detailed implementation plan.",
			},
			{
				ID:     "code",
				Name:   "Implement",
				Agent:  role.Coder,
				Prompt: "Implement the feature following the approved plan.",
			},
			{
				ID:     "review",
				Name:   "Review",
				Agent:  role.Reviewer,
				Prompt: "Review the implementation for correctness, security, and style.",
			},
		},
	}
}

// reviewChangesPipeline: reviewer only
func reviewChangesPipeline() Pipeline {
	return Pipeline{
		ID:          "review-changes",
		Name:        "Review Changes",
		Description: "Review uncommitted changes",
		Stages: []PipelineStage{
			{
				ID:     "review",
				Name:   "Review",
				Agent:  role.Reviewer,
				Prompt: "Review the current diff and provide feedback.",
			},
		},
	}
}
