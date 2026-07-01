// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"github.com/pijalu/goa/tools/common"
	"github.com/pijalu/goa/tools/plan"
	"github.com/pijalu/goa/tools/todo"
)

// Aliases keep existing tools-package callers working after tools moved into
// sub-packages and shared utilities moved to tools/common. New code is
// encouraged to import the specific sub-package directly.

type (
	FileToolConfig   = common.FileToolConfig
	ReadFileConfig   = common.ReadFileConfig
	GitStager        = common.GitStager
	ToolGroup        = common.ToolGroup
	ToolAccess       = common.ToolAccess
	Accessor         = common.Accessor
	Documentable     = common.Documentable
	DocumentedTool   = common.DocumentedTool
	TruncationResult = common.TruncationResult

	// Sub-package tool types.
	PlanModeTool = plan.PlanModeTool
	TodoListTool = todo.TodoListTool
)

const (
	DefaultMaxLines = common.DefaultMaxLines
	DefaultMaxBytes = common.DefaultMaxBytes
)

// levenshteinDistance is kept as a package-local alias for existing tests.
var levenshteinDistance = common.LevenshteinDistance

var (
	NewGitStager              = common.NewGitStager
	NormalizeFileToolPath     = common.NormalizeFileToolPath
	ResolveFileToolPath       = common.ResolveFileToolPath
	FileToolFuzzyMatchEnabled = common.FileToolFuzzyMatchEnabled
	ReadFileWithFuzzyFallback = common.ReadFileWithFuzzyFallback
	IsProtectedPath           = common.IsProtectedPath
	ResolveToolPath           = common.ResolveToolPath
	FuzzyFindFile             = common.FuzzyFindFile
	LazySyncFromMain          = common.LazySyncFromMain
	TruncateTail              = common.TruncateTail
	TruncateHead              = common.TruncateHead
	TruncResString            = common.TruncResString
	SaveTruncatedOutput       = common.SaveTruncatedOutput
	CompressOutput            = common.CompressOutput
	OutputCompressors         = common.OutputCompressors
	ReadDoc                   = common.ReadDoc
	LoadSearchPriority        = common.LoadSearchPriority
	LoadEmbeddedPriority      = common.LoadEmbeddedPriority
	MergeUserPriorityOverride = common.MergeUserPriorityOverride
	ExtPriority               = common.ExtPriority
	DefaultSearchPriority     = common.DefaultSearchPriority
)

var (
	// readDoc is kept as a package-local alias so existing ShortDoc/LongDoc
	// methods in the tools package can continue using the old name.
	readDoc = common.ReadDoc

	// extPriority is kept as a package-local alias for the search tool.
	extPriority = common.ExtPriority
)
