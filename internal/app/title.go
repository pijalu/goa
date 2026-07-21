// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"sync"
	"time"

	"github.com/pijalu/goa/internal/spinner"
)

// titlePhase identifies the title-controller lifecycle stage.
type titlePhase int

const (
	// titlePhaseStartup shows the early boot brand ("g⬡a") until the
	// startup-done hook fires (or the fallback deadline elapses).
	titlePhaseStartup titlePhase = iota
	// titlePhaseTransition plays the one-shot startup transition animation
	// (g⬡a → g⬡ → ⬡) at a slow frame rate.
	titlePhaseTransition
	// titlePhaseNormal is steady state: the base title when idle, the spinner
	// animation while the agent is working.
	titlePhaseNormal
)

// startupTitleFrames is the ordered transition sequence played once startup
// completes (bugs.md "Title bar startup sequence"): g⬡a → g⬡ → ⬡.
var startupTitleFrames = []string{"g⬡a", "g⬡", titleBrand}

// titleStartupTransitionInterval is the (deliberately slow) frame rate of the
// startup transition animation — 1s per the feature request.
const titleStartupTransitionInterval = time.Second

// titleController is the single writer for the terminal window title.
//
// Lifecycle: the boot title "g⬡a" is set as early as possible; when the
// startup-done hook fires (explicit signal after async plugin/history load,
// or the fallback deadline — whichever first), a one-shot transition
// animation plays at 1s/frame (g⬡a → g⬡ → ⬡); afterwards the controller
// settles into normal mode where it shows the base title while idle and the
// spinner animation while the agent works (when animated titles are enabled).
//
// Concurrency (bugs.md "goa should not be frozen until completion of the
// title animation"): title writes NEVER run on the caller's goroutine (in
// particular never on the TUI commandLoop, which drives input handling). All
// writes are enqueued on a buffered, latest-wins channel consumed by a single
// dedicated writer goroutine; a full channel drops the stale pending frame
// rather than blocking, so a slow terminal can never stall input or the
// animation ticker.
type titleController struct {
	frames    []string     // working-animation frames (empty = no animation)
	interval  time.Duration
	animated  bool // animated-title config (false = always static)

	mu      sync.Mutex
	phase   titlePhase
	base    string // current static title (contextual, e.g. "⬡ - project")
	working bool
	frame   int
	stopCh  chan struct{} // closed to halt the animation goroutine
	stopped bool

	// writes carries pending title strings to the writer goroutine. Capacity 1
	// plus latest-wins drop keeps only the freshest title queued.
	writes chan string
	writeCh chan<- string // sink handed to enqueue (writes)
}

// newTitleController builds a controller. set is the title sink invoked on a
// dedicated writer goroutine (TUI.SetTitle in production); def provides the
// working-animation frames and interval; animated mirrors the
// tui.animated_title config.
func newTitleController(set func(string), def spinner.Definition, animated bool) *titleController {
	writes := make(chan string, 1)
	tc := &titleController{
		frames:   def.Frames,
		interval: time.Duration(def.IntervalMS()) * time.Millisecond,
		animated: animated,
		phase:    titlePhaseStartup,
		base:     startupTitleFrames[0],
		writes:   writes,
		writeCh:  writes,
	}
	tc.enqueue(startupTitleFrames[0]) // boot brand as early as possible
	go tc.writeLoop(set, writes)
	return tc
}

// writeLoop is the sole caller of the title sink. It drains the latest-wins
// channel until it is closed by stop().
func (tc *titleController) writeLoop(set func(string), ch <-chan string) {
	for title := range ch {
		set(title)
	}
}

// enqueue hands a title to the writer goroutine without ever blocking: when a
// title is already pending, the stale one is dropped in favor of the newest.
func (tc *titleController) enqueue(title string) {
	select {
	case tc.writeCh <- title:
	default:
		// A title is already queued; replace it with the freshest.
		select {
		case <-tc.writes:
		default:
		}
		select {
		case tc.writeCh <- title:
		default:
		}
	}
}

// set writes a title through the non-blocking queue. It replaces the direct
// sink call so no caller (commandLoop included) ever blocks on a terminal
// write.
func (tc *titleController) set(title string) {
	tc.enqueue(title)
}

// setBase updates the contextual (static) title shown when idle. During the
// startup phase the base is only recorded — the boot brand keeps priority
// until the transition plays.
func (tc *titleController) setBase(title string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.base = title
	if tc.phase == titlePhaseNormal && !tc.working {
		tc.set(title)
	}
}

// startupDone signals that the startup sequence completed (explicit hook
// after async plugin/history load, or the fallback deadline — callers fire
// whichever comes first; only the first call has an effect). It plays the
// one-shot transition animation, then settles into normal mode.
func (tc *titleController) startupDone() {
	tc.mu.Lock()
	if tc.phase != titlePhaseStartup {
		tc.mu.Unlock()
		return
	}
	tc.phase = titlePhaseTransition
	tc.mu.Unlock()

	// Play g⬡a → g⬡ → ⬡ at the slow transition rate, then settle.
	tc.playTransition()
}

