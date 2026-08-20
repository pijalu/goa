// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatcherDebounce is how long a burst of filesystem events (a save typically
// emits create/write/rename in quick succession, e.g. an editor's atomic
// temp-file + rename dance) is coalesced into a single reload.
const WatcherDebounce = 100 * time.Millisecond

// WatcherDirRescan is how often the watcher re-checks for writable cascade
// directories that did not exist at startup (e.g. a project that gains a
// .goa/config.yaml mid-session). fsnotify can only watch existing dirs, so a
// late-created layer is picked up at the next rescan.
const WatcherDirRescan = 2 * time.Second

// ConfigWatcher watches the writable layers of the config cascade (home
// ~/.goa/config.yaml, project .goa/config.yaml, project .goa/config.local.yaml,
// or the explicit --config file) for external edits and hot-publishes reloaded
// configs. Invalid edits keep the last good config and log a warning — the
// same "boot fails loud, reload keeps last-good" contract as the dsh
// settings-file provider.
//
// The watcher is shutdown-safe: Close is idempotent, stops the event loop
// goroutine, waits for it to exit, and closes the Changes channel so
// consumers can range to completion without leaking a goroutine.
type ConfigWatcher struct {
	cl   *CascadeLoader
	logf func(format string, args ...any)

	fsw  *fsnotify.Watcher
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup

	// debounce coalesces a burst of events into one reload.
	debounce time.Duration
	// rescanInterval is how often the loop re-checks for late-created writable
	// cascade dirs.
	rescanInterval time.Duration
	// watched maps a watched directory to the base names of the writable
	// cascade files it contains. Events for other names are ignored.
	watched map[string]map[string]bool
	// content caches the on-disk content of each watched file at the last
	// successful reload (or initial snapshot). A watcher event whose content
	// equals the cache is a no-op — duplicate events and the loader's own
	// writes (which write identical bytes back) never trigger a reload.
	mu      sync.Mutex
	content map[string]string

	changes chan *Config
}

// NewConfigWatcher creates a watcher over the writable cascade layers known to
// cl. It does not start watching until Start is called. logf is used for
// warnings (may be nil to discard); the application passes log.Printf.
func NewConfigWatcher(cl *CascadeLoader, logf func(format string, args ...any)) (*ConfigWatcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &ConfigWatcher{
		cl:             cl,
		logf:           logf,
		fsw:            fsw,
		done:           make(chan struct{}),
		debounce:       WatcherDebounce,
		rescanInterval: WatcherDirRescan,
		watched:        map[string]map[string]bool{},
		content:        map[string]string{},
		changes:        make(chan *Config, 1),
	}

	paths := cl.WritableConfigPaths()
	for _, p := range paths {
		dir := filepath.Dir(p)
		base := filepath.Base(p)
		if w.watched[dir] == nil {
			w.watched[dir] = map[string]bool{}
		}
		w.watched[dir][base] = true

		// Snapshot the current content so the first event for an unchanged
		// file is a no-op, and so a missing writable layer (no project
		// .goa/config.yaml yet) is treated as "empty content".
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			if !os.IsNotExist(rerr) {
				w.warn("config watcher: read %s: %v", p, rerr)
			}
			w.content[p] = ""
			continue
		}
		w.content[p] = string(data)
	}
	w.ensureWatchedDirs()
	return w, nil
}

// ensureWatchedDirs adds fsnotify watches for every writable cascade directory
// that exists. fsnotify cannot watch a missing directory, so late-created
// layers are picked up by the loop's periodic rescan.
func (w *ConfigWatcher) ensureWatchedDirs() {
	for dir := range w.watched {
		if _, err := os.Stat(dir); err != nil {
			continue // still missing — retry at the next rescan
		}
		if err := w.fsw.Add(dir); err != nil {
			w.warn("config watcher: watch %s: %v", dir, err)
		}
	}
}

// Changes returns the channel of reloaded configs. It is closed by Close, so
// consumers can range over it until the watcher is shut down.
func (w *ConfigWatcher) Changes() <-chan *Config {
	return w.changes
}

// Start launches the event-loop goroutine.
func (w *ConfigWatcher) Start() {
	w.wg.Add(1)
	go w.loop()
}

// Close stops the watcher and waits for the event-loop goroutine to exit. It
// is idempotent.
func (w *ConfigWatcher) Close() {
	w.once.Do(func() {
		close(w.done)
		_ = w.fsw.Close()
		w.wg.Wait()
		close(w.changes)
	})
}

// isRelevant reports whether a filesystem event touches one of the watched
// writable cascade files.
func (w *ConfigWatcher) isRelevant(ev fsnotify.Event) bool {
	bases, ok := w.watched[filepath.Dir(ev.Name)]
	if !ok {
		return false
	}
	return bases[filepath.Base(ev.Name)]
}

// loop runs until Close. It debounces bursts of relevant events into a single
// reload, rescans for late-created writable dirs, and exits cleanly when the
// watcher is closed (no goroutine leak).
func (w *ConfigWatcher) loop() {
	defer w.wg.Done()
	var timer *time.Timer
	var timerC <-chan time.Time
	rescan := time.NewTicker(w.rescanInterval)
	defer rescan.Stop()
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			timer, timerC = w.debounceEvent(timer, timerC, ev)
		case <-timerC:
			timer = nil
			timerC = nil
			w.reload()
		case <-rescan.C:
			w.ensureWatchedDirs()
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.warn("config watcher: %v", err)
		case <-w.done:
			return
		}
	}
}

// debounceEvent coalesces a burst of relevant events into one pending reload:
// the first event arms a timer, later events reset it so the reload only fires
// once the write settles. Events for unrelated files leave the state unchanged.
func (w *ConfigWatcher) debounceEvent(timer *time.Timer, timerC <-chan time.Time, ev fsnotify.Event) (*time.Timer, <-chan time.Time) {
	if !w.isRelevant(ev) {
		return timer, timerC
	}
	if timer == nil {
		t := time.NewTimer(w.debounce)
		return t, t.C
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(w.debounce)
	return timer, timer.C
}

// reload re-reads the writable layers and, when content changed since the last
// successful reload, reloads the cascade. A broken edit is logged and the last
// good config is kept (nothing is published); the next fix triggers a reload.
func (w *ConfigWatcher) reload() {
	current := w.readContents()
	if w.unchanged(current) {
		return
	}

	cfg, err := w.cl.Reload()
	if err != nil {
		w.warn("config hot-reload failed, keeping last-good config: %v", err)
		return
	}

	w.mu.Lock()
	for p, c := range current {
		w.content[p] = c
	}
	w.mu.Unlock()

	select {
	case w.changes <- cfg:
	default:
		// A consumer that has not drained yet will see the latest config on
		// its next read; dropping the older pointer is safe.
	}
}

// readContents reads every watched file. A missing file is recorded as empty
// content, so deletion of a writable layer publishes the cascade without it.
func (w *ConfigWatcher) readContents() map[string]string {
	paths := w.cl.WritableConfigPaths()
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			out[p] = ""
			continue
		}
		out[p] = string(data)
	}
	return out
}

// unchanged reports whether every watched file still holds the content observed
// at the last successful reload. Duplicate events and the loader's own writes
// (identical bytes) are no-ops.
func (w *ConfigWatcher) unchanged(current map[string]string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for p, c := range current {
		if w.content[p] != c {
			return false
		}
	}
	return true
}

func (w *ConfigWatcher) warn(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
	}
}
