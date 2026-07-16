// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/acp"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// App owns the application's runtime state and event routing.
// It wraps the initialized subsystems and holds the mutable session state
// (streaming context, token counters, etc.) that was previously stored in
// package-level variables in main.go.
type App struct {
	subs *subsystems

	// streamCtx tracks the currently active streaming block (thinking or
	// assistant content). It is owned by the event-forwarder goroutine.
	stream streamState

	// statsMu protects the mutable session statistics below.
	statsMu sync.Mutex

	// sessionActive tracks whether the agent is currently processing a turn.
	sessionActive bool

	// Token tracking for conversation statistics in footer.
	tokenPromptTotal     int
	tokenPredictedTotal  int
	tokenCacheReadTotal  int
	tokenCacheWriteTotal int
	tokenSessionMax      int
	tokenSessionMaxAuto  bool
	tokenSessionEstimate int

	// Last-turn tracking.
	lastTurnPromptN    int
	lastTurnPredictedN int
	lastTurnCacheRead  int
	lastTurnCacheWrite int
	lastTurnSpeed      float64
	turnCount          int

	// Compression counters for the footer.
	microCompacts int
	compacts      int

	// Previous cache hit percentage for cache-hit evolution tracking.
	// Used to color the CH% footer stat based on whether the cache hit rate
	// is growing, dropping, or stable.
	prevCacheHitPct float64

	// Status tracking for granular footer/status messages.
	toolResultsSeen      int
	toolCallsTotal       int
	toolCallWarningLevel ToolCallLevel

	// pendingInput holds an active main-input request (a command or review
	// prompt reusing the main editor). It carries onSubmit AND onCancel so the
	// caller owns its own cancel/restore behavior — no separate
	// reviewOverlayRestore field is needed.
	pendingInput *inputRequest

	approvalStateFields
}

// inputRequest tracks a command-requested value from the main input line.
// onCancel (optional) runs when the request is cancelled (Ctrl+C), letting the
// caller restore prior state (e.g. re-show the review overlay).
type inputRequest struct {
	prompt   string
	onSubmit func(string)
	onCancel func()
}

// New creates an App from initialized subsystems.
func New(subs *subsystems) *App {
	a := &App{subs: subs}
	wireSwarmTool(a)
	return a
}

// Run starts the TUI, agent session, and event loop. It returns true if the
// application should relaunch (e.g., after the setup wizard writes config).
func (a *App) Run() bool {
	subs := a.subs
	cfg := subs.cfg
	projectDir := subs.projectDir

	initTheme(cfg)
	initSpinner(cfg)

	// Before building the TUI (terminal is in normal mode), prompt the user
	// about project-level skills if this is the first run.
	a.promptProjectTrustIfNeeded(projectDir)

	engine, chat, inp := a.buildTUI()
	// Attach the interactive clarify host callback now that the App exists.
	attachClarifyTool(subs.toolRegistry, func(title, summary, question string, options []string) (string, bool) {
		return a.clarify(tui.NewClarifyCard(title, summary, question, options))
	})
	a.wireToolConfirmation(engine)
	a.loadPersistedPathApprovals()
	showStartupBanner(subs, chat)
	if subs.perfLoad {
		a.startPerfLoad()
	} else {
		startAgentSession(subs, chat)
	}
	engine.RenderNow()

	done := a.setupEventHandlers(engine, chat, inp)
	engine.RunLoops() // launch the commandLoop (sole state owner) + renderLoop
	<-done
	a.saveInputHistory(inp)
	if subs.dreamScheduler != nil {
		subs.dreamScheduler.writeSchedulerState()
		subs.dreamScheduler.Stop()
	}
	engine.Stop()

	if subs.runWizard {
		fmt.Println("\n⟡  Launching setup wizard...")
		result, err := config.RunSetupWizard(projectDir, subs.loader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Wizard error: %v\n", err)
			return false
		}
		if result.Cancelled {
			fmt.Println("Setup cancelled.")
			return false
		}
		if result.ConfigWritten {
			fmt.Println("Configuration saved. Restarting goa...")
		} else {
			fmt.Println("Setup complete. Restarting goa...")
		}
		return true
	}
	return false
}

// Main is the top-level entry point used by cmd/goa. It parses CLI flags,
// loads config, initializes subsystems, and runs the application loop.
func Main() {
	defer handleShutdown()
	log.SetOutput(io.Discard)

	for {
		relaunch := runApp()
		if !relaunch {
			break
		}
	}
}