// playTransition runs the startup transition synchronously on the caller's
// goroutine; callers invoke it from a dedicated goroutine. It is interruptible
// via stop().
func (tc *titleController) playTransition() {
	for _, frame := range startupTitleFrames[1:] {
		select {
		case <-tc.stopChan():
			return
		case <-time.After(titleStartupTransitionInterval):
		}
		tc.mu.Lock()
		if tc.phase != titlePhaseTransition {
			tc.mu.Unlock()
			return
		}
		tc.set(frame)
		tc.mu.Unlock()
	}
	tc.mu.Lock()
	tc.phase = titlePhaseNormal
	tc.mu.Unlock()
	tc.render()
}

// setWorking toggles the working animation (agent busy). In normal mode with
// animated titles enabled, the title spins with the configured frames; on
// idle it returns to the base title. Outside normal mode (startup/transition)
// the flag is recorded and applied when normal mode begins.
func (tc *titleController) setWorking(working bool) {
	tc.mu.Lock()
	tc.working = working
	tc.mu.Unlock()
	tc.render()
}

// render pushes the title appropriate for the current state to the sink.
// Working animation frames are driven by tick(); render covers the
// transitions (working→first frame, idle→base).
func (tc *titleController) render() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.phase != titlePhaseNormal {
		return
	}
	if tc.working && tc.animated && len(tc.frames) > 0 {
		tc.set(tc.frames[tc.frame%len(tc.frames)] + tc.suffix())
		return
	}
	tc.set(tc.base)
}

// tick advances the working-animation frame. Called on the spinner interval
// by the animation goroutine.
func (tc *titleController) tick() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.phase != titlePhaseNormal || !tc.working || !tc.animated || len(tc.frames) == 0 {
		return
	}
	tc.frame++
	tc.set(tc.frames[tc.frame%len(tc.frames)] + tc.suffix())
}

// suffix returns the contextual part appended after the animated spinner
// frame, preserving the " - <context>" of the base title (empty when the base
// is the bare brand).
func (tc *titleController) suffix() string {
	if len(tc.base) > len(titleBrand) {
		return tc.base[len(titleBrand):]
	}
	return ""
}

// run starts the animation ticker goroutine. It exits on stop().
func (tc *titleController) run() {
	tc.mu.Lock()
	if tc.stopCh != nil {
		tc.mu.Unlock()
		return
	}
	tc.stopCh = make(chan struct{})
	stop := tc.stopCh
	tc.mu.Unlock()

	go func() {
		ticker := time.NewTicker(tc.interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				tc.tick()
			}
		}
	}()
}

// stopChan returns the stop channel, allocating it if run() has not been
// called yet (so playTransition is interruptible even before run).
func (tc *titleController) stopChan() <-chan struct{} {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.stopCh == nil {
		tc.stopCh = make(chan struct{})
	}
	return tc.stopCh
}

// stop halts the animation goroutine, any in-flight transition, and the
// writer goroutine (by closing the latest-wins channel).
func (tc *titleController) stop() {
	tc.mu.Lock()
	if tc.stopped {
		tc.mu.Unlock()
		return
	}
	tc.stopped = true
	if tc.stopCh != nil {
		close(tc.stopCh)
	}
	writes := tc.writes
	tc.mu.Unlock()
	// Close outside the lock: writeLoop may be mid-set on a slow sink.
	close(writes)
}

// startupTitleFallback is the maximum time the boot brand stays up waiting
// for the explicit startup-done hook; the transition plays at this deadline
// even if async loads never signal (bugs.md: 5s fallback only).
const startupTitleFallback = 5 * time.Second

// setBaseTitle updates the contextual window title, routing through the title
// controller when present (single writer) so the working animation and the
// startup sequence never fight direct SetTitle callers.
func (a *App) setBaseTitle(title string) {
	if a.titleCtl != nil {
		a.titleCtl.setBase(title)
		return
	}
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.SetTitle(title)
	}
}

// startTitleStartupHook launches the startup-done watcher: it fires the title
// transition when BOTH async startup loads (plugins + input history) have
// completed, or when the 5s fallback deadline elapses — whichever comes first
// (bugs.md "Title bar startup sequence": explicit hook, 5s fallback only).
// Nil/disabled loads count as already done. No-op without a controller.
func (a *App) startTitleStartupHook() {
	if a.titleCtl == nil {
		return
	}
	pluginsDone := a.pluginsLoaded
	historyDone := a.historyLoadDone
	go func() {
		wait := func(ch <-chan struct{}) {
			if ch != nil {
				<-ch
			}
		}
		done := make(chan struct{})
		go func() {
			wait(pluginsDone)
			wait(historyDone)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(startupTitleFallback):
		}
		a.titleCtl.startupDone()
	}()
}
