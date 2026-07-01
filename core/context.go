// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// ModelValidator exposes background model validation results to commands.
type ModelValidator interface {
	IsValid(modelID string) bool
}

// ModelCache stores provider model lists so commands can avoid redundant API calls.
type ModelCache interface {
	Get(providerID string, ttl time.Duration) ([]provider.ModelInfo, bool)
	Set(providerID string, models []provider.ModelInfo)
}

// ToolRegistry provides tool lookup for commands.
type ToolRegistry interface {
	Get(name string) (agentic.Tool, bool)
	All() []agentic.Tool
	Register(tool agentic.Tool)
	Unregister(name string)
}

// SkillRegistry provides skill lookup for commands.
type SkillRegistry interface {
	Get(name string) (*skills.Skill, bool)
	List() []skills.SkillSummary
	IsInline(name string) bool
}

// ProviderManager provides provider operations for commands.
type ProviderManager interface {
	Active() (*config.ProviderConfig, string)
	SetActive(providerID, model string) error
	ListModels(providerID string) ([]provider.ModelInfo, error)
	ListModelsCached(providerID string, ttl time.Duration) ([]provider.ModelInfo, error)
	TestConnection(providerID string) (time.Duration, int, error)
	ResolveActiveModel() (agenticprovider.Model, error)
	BuildStreamOptions() agenticprovider.StreamOptions
}

// MemoryStore provides memory operations for commands.
type MemoryStore interface {
	List() ([]memory.MemoryFileInfo, error)
	Read(name string) (string, error)
	Write(name, content string) error
	Append(name, section, content string) error
	Delete(name string) error
	HasConsolidated() bool
	ReadConsolidated() (string, error)
}

// SessionStoreAPI provides session persistence operations for commands.
type SessionStoreAPI interface {
	ListSessions() ([]SessionInfo, error)
	LoadSession(name string) ([]agentic.OutputEvent, error)
	SaveCurrent(name string) error
	DeleteSession(name string) error
	// SessionID returns the active session ID, or empty if none.
	SessionID() string
	// CurrentSessionPath returns the filesystem path to the active session
	// JSONL file, or empty if no session is active.
	CurrentSessionPath() string
	// ImportSession copies a JSONL file into the sessions directory under
	// the given name. The sourcePath must be a valid JSONL file. Returns
	// an error if a session with that name already exists.
	ImportSession(name, sourcePath string) error
}

// DocsProvider provides access to embedded documentation.
type DocsProvider interface {
	// List returns all available documentation files.
	List() ([]DocInfo, error)
	// Get returns the content of a documentation file by name.
	Get(name string) (string, error)
	// FindDocFile finds a doc file matching the given query.
	FindDocFile(query string) (DocInfo, error)
}

// DocInfo describes a single documentation file.
type DocInfo struct {
	Name        string
	File        string
	Path        string
	Description string
}