func runApp() bool {
	projectDir := MustGetwd()
	cliFlags, runtimeOpts := ParseCLIFlags()
	if err := runtimeOpts.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	runtimeOpts = applyProfilingDefaults(runtimeOpts)
	prof, err := startProfiling(runtimeOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer prof.stopProfiling()

	loader := config.NewCascadeLoader(projectDir, cliFlags["config"], cliFlags)
	cfg := LoadConfig(loader, projectDir)
	subs := InitSubsystems(cfg, loader, projectDir, runtimeOpts)
	switch {
	case runtimeOpts.DreamMode():
		runDream(subs, runtimeOpts)
		return false
	case runtimeOpts.ACP:
		runACP(subs)
		return false
	case runtimeOpts.CheckUpdate:
		runUpdateCheck(subs, runtimeOpts)
		return false
	case runtimeOpts.ExportOutput != "" || runtimeOpts.ExportSession != "" || runtimeOpts.IncludeGlobalLog:
		runExport(subs, exportOptionsFromRuntime(runtimeOpts))
		return false
	case runtimeOpts.Headless():
		runHeadless(subs, runtimeOpts)
		return false
	default:
		return New(subs).Run()
	}
}

// applyProfilingDefaults fills in default profile file names when the user
// passes --with-profiling without explicit --cpuprofile / --memprofile / --trace.
func applyProfilingDefaults(opts RuntimeOptions) RuntimeOptions {
	if !opts.WithProfiling {
		return opts
	}
	if opts.CPUProfile == "" {
		opts.CPUProfile = "cpu.prof"
	}
	if opts.MemProfile == "" {
		opts.MemProfile = "mem.prof"
	}
	if opts.TraceFile == "" {
		opts.TraceFile = "trace.out"
	}
	return opts
}

func runACP(subs *subsystems) {
	factory := func(sessionID string) acp.AgentDriver {
		return newACPAgentDriver(subs, sessionID)
	}
	server := acp.NewACPServerWithDriver(os.Stdin, os.Stdout, factory)
	server.Start()
}

func runUpdateCheck(subs *subsystems, opts RuntimeOptions) {
	checkForUpdates(subs, opts.Telemetry)
}

func handleShutdown() {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "Panic: %v\n\n%s\n", r, debug.Stack())
		os.Exit(1)
	}
}

// requestMainInput asks the user to type a value on the main input line.
// The next non-empty submit will invoke onSubmit with the typed text.
// The prompt is shown as the input editor's bordered title (the single cue).
func (a *App) requestMainInput(prompt string, onSubmit func(string)) {
	a.requestMainInputWithCancel(prompt, onSubmit, nil)
}

