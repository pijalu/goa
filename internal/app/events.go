// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"log"
	"runtime/debug"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

func (a *App) setupEventHandlers(engine *tui.TUI, chat *tui.ChatViewport, inp *tui.Editor) chan struct{} {
	inp.SetOnSubmit(a.makeSubmitHandler(engine, chat))
	inp.OnImagePaste = func(path string) {
		a.handlePastedImage(engine, chat, path)
	}
	done := make(chan struct{})

	bus := a.subs.events
	go a.runAgentEventReader(done, bus.Agent)
	go a.runControlEventReader(done, bus.Control)
	go a.runChatEventReader(done, bus.Chat)
	go a.runFooterEventReader(done, bus.Footer)
	go a.runGitRefreshLoop(done, gitRefreshInterval)
	go a.runPeakRefreshLoop(done, peakRefreshInterval)

	// Forward foreground orchestrator events to the TUI event bus, so that
	// companion post-turn output and other orchestrator-managed workflows
	// show agent-colored messages in the chat viewport.
	if a.subs.foregroundOrch != nil {
		go a.runOrchestratorEventForwarder(done)
	}
	// Forward pipeline runner events once, centrally. Per-command consumers
	// would compete for the same channel and lose events round-robin.
	if a.subs.pipelineRunner != nil {
		go a.runPipelineEventForwarder(done)
	}
	// Persistent multi-agent run view: shows the tabbed (Stats + per-agent +
	// All) view for the active orchestration run, updated on the commandLoop
	// (R1 single-owner invariant). Unlike the old overlay it stays after finish.
	if a.subs.orchActive != nil {
		go a.runOrchestratorViewForwarder(done)
	}

	go func() {
		// Block until either the engine stops (via Ctrl+C) or done is
		// externally closed — whichever happens first. The select prevents
		// busy-polling (see Bug #1 in TOFIX.md).
		select {
		case <-engine.Stopped():
		case <-done:
		}
		// If done was already closed by someone else, don't close it again.
		select {
		case <-done:
		default:
			close(done)
		}
	}()
	return done
}

// apply routes a state-mutating function through the TUI commandLoop (the sole
// state owner in the Actor model). If no TUI engine is attached (headless /
// tests), it runs inline. All event handlers that mutate TUI components must
// go through apply so the commandLoop stays the sole mutator.
func (a *App) apply(fn func()) {
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.Apply(fn)
		return
	}
	fn()
}

func (a *App) runAgentEventReader(done chan struct{}, ch <-chan event.AgentEvent) {
	runWithPanicRestart(readerMaxRestarts,
		func(r any, stack []byte) {
			log.Printf("[events] runAgentEventReader panicked: %v\n%s", r, stack)
			// Recover from rendering panics so the agent event loop survives.
			// Without this, a single bad render kills all agent output delivery.
			a.showPanicError("render", r, stack)
		},
		func() {
			log.Printf("[events] runAgentEventReader exceeded %d consecutive restarts; stopping", readerMaxRestarts)
			a.showPanicError("render",
				fmt.Errorf("render loop repeatedly panicked (%d consecutive times)", readerMaxRestarts),
				debug.Stack())
		},
		func() {
			for {
				select {
				case <-done:
					return
				case ev, ok := <-ch:
					if !ok {
						return
					}
					a.apply(func() {
						a.handleAgentOutputEvent(&ev.Event)
						if ev.GoalUpdate != nil {
							a.handleGoalUpdate(ev.GoalUpdate)
						}
					})
				}
			}
		})
}

func (a *App) runControlEventReader(done chan struct{}, ch <-chan event.ControlEvent) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			a.apply(func() {
				if a.handleControlEvent(ev) {
					return
				}
			})
		}
	}
}

func (a *App) runChatEventReader(done chan struct{}, ch <-chan event.ChatEvent) {
	runWithPanicRestart(readerMaxRestarts,
		func(r any, stack []byte) {
			log.Printf("[events] runChatEventReader panicked: %v\n%s", r, stack)
			a.showPanicError("chat", r, stack)
		},
		func() {
			log.Printf("[events] runChatEventReader exceeded %d consecutive restarts; stopping", readerMaxRestarts)
			a.showPanicError("chat",
				fmt.Errorf("chat loop repeatedly panicked (%d consecutive times)", readerMaxRestarts),
				debug.Stack())
		},
		func() {
			for {
				select {
				case <-done:
					return
				case ev, ok := <-ch:
					if !ok {
						return
					}
					a.apply(func() {
						a.handleChatEvent(ev)
					})
				}
			}
		})
}

func (a *App) runFooterEventReader(done chan struct{}, ch <-chan event.FooterEvent) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			a.apply(func() {
				a.handleFooterEvent(ev)
			})
		}
	}
}

// gitRefreshInterval is how often the footer re-polls the workdir's git state
// so branch switches or commits done outside goa show up without a restart.
const gitRefreshInterval = 2 * time.Second

// peakRefreshInterval is how often the footer re-checks the provider peak
// window coloring so the red/orange/green transitions appear at window edges
// even when no other event forces a redraw.
const peakRefreshInterval = 30 * time.Second

// runGitRefreshLoop periodically refreshes the footer's git branch/dirty
// state. Gathering spawns git subprocesses, so it runs off the commandLoop;
// the result is applied on the loop only when it actually changed.
//
// subs.footer and subs.projectDir are written before this goroutine starts
// and never reassigned, so reading them here is race-free.
func (a *App) runGitRefreshLoop(done chan struct{}, interval time.Duration) {
	footer := a.subs.footer
	if footer == nil || a.subs.projectDir == "" {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			a.refreshFooterGitOnce(footer, a.subs.projectDir)
		}
	}
}

// refreshFooterGitOnce gathers the git state for dir off the commandLoop and
// applies it to the footer on the loop, requesting a render only when the
// displayed state changed.
func (a *App) refreshFooterGitOnce(footer *tui.Footer, dir string) {
	info := tui.GatherGitInfo(dir)
	a.apply(func() {
		d := footer.Data()
		if d.GitBranch == info.Branch && d.GitDirty == info.Dirty && d.GitConflicts == info.Conflicts {
			return
		}
		footer.SetGitInfo(info)
		if a.subs.tuiEngine != nil {
			a.subs.tuiEngine.RequestRender()
		}
	})
}

// runPeakRefreshLoop periodically requests a footer re-render so the provider
// peak indicator stays current. It is a no-op (no render request) when the
// active provider has no peak windows, so sessions on other providers never
// tick. The render itself re-evaluates time.Now() and is differential, so a
// request with unchanged peak color costs one no-op frame.
func (a *App) runPeakRefreshLoop(done chan struct{}, interval time.Duration) {
	footer := a.subs.footer
	if footer == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if !a.activeProviderHasPeak() {
				continue
			}
			a.apply(func() {
				if a.subs.tuiEngine != nil {
					a.subs.tuiEngine.RequestRender()
				}
			})
		}
	}
}

// activeProviderHasPeak reports whether the active provider is one of the
// catalog entries with peak-pricing windows, i.e. the footer peak indicator
// can actually change color over time.
func (a *App) activeProviderHasPeak() bool {
	if a.subs == nil || a.subs.cfg == nil {
		return false
	}
	def := schema.LookupProviderDefByID(a.subs.cfg.ActiveProvider)
	return def != nil && len(def.PeakHours) > 0
}