// Context holds the runtime context for command execution. It provides
// access to all subsystem references that commands need to operate.
//
// Commands should depend on the narrowest role interface they need rather
// than the full Context. Context implements CommandEnv, UIHost, and
// SessionEnv, and individual helpers should accept only the role they use.
type Context struct {
	// Config is the current active configuration.
	Config *config.Config

	// ProjectDir is the current working directory / project root.
	ProjectDir string

	// ConfigSaver persists configuration changes.
	ConfigSaver config.ConfigSaver

	// AgentManager controls the agent lifecycle.
	AgentManager *AgentManager

	// ExecutionController manages confirmation, review queue, and permission rules.
	ExecutionController *ExecutionController

	// ModeRegistry provides access to registered modes and skills.
	ModeRegistry *ModeRegistry

	// EventBus is the typed event bus used by commands to emit TUI events.
	EventBus *event.Bus

	// SelectOptionFunc is an optional callback for interactive selection.
	// Commands that need the user to pick from options call this.
	// The onSelected callback is invoked with the result when the user
	// confirms or cancels. This is async-friendly: the caller returns
	// immediately and onSelected runs later.
	SelectOptionFunc func(title string, options []tui.SelectorItem, current string, onSelected func(selected string, ok bool))

	// ShowInputFunc is an optional callback for a single-line text prompt.
	// The onSubmit callback is invoked with the result when the user confirms
	// or cancels. This is async-friendly: the caller returns immediately and
	// onSubmit runs later.
	ShowInputFunc func(prompt, current string, onSubmit func(value string, ok bool))

	// RequestMainInput asks the host to capture the next user input from the
	// main input line. The onSubmit callback receives the typed text. Used by
	// /export to collect an issue description without opening a modal prompt.
	RequestMainInput func(prompt string, onSubmit func(text string))

	// WorktreeManager manages git worktrees for sandboxed operations.
	WorktreeManager *internal.WorktreeManager

	// PipelineRunner executes multi-agent pipeline stages.
	PipelineRunner *multiagent.PipelineRunner

	// ActivePipelineRun tracks the currently executing pipeline, if any.
	ActivePipelineRun *multiagent.PipelineRun

	// ForegroundOrchestrator manages multi-agent workflows in the foreground.
	// Commands like /pair and /reviewer use this for user-visible orchestration.
	ForegroundOrchestrator *multiagent.ForegroundOrchestrator

	// AgentPool creates and caches sub-agents for the Agent tool and workflows.
	AgentPool *multiagent.AgentPool

	// GoalManager manages coding goals.
	GoalManager *GoalManager

	// CronManager manages scheduled agent tasks.
	CronManager *CronManager

	// WorkflowRegistry holds loaded workflows (built-in + user-defined).
	WorkflowRegistry *multiagent.WorkflowRegistry

	// AssistantText holds the last assistant message text for /copy.
	// Populated by handleSlashCommand before executing the command.
	AssistantText string

	// SubmitToAgent is an optional callback that submits the given text to
	// the main agent as a user message, displayed in the chat viewport.
	// Used by commands like /skill:run to forward expanded skill content
	// to the LLM for inline execution.
	SubmitToAgent func(text string)

	// RenderChat is an optional callback that returns the complete rendered
	// chat content (ANSI-stripped) for inclusion in exported bundles.
	RenderChat func(width int) string

	// PTYManager manages pseudo-terminal sessions for interactive commands.
	// Used by /pty commands to list, read, write, and kill sessions.
	PTYManager PTYManager

	// ShowPTYOverlay is an optional callback that opens a PTY session viewer
	// overlay in the TUI. Used by /pty:monitor to attach to a running session.
	ShowPTYOverlay func(sessionID string)

	// The following fields are optional — they may be nil until the
	// corresponding chunks are implemented. They use typed interfaces
	// so commands can assert capabilities at compile time.

	// ToolRegistry provides tool lookup (populated by M03).
	ToolRegistry ToolRegistry

	// ToolFactory creates a configurable tool instance by name. Used by
	// /tools:name:on to register a tool at runtime without restarting.
	ToolFactory func(name string) (agentic.Tool, bool)

	// SkillRegistry provides skill lookup (populated by M08).
	SkillRegistry SkillRegistry

	// ProviderManager manages provider lifecycle (populated by M04).
	ProviderManager ProviderManager

	// ModelValidator exposes background validation results for configured models.
	ModelValidator ModelValidator

	// MemoryStore provides persistent memory operations (populated by M04).
	MemoryStore MemoryStore

	// SessionStore provides session persistence operations.
	SessionStore SessionStoreAPI

	// DocsProvider provides access to embedded documentation.
	DocsProvider DocsProvider

	// InitialActiveProvider and InitialActiveModel capture the model/provider
	// in effect when the application started. Used by /reset to restore the
	// starting point without reloading configuration.
	InitialActiveProvider string
	InitialActiveModel    string

	// ReloadHandler provides ability to reload skills, context files, and plugins.
	ReloadHandler ReloadHandler

	// OutputBuffer is set by Router.Execute before calling Command.Run.
	// Commands should write their response text here using Writef
	// instead of fmt.Printf/Println (which go to stdout and are invisible
	// in the TUI). The router returns this text as the command output.
	OutputBuffer *strings.Builder
}

// Compile-time assertions that Context implements the three role interfaces.
var (
	_ CommandEnv   = (*Context)(nil)
	_ UIHost       = (*Context)(nil)
	_ SessionEnv   = (*Context)(nil)
	_ OutputWriter = (*Context)(nil)
)

// OutputWriter is the minimal interface for writing command output.
// Helpers that only need to emit text should depend on this instead of
// the full Context.
type OutputWriter interface {
	// Writef writes formatted output to the command's output buffer, or falls
	// back to stdout if no buffer is set.
	Writef(format string, args ...interface{})
}

