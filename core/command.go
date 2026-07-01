// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package core implements the Goa command system, agent lifecycle, session
// persistence, loop detection, and the execution controller. This is the
// brain of Goa — every user interaction routes through this package.
package core

import (
	"fmt"
	"sort"
	"sync"
)

// Command is the interface implemented by all Goa commands (/help, /mode, etc.).
type Command interface {
	// Name returns the primary command name (without leading slash).
	Name() string

	// Aliases returns alternative names for this command.
	Aliases() []string

	// ShortHelp returns a one-line description (≤100 chars).
	ShortHelp() string

	// LongHelp returns a detailed description with examples (≤2000 chars).
	LongHelp() string

	// Run executes the command with the given arguments.
	Run(ctx Context, args []string) error
}

// InternalCommand is an optional interface commands implement to declare that
// their invocation should NOT be echoed into the chat viewport or forwarded
// to the LLM. Internal commands are purely in-process (e.g., opening the
// config wizard). When IsInternal returns true, handleSlashCommand skips the
// chat echo and the command is responsible for its own user feedback via
// status messages or flash notifications.
type InternalCommand interface {
	IsInternal() bool
}

// IsInternal reports whether a command should be hidden from the chat/LLM.
// Commands that do not implement InternalCommand are treated as visible.
func IsInternal(cmd Command) bool {
	ic, ok := cmd.(InternalCommand)
	return ok && ic.IsInternal()
}

// StatusProvider is an optional interface commands implement to customize the
// output of the "/<cmd>?" short-status suffix. When implemented, "?" returns
// the live status (e.g. current value) instead of the static ShortHelp text.
// "??" continues to return LongHelp for full documentation.
//
// This lets single commands cover both the "set" and "display" use cases
// (e.g. /provider? prints the active provider; /provider?? shows the help).
type StatusProvider interface {
	// Status returns a short human-readable snapshot of the command's state.
	// Receives the command Context so it can read runtime config. Returning
	// an empty string falls back to ShortHelp().
	Status(ctx Context) string
}

// ArgCompleter is an optional interface commands can implement to provide
// argument completion. Called when the user presses Tab after the command name.
// prefix is the raw text after the command name (e.g., for "/mode cod" it's "cod").
type ArgCompleter interface {
	// CompleteArgs returns completion candidates for the argument prefix.
	CompleteArgs(ctx Context, prefix string) []ArgCompletion
}

// ArgCompletion is a single argument completion candidate.
type ArgCompletion struct {
	Value       string
	Description string
}

// CommandRegistry manages the collection of registered commands.
// Commands are registered explicitly via Register or RegisterAll.
type CommandRegistry struct {
	commands map[string]Command // keyed by name
}

// NewCommandRegistry creates an empty registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]Command),
	}
}

// Register adds a command to the registry. Returns an error if a command with
// the same name is already registered.
func (r *CommandRegistry) Register(cmd Command) error {
	name := cmd.Name()
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command already registered: /%s", name)
	}
	r.commands[name] = cmd
	return nil
}

// RegisterSafe registers a command without returning an error on conflict.
// Returns true if the command was registered, false if a command with the
// same name already exists (caller should warn).
func (r *CommandRegistry) RegisterSafe(cmd Command) bool {
	return r.Register(cmd) == nil
}

// Resolve finds a command by name or alias. Returns the command and true if found.
func (r *CommandRegistry) Resolve(name string) (Command, bool) {
	if cmd, ok := r.commands[name]; ok {
		return cmd, ok
	}
	// Check aliases
	for _, cmd := range r.commands {
		for _, alias := range cmd.Aliases() {
			if alias == name {
				return cmd, true
			}
		}
	}
	return nil, false
}

// All returns all registered commands in a stable order (alphabetical by name).
func (r *CommandRegistry) All() []Command {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	// Sort for deterministic order
	sort.Strings(names)

	result := make([]Command, len(names))
	for i, name := range names {
		result[i] = r.commands[name]
	}
	return result
}

// defaultRegistry is the shared registry used by the deprecated global helpers
// below (GlobalRegistry/RegisterCommand/ResetGlobalRegistry). It is retained as
// a thin shim because several call sites in internal/app and cmd/goa still
// resolve it implicitly; those callers should be migrated to receive an
// explicit *CommandRegistry from app bootstrap instead (see SOLID-4 / W8).
//
// The shim is race-safe: all access goes through registryMu so that
// ResetGlobalRegistry's reassignment no longer races with concurrent readers
// under -race (the original SOLID-4 data race).
var (
	defaultRegistry = NewCommandRegistry()
	registryMu      sync.RWMutex
)

// Deprecated: Mutating a package-global registry hidden couples every package
// that links core. Prefer constructing a *CommandRegistry in app bootstrap and
// passing it explicitly. This helper remains only until internal/app and
// cmd/goa are migrated (tracked in W8).
func RegisterCommand(cmd Command) error {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return defaultRegistry.Register(cmd)
}

// Deprecated: Prefer an explicitly-injected *CommandRegistry. See RegisterCommand.
func GlobalRegistry() *CommandRegistry {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return defaultRegistry
}

// Deprecated: Test-only helper that reassigns the global registry. Prefer
// constructing independent *CommandRegistry values per test. See RegisterCommand.
func ResetGlobalRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	defaultRegistry = NewCommandRegistry()
}
