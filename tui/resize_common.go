// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"time"

	"golang.org/x/term"
)

// Windows console input-record event types (Win32 INPUT_RECORD.EventType,
// wincon.h). Declared here, platform-neutral, so the record-filtering logic
// can be unit-tested on every OS with synthetic buffers.
const (
	winEventKey              uint16 = 0x0001 // KEY_EVENT
	winEventMouse            uint16 = 0x0002 // MOUSE_EVENT
	winEventWindowBufferSize uint16 = 0x0004 // WINDOW_BUFFER_SIZE_EVENT
	winEventMenu             uint16 = 0x0008 // MENU_EVENT
	winEventFocus            uint16 = 0x0010 // FOCUS_EVENT
)

// inputRecord is the minimal mirror of the Win32 INPUT_RECORD structure the
// resize pipeline needs: only EventType is inspected. Platform glue converts
// raw console records into this neutral shape so filtering stays testable
// without a Windows host.
type inputRecord struct {
	eventType uint16
}

// isResizeRecord reports whether r carries a WINDOW_BUFFER_SIZE_EVENT, i.e.
// whether the console screen buffer changed dimensions.
func isResizeRecord(r inputRecord) bool {
	return r.eventType == winEventWindowBufferSize
}

// filterResizeRecords reports whether any record in buf signals a console
// buffer-size change. Every other record kind belongs to other consumers of
// the console input queue (keys, mouse, focus, menus) and is ignored.
func filterResizeRecords(buf []inputRecord) bool {
	for _, r := range buf {
		if isResizeRecord(r) {
			return true
		}
	}
	return false
}

const (
	// resizeChannelBuffer sizes the resizeEvents channel. The consumer
	// contract is unchanged from the original poller: buffered channel,
	// non-blocking sends that coalesce resize bursts into at most this many
	// pending notifications.
	resizeChannelBuffer = 4

	// resizeFallbackInterval is the legacy polling cadence, kept verbatim as
	// the automatic fallback whenever event-driven detection is unavailable
	// (not a console handle, console API failure, flag rejection, ...).
	resizeFallbackInterval = 250 * time.Millisecond
)

// sendResize delivers one resize notification without ever blocking: if the
// buffer is full the event is coalesced away — a busy consumer re-reads the
// size on its next wake-up anyway, so dropping duplicates loses nothing.
func sendResize(out chan<- struct{}) {
	select {
	case out <- struct{}{}:
	default:
	}
}

// resizeWatcher abstracts an event-driven source of terminal resizes
// (SIGWINCH on Unix, console input events on Windows).
type resizeWatcher interface {
	// watch emits into out until done is closed. Implementations must use
	// sendResize (never block on out) and must return once done is closed.
	watch(out chan<- struct{}, done <-chan struct{})
}

// newResizeWatcher resolves the platform watcher; returning nil means "event
// mode unavailable" and makes resizeEvents fall back to polling. Indirected
// through a variable so tests can pin either mode (cf. stdinFile precedent).
var newResizeWatcher = newPlatformResizeWatcher

// consoleSizeFunc indirection lets tests script size sequences without a
// real terminal, exactly like the production callers use consoleSize.
var consoleSizeFunc = consoleSize

// consoleSize returns the current terminal dimensions via x/term, which maps
// to GetConsoleScreenBufferInfo on Windows and TIOCGWINSZ on Unix. Failures
// degrade to the classic 80x24 default.
func consoleSize() (int, int) {
	w, h, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return 80, 24
	}
	return w, h
}

// runResizePoller drives the legacy size-polling loop: every interval it
// compares the current size against the last seen one and emits a single
// notification on change. It returns when done is closed. Kept byte-for-byte
// compatible with the original ticker semantics; used as the automatic
// fallback when event mode is unavailable.
func runResizePoller(
	out chan<- struct{},
	done <-chan struct{},
	size func() (int, int),
	interval time.Duration,
) {
	lastW, lastH := size()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			w, h := size()
			if w != lastW || h != lastH {
				lastW, lastH = w, h
				sendResize(out)
			}
		}
	}
}

// resizeEvents returns a channel that fires when the terminal size changes.
// The preferred source is the platform event watcher (instant detection);
// when event mode is unavailable it transparently falls back to the legacy
// 250ms poller. Contract for consumers (unchanged): buffered channel of
// resizeChannelBuffer slots, non-blocking sends, stops emitting after done
// is closed.
func resizeEvents(done <-chan struct{}) <-chan struct{} {
	out := make(chan struct{}, resizeChannelBuffer)
	if w := newResizeWatcher(); w != nil {
		go w.watch(out, done)
		return out
	}
	go runResizePoller(out, done, consoleSizeFunc, resizeFallbackInterval)
	return out
}