// CommandEnv is the base role interface available to every command.
// It provides output writing and event emission.
type CommandEnv interface {
	OutputWriter
	EventSink
}

// UIHost is the role interface for commands that drive interactive UI widgets.
type UIHost interface {
	CommandEnv
	Selector
	InputPrompter
}

// SessionEnv is the role interface for commands that need session state.
type SessionEnv interface {
	CommandEnv
	ModeController
	SessionRecorder
	SystemPromptProvider
}

// SystemPromptProvider gives access to the assembled system prompt for the
// current profile, mode, and skill context. This is used by transparency
// commands like /prompt and the side panel.
type SystemPromptProvider interface {
	SystemPrompt() string
}

// PTYManager provides access to pseudo-terminal sessions for the /pty command.
type PTYManager interface {
	List() []internal.PTYSessionInfo
	Stop(id string) error
	Read(id string, tail int) (string, error)
	Write(id, input string) error
}

// ReloadHandler is the interface for runtime reloading of resources.
type ReloadHandler interface {
	// ReloadSkills re-scans all skill directories and re-loads the registry.
	// Returns count of loaded skills and any error.
	ReloadSkills() (int, error)

	// ReloadContext re-scans for AGENTS.md files and reloads context.
	// Returns count of loaded context files and any error.
	ReloadContext() (int, error)

	// ReloadPlugins re-scans plugin directories and re-loads plugins.
	ReloadPlugins() error
}

// EventSink sends UI events to the application event bus.
// Commands use this to flash messages, refresh the footer, request control
// actions, or update the chat viewport.
type EventSink interface {
	// Flash sends a transient flash message to the chat/status area.
	Flash(text string)
	// FooterRefresh requests a full footer rebuild from the current config.
	FooterRefresh()
	// StopRequest asks the application to exit cleanly.
	StopRequest()
	// ClearChat asks the chat viewport to be cleared.
	ClearChat()
	// NewSession asks the application to stop the current agent session and
	// start a fresh one, clearing both the chat viewport and all statistics.
	NewSession()
	// InterAgent sends an agent-to-agent message to the chat viewport.
	InterAgent(from, to, content string)
}

// Selector is the role interface for presenting an option picker.
type Selector interface {
	SelectOption(title string, options []tui.SelectorItem, current string, onSelected func(selected string, ok bool))
}

// InputPrompter is the role interface for presenting a single-line input prompt.
type InputPrompter interface {
	ShowInput(prompt, current string, onSubmit func(value string, ok bool))
}

// ModeController exposes mode state operations used by commands.
type ModeController interface {
	CurrentMode() internal.ModeState
	SetMode(internal.ModeState) internal.ModeState
	PushMode(internal.ModeState, string) internal.ModeState
	PopMode() internal.ModeState
	SetThinkingLevel(string) error
	GetThinkingLevel() string
}

// SessionRecorder exposes completed-turn history used by commands.
type SessionRecorder interface {
	TurnHistory() []TurnRecord
	LastTurn() *TurnRecord
}

// Writef writes formatted output to the command's output buffer, or falls
// back to stdout if no buffer is set (e.g., in tests). Commands should use
// this instead of fmt.Printf/Println so their output appears in the TUI.
func (c Context) Writef(format string, args ...interface{}) {
	if c.OutputBuffer != nil {
		fmt.Fprintf(c.OutputBuffer, format, args...)
	} else {
		fmt.Printf(format, args...)
	}
}

// SelectOption shows an interactive picker and invokes onSelected with the result.
// The onSelected callback receives ("", false) if the user cancels or no
// callback is configured. This is non-blocking — the caller should return
// immediately after calling SelectOption; the action is applied in onSelected.
func (c Context) SelectOption(title string, options []tui.SelectorItem, current string, onSelected func(selected string, ok bool)) {
	if c.SelectOptionFunc != nil {
		c.SelectOptionFunc(title, options, current, onSelected)
	} else if onSelected != nil {
		onSelected("", false)
	}
}

