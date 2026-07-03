// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/sessiontree"
	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/internal/telemetry"
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/internal/update"
)

// CommandDependencies holds the runtime dependencies needed by commands.
// Passing a zero value is allowed for tests; commands that need dependencies
// will return an error if called without them.
type CommandDependencies struct {
	GoalCommand     *GoalCommand
	AuthStore       *auth.Store
	SessionTree     *sessiontree.Manager
	ThemeStore      *config.ThemeStore
	UpdateChecker   *update.Checker
	TelemetryClient *telemetry.Client
	TrustManager    *trust.Manager
	SwarmState      *swarm.State
}

// RegisterAll registers all built-in slash commands into the given registry.
// Call this once at application startup instead of relying on init()-time
// global registration.
func RegisterAll(r *core.CommandRegistry, deps ...CommandDependencies) error {
	var dep CommandDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	cmds := []core.Command{
		// autonomy
		&AutonomyCommand{},
		// companion
		&CompanionToggleCommand{},
		// compress
		&CompressCommand{},
		// config
		&ConfigCommand{},
		&SetupCommand{},
		// copy
		&CopyCommand{},
		// docs
		&DocsCommand{},
		&ToolsDocCommand{},
		// dream
		&DreamCommand{},
		// execution
		&StopCommand{},
		&RetryCommand{},
		&UndoCommand{},
		// memory
		&MemoryCommand{},
		// meta
		&GoaCommand{},
		&VersionCommand{},
		&DebugCommand{},
		// mode
		&ModeCommand{},
		// model
		&ModelCommand{},
		// multiagent
		&PipelineCommand{},
		&GoCommand{},
		// orchestrate
		&OrchestrateCommand{},
		// profile
		&ProfileCommand{},
		// pair / reviewer workflows
		&PairCommand{},
		&ReviewerCommand{},
		// provider
		&ProviderCommand{},
		// pty
		&PTYCommand{},
		// reload
		&ReloadCommand{},
		// review
		&ReviewCommand{},
		// session_persist
		&SessionCommand{},
		// new — shortcut for /session new
		&NewCommand{},
		// help
		&HelpCommand{Registry: r},
		&HotkeysCommand{},
		&QuitCommand{},
		// skills
		&SkillsCommand{},
		// thinking_blocks
		&ThinkingBlocksCommand{},
		// thinking
		&ThinkingCommand{},
		// transparency
		&ExchangeCommand{},
		&PromptCommand{},
		&StatsCommand{},
		// ui
		&UICommand{},
		// workflows
		&WorkflowsCommand{},
		// export
		&ExportCommand{},
		// swarm
		&SwarmCommand{State: dep.SwarmState},
		// login / logout
		&LoginCommand{Store: dep.AuthStore},
		&LogoutCommand{Store: dep.AuthStore},
		// trust
		&TrustCommand{Manager: dep.TrustManager},
		// session tree
		&TreeCommand{Manager: dep.SessionTree},
		&ForkCommand{Manager: dep.SessionTree},
		&CloneCommand{Manager: dep.SessionTree},
		// theme
		&ThemeCommand{Store: dep.ThemeStore},
		// update
		&UpdateCommand{Checker: dep.UpdateChecker},
		// telemetry
		&TelemetryCommand{Client: dep.TelemetryClient},
		// permission
		&PermissionCommand{},
	}
	for _, cmd := range cmds {
		if err := r.Register(cmd); err != nil {
			return err
		}
	}
	if dep.GoalCommand != nil {
		if err := r.Register(dep.GoalCommand); err != nil {
			return err
		}
	}
	return nil
}
