// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package spinner provides spinner animation definitions loaded from an
// embedded spinners.json (cli-spinners format) or from ~/.goa/spinner.json
// if the user provides one. It is used by the TUI to animate status
// indicators and busy spinners.
package spinner

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed spinners.json
var builtinFS embed.FS

// Definition describes one spinner animation.
type Definition struct {
	Interval int      `json:"interval"` // milliseconds between frames
	Frames   []string `json:"frames"`   // animation frames
}

// IntervalMS returns the spinner interval in milliseconds (defaults to 80).
func (d Definition) IntervalMS() int {
	if d.Interval <= 0 {
		return 80
	}
	return d.Interval
}

// Width returns the maximum visual width of any frame.
func (d Definition) Width() int {
	max := 0
	for _, f := range d.Frames {
		if len(f) > max {
			max = len(f)
		}
	}
	return max
}

var (
	registry map[string]Definition
	once     sync.Once
)

// All returns all spinner definitions keyed by name.
func All() map[string]Definition {
	once.Do(load)
	return registry
}

// Get returns a spinner definition by name, or false if not found.
func Get(name string) (Definition, bool) {
	once.Do(load)
	d, ok := registry[name]
	return d, ok
}

// Names returns all spinner names in sorted order.
func Names() []string {
	once.Do(load)
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}

// Default returns the name and definition of the default spinner. Hexagon is
// the default (bugs.md "Hexagon spinner as default"); arc remains the
// fallback if hexagon is unavailable (e.g. a user spinner.json without it).
func Default() (string, Definition) {
	once.Do(load)
	if d, ok := registry["hexagon"]; ok {
		return "hexagon", d
	}
	if d, ok := registry["arc"]; ok {
		return "arc", d
	}
	for n, d := range registry {
		return n, d
	}
	return "", Definition{}
}

func load() {
	// Try user-provided ~/.goa/spinner.json first
	home, _ := os.UserHomeDir()
	if home != "" {
		userPath := filepath.Join(home, ".goa", "spinner.json")
		if data, err := os.ReadFile(userPath); err == nil {
			registry = make(map[string]Definition)
			if err := json.Unmarshal(data, &registry); err == nil && len(registry) > 0 {
				return
			}
		}
	}

	// Fall back to embedded spinners.json
	data, err := builtinFS.ReadFile("spinners.json")
	if err != nil {
		registry = fallback()
		fmt.Printf("spinner: failed to read embedded spinners.json: %v\n", err)
		return
	}
	registry = make(map[string]Definition)
	if err := json.Unmarshal(data, &registry); err != nil {
		registry = fallback()
		fmt.Printf("spinner: failed to parse spinners.json: %v\n", err)
	}
}

func fallback() map[string]Definition {
	return map[string]Definition{
		"arc": {Interval: 100, Frames: []string{"◜", "◠", "◝", "◞", "◡", "◟"}},
	}
}