// ShowInput shows a single-line input prompt and invokes onSubmit with the result.
// The onSubmit callback receives ("", false) if the user cancels or no
// callback is configured. This is non-blocking — the caller should return
// immediately after calling ShowInput; the action is applied in onSubmit.
func (c Context) ShowInput(prompt, current string, onSubmit func(value string, ok bool)) {
	if c.ShowInputFunc != nil {
		c.ShowInputFunc(prompt, current, onSubmit)
	} else if onSubmit != nil {
		onSubmit("", false)
	}
}

// Flash sends a transient flash message to the chat/status area.
// It is part of the EventSink interface.
func (c Context) Flash(text string) {
	if c.EventBus == nil {
		return
	}
	select {
	case c.EventBus.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
	default:
	}
}

// FooterRefresh requests a full footer rebuild from the current config.
func (c Context) FooterRefresh() {
	if c.EventBus == nil {
		return
	}
	select {
	case c.EventBus.Footer <- event.FooterEvent{FooterRefresh: true}:
	default:
	}
}

// StopRequest asks the application to exit cleanly.
func (c Context) StopRequest() {
	if c.EventBus == nil {
		return
	}
	select {
	case c.EventBus.Control <- event.ControlEvent{StopRequest: true}:
	default:
	}
}

// ClearChat asks the chat viewport to be cleared.
func (c Context) ClearChat() {
	if c.EventBus == nil {
		return
	}
	select {
	case c.EventBus.Chat <- event.ChatEvent{ClearChat: true}:
	default:
	}
}

// NewSession asks the application to stop the current agent session and
// start a fresh one, clearing both the chat viewport and all statistics.
func (c Context) NewSession() {
	if c.EventBus == nil {
		return
	}
	select {
	case c.EventBus.Control <- event.ControlEvent{NewSession: true}:
	default:
	}
}

// InterAgent sends an agent-to-agent message to the chat viewport.
func (c Context) InterAgent(from, to, content string) {
	if c.EventBus == nil {
		return
	}
	select {
	case c.EventBus.Chat <- event.ChatEvent{InterAgent: &event.InterAgent{From: from, To: to, Content: content}}:
	default:
	}
}

// ControlEvent sends a control event on the event bus.
func (c Context) ControlEvent(ev event.ControlEvent) {
	if c.EventBus == nil {
		return
	}
	select {
	case c.EventBus.Control <- ev:
	default:
	}
}

// CurrentMode returns the current mode state via AgentManager.
func (c Context) CurrentMode() internal.ModeState {
	if c.AgentManager == nil {
		return internal.ModeState{}
	}
	return c.AgentManager.CurrentMode()
}

// SetMode replaces the current mode via AgentManager.
func (c Context) SetMode(ms internal.ModeState) internal.ModeState {
	if c.AgentManager == nil {
		return ms
	}
	return c.AgentManager.SetMode(ms)
}

// PushMode saves current and activates a new mode via AgentManager.
func (c Context) PushMode(ms internal.ModeState, source string) internal.ModeState {
	if c.AgentManager == nil {
		return ms
	}
	return c.AgentManager.PushMode(ms, source)
}

// PopMode restores the previous mode via AgentManager.
func (c Context) PopMode() internal.ModeState {
	if c.AgentManager == nil {
		return internal.ModeState{}
	}
	return c.AgentManager.PopMode()
}

// SetThinkingLevel sets the reasoning effort level via AgentManager.
func (c Context) SetThinkingLevel(level string) error {
	if c.AgentManager == nil {
		return nil
	}
	return c.AgentManager.SetThinkingLevel(level)
}

// GetThinkingLevel returns the current thinking level via AgentManager.
func (c Context) GetThinkingLevel() string {
	if c.AgentManager == nil {
		return ""
	}
	return c.AgentManager.GetThinkingLevel()
}

// TurnHistory returns completed turn records via AgentManager.
func (c Context) TurnHistory() []TurnRecord {
	if c.AgentManager == nil {
		return nil
	}
	return c.AgentManager.TurnHistory()
}

// LastTurn returns the most recent completed turn via AgentManager.
func (c Context) LastTurn() *TurnRecord {
	if c.AgentManager == nil {
		return nil
	}
	return c.AgentManager.LastTurn()
}

// SystemPrompt returns the assembled system prompt for the current session
// via AgentManager. This implements SystemPromptProvider.
func (c Context) SystemPrompt() string {
	if c.AgentManager == nil {
		return ""
	}
	return c.AgentManager.SystemPrompt()
}
