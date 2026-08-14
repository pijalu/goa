// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

// TestActiveProviderHasPeak verifies the peak-refresh gate: only catalog
// providers with peak-pricing windows trigger the periodic footer refresh.
func TestActiveProviderHasPeak(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"deepseek has peak windows", &config.Config{ActiveProvider: "deepseek"}, true},
		{"zai has peak windows", &config.Config{ActiveProvider: "zai"}, true},
		{"zai-api has peak windows", &config.Config{ActiveProvider: "zai-api"}, true},
		{"google has no peak windows", &config.Config{ActiveProvider: "google"}, false},
		{"empty provider has no peak windows", &config.Config{ActiveProvider: ""}, false},
		{"nil cfg is not peak", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{subs: &subsystems{cfg: tc.cfg}}
			if got := a.activeProviderHasPeak(); got != tc.want {
				t.Errorf("activeProviderHasPeak() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRunPeakRefreshLoop_Lifecycle: the loop ticks and exits on done without
// leaking the goroutine, for both peak and non-peak providers.
func TestRunPeakRefreshLoop_Lifecycle(t *testing.T) {
	for _, provider := range []string{"deepseek", "google"} {
		t.Run(provider, func(t *testing.T) {
			a := &App{}
			a.subs = &subsystems{
				cfg:    &config.Config{ActiveProvider: provider},
				footer: tui.NewFooter(),
			}

			done := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.runPeakRefreshLoop(done, 5*time.Millisecond)
			}()

			// Several ticks; no engine attached so peak ticks are no-ops
			// (RequestRender is guarded), but the loop must stay alive.
			time.Sleep(60 * time.Millisecond)
			close(done)

			waitCh := make(chan struct{})
			go func() { wg.Wait(); close(waitCh) }()
			select {
			case <-waitCh:
			case <-time.After(2 * time.Second):
				t.Fatal("runPeakRefreshLoop did not exit after done was closed")
			}
		})
	}
}

// TestRunPeakRefreshLoop_NoFooter: headless runs (no footer) must exit
// immediately instead of spinning.
func TestRunPeakRefreshLoop_NoFooter(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{cfg: &config.Config{ActiveProvider: "deepseek"}}

	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		a.runPeakRefreshLoop(done, time.Millisecond)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("loop without footer should return immediately")
	}
	close(done)
}