// requestMainInputWithCancel registers a main-input request with a cancel
// handler. onCancel runs if the user cancels (Ctrl+C), letting the caller
// restore prior UI state. This is the single mechanism — review overlay
// restore is expressed as an onCancel, not a separate field.
//
// The prompt is shown in exactly ONE place: the input editor's bordered title
// ("┨ <prompt> ┠"), right where the user types. It is NOT also emitted as a
// chat system message or a separate pending-input line — those duplicated the
// instruction up to three times (a boxed note, a plain line, and the title).
// The editor is always rendered (even on orchestrator tabs), so the title
// alone is a sufficient, persistent cue. `silent` is retained for callers but
// no longer adds a chat message.
func (a *App) requestMainInputWithCancel(prompt string, onSubmit func(string), onCancel func()) {
	a.pendingInput = &inputRequest{prompt: prompt, onSubmit: onSubmit, onCancel: onCancel}
	// Single source of truth for the prompt: the input editor's bordered title.
	if inp := a.subs.getInput(); inp != nil {
		inp.SetTitle(prompt)
	}
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

// clarify renders a ClarifyCard in the conversation and blocks until the user
// answers on the main input line. It is the host backend for the
// ask_user_question tool (core.Context.ClarifyFunc). Because tool execution
// happens off the commandLoop, ALL state mutations (card append, pendingInput
// registration, title set) are routed through app.apply so the commandLoop
// remains the sole mutator. The blocking happens here on the tool goroutine.
func (a *App) clarify(card *tui.ClarifyCard) (string, bool) {
	type result struct {
		text string
		ok   bool
	}
	resCh := make(chan result, 1)

	a.apply(func() {
		if a.subs.chat != nil {
			a.subs.chat.AddClarifyCard(card)
		}
		prompt := card.Question()
		if t := card.Title(); t != "" {
			if prompt == "" {
				prompt = t
			} else {
				prompt = t + ": " + prompt
			}
		}
		// Seed the editor empty so the previous message text doesn't linger.
		if inp := a.subs.getInput(); inp != nil {
			inp.SetText("")
		}
		a.requestMainInputWithCancel(prompt, func(text string) {
			resCh <- result{text, true}
		}, func() {
			resCh <- result{"", false}
		})
		if a.subs.tuiEngine != nil {
			a.subs.tuiEngine.RequestRender()
		}
	})

	r := <-resCh
	return r.text, r.ok
}

// clearMainInputRequest clears any pending main-input request and restores the
// input editor title. Called after the value is consumed or the request is
// cancelled.
func (a *App) clearMainInputRequest() {
	a.pendingInput = nil
	if a.subs != nil {
		// Restore the input title for the active context (steer prompt during
		// orchestration, empty otherwise). pendingInput is already nil so
		// updateOrchInputPrompt will not early-return.
		a.updateOrchInputPrompt()
	}
}

// cancelPendingMainInput cancels an active main-input request (e.g. a /goal
// objective prompt). Returns true when a request was cancelled so the caller
// (Ctrl+C handler) can suppress the quit. The request's own onCancel (if any)
// runs, so callers own their restore behavior.
func (a *App) cancelPendingMainInput() bool {
	if a.pendingInput == nil {
		return false
	}
	onCancel := a.pendingInput.onCancel
	a.clearMainInputRequest()
	if onCancel != nil {
		onCancel()
		return true
	}
	if a.subs != nil && a.subs.chat != nil {
		a.subs.chat.AddFlashMessage("⚡ Input cancelled.")
	}
	return true
}

// promptProjectTrustIfNeeded checks whether the user should be asked about
// trusting this project's skills. It scans .agents/skills/ for skill
// directories and, if any exist and the trust manager has never prompted,
// asks the user whether to trust all project skills. The result is persisted
// so the prompt is shown at most once.
//
// This is called before the TUI starts, while the terminal is still in
// normal (cooked) mode, so fmt.Print / bufio.Scanner work directly.
func (a *App) promptProjectTrustIfNeeded(projectDir string) {
	mgr := a.subs.trustMgr
	if mgr == nil {
		return
	}
	if !mgr.NeedProjectTrustPrompt() {
		return
	}

	// Check whether project skills actually exist.
	projectSkillsDir := filepath.Join(projectDir, ".agents", "skills")
	entries, err := os.ReadDir(projectSkillsDir)
	if err != nil || len(entries) == 0 {
		return // no project skills to trust
	}

	// Count skill directories (subdirs that contain SKILL.md).
	skillCount := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(projectSkillsDir, e.Name(), "SKILL.md")); err == nil {
			skillCount++
		}
	}
	if skillCount == 0 {
		return
	}

	fmt.Printf("\n⟡  This project has %d skill(s) in .agents/skills/.\n", skillCount)
	fmt.Print("   Trust them? [y/N] ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	trusted := answer == "y" || answer == "yes"

	if err := mgr.SetProjectTrustPrompted(trusted); err != nil {
		log.Printf("Warning: failed to persist trust decision: %v\n", err)
	}

	if trusted {
		fmt.Println("   ✓ Project skills trusted. Loading...")
		// Reload the skill registry so project skills are picked up now that
		// the default trust decision is set to trusted.
		a.reloadSkills()
	} else {
		fmt.Println("   Skills not trusted. Use /trust allow <name> to trust individual skills.")
	}
	fmt.Println()
}

// reloadSkills rebuilds the skill registry so that newly trusted project
// skills are discovered. This is called after the user grants trust.
func (a *App) reloadSkills() {
	projectDir := a.subs.projectDir
	cfg := a.subs.cfg
	trustMgr := a.subs.trustMgr

	skillDirs := append(config.DefaultSkillDirs(projectDir), cfg.Skills.Dirs...)
	reg := skills.NewSkillRegistry(skillDirs)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	reg.SetTrustChecker(newSkillTrustChecker(trustMgr))
	if err := reg.LoadAll(); err != nil {
		log.Printf("Warning: failed to reload skills after trust: %v\n", err)
		return
	}
	a.subs.skillRegistry = reg

	// Register /<skillname> shortcuts for the newly loaded project skills.
	if warnings := commands.RegisterSkillShortcuts(a.subs.registry, reg); len(warnings) > 0 {
		for _, w := range warnings {
			log.Printf("Warning: %s\n", w)
		}
	}
	log.Printf("Reloaded %d skills after project trust decision\n", len(reg.List()))
}
