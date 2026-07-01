// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"
)

func checkCmdMeta(t *testing.T, cmd interface {
	Name() string
	ShortHelp() string
	LongHelp() string
}) {
	t.Helper()
	if cmd.Name() == "" {
		t.Error("expected non-empty Name")
	}
	if cmd.ShortHelp() == "" {
		t.Error("expected non-empty ShortHelp")
	}
	if cmd.LongHelp() == "" {
		t.Error("expected non-empty LongHelp")
	}
}

func TestAutonomyCommand_Meta(t *testing.T)        { checkCmdMeta(t, &AutonomyCommand{}) }
func TestCompanionToggleCommand_Meta(t *testing.T) { checkCmdMeta(t, &CompanionToggleCommand{}) }
func TestCompressCommand_Meta(t *testing.T)        { checkCmdMeta(t, &CompressCommand{}) }
func TestConfigCommand_Meta(t *testing.T)          { checkCmdMeta(t, &ConfigCommand{}) }
func TestCopyCommand_Meta(t *testing.T)            { checkCmdMeta(t, &CopyCommand{}) }
func TestDebugCommand_Meta(t *testing.T)           { checkCmdMeta(t, &DebugCommand{}) }
func TestDocsCommand_Meta(t *testing.T)            { checkCmdMeta(t, &DocsCommand{}) }
func TestExchangeCommand_Meta(t *testing.T)        { checkCmdMeta(t, &ExchangeCommand{}) }
func TestGoaCommand_Meta(t *testing.T)             { checkCmdMeta(t, &GoaCommand{}) }
func TestHelpCommand_Meta(t *testing.T)            { checkCmdMeta(t, &HelpCommand{}) }
func TestMemoryCommand_Meta(t *testing.T)          { checkCmdMeta(t, &MemoryCommand{}) }
func TestModeCommand_Meta(t *testing.T)            { checkCmdMeta(t, &ModeCommand{}) }
func TestModelCommand_Meta(t *testing.T)           { checkCmdMeta(t, &ModelCommand{}) }
func TestOrchestrateCommand_Meta(t *testing.T)     { checkCmdMeta(t, &OrchestrateCommand{}) }
func TestPipelineCommand_Meta(t *testing.T)        { checkCmdMeta(t, &PipelineCommand{}) }
func TestProfileCommand_Meta(t *testing.T)         { checkCmdMeta(t, &ProfileCommand{}) }
func TestPromptCommand_Meta(t *testing.T)          { checkCmdMeta(t, &PromptCommand{}) }
func TestProviderCommand_Meta(t *testing.T)        { checkCmdMeta(t, &ProviderCommand{}) }
func TestPTYCommand_Meta(t *testing.T)             { checkCmdMeta(t, &PTYCommand{}) }
func TestReloadCommand_Meta(t *testing.T)          { checkCmdMeta(t, &ReloadCommand{}) }
func TestSessionCommand_Meta(t *testing.T)         { checkCmdMeta(t, &SessionCommand{}) }
func TestSkillsCommand_Meta(t *testing.T)          { checkCmdMeta(t, &SkillsCommand{}) }
func TestStopCommand_Meta(t *testing.T)            { checkCmdMeta(t, &StopCommand{}) }
func TestRetryCommand_Meta(t *testing.T)           { checkCmdMeta(t, &RetryCommand{}) }
func TestThinkingCommand_Meta(t *testing.T)        { checkCmdMeta(t, &ThinkingCommand{}) }
func TestThinkingBlocksCommand_Meta(t *testing.T)  { checkCmdMeta(t, &ThinkingBlocksCommand{}) }
func TestStatsCommand_Meta(t *testing.T)           { checkCmdMeta(t, &StatsCommand{}) }
func TestUICommand_Meta(t *testing.T)              { checkCmdMeta(t, &UICommand{}) }
func TestUndoCommand_Meta(t *testing.T)            { checkCmdMeta(t, &UndoCommand{}) }
func TestVersionCommand_Meta(t *testing.T)         { checkCmdMeta(t, &VersionCommand{}) }
func TestWorkflowsCommand_Meta(t *testing.T)       { checkCmdMeta(t, &WorkflowsCommand{}) }
func TestQuitCommand_Meta(t *testing.T)            { checkCmdMeta(t, &QuitCommand{}) }
func TestToolsDocCommand_Meta(t *testing.T)        { checkCmdMeta(t, &ToolsDocCommand{}) }
func TestGoCommand_Meta(t *testing.T)              { checkCmdMeta(t, &GoCommand{}) }
func TestPairCommand_Meta(t *testing.T)            { checkCmdMeta(t, &PairCommand{}) }
func TestReviewerCommand_Meta(t *testing.T)        { checkCmdMeta(t, &ReviewerCommand{}) }
