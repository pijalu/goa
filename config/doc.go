// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package config implements the Goa configuration system with a cascade
// loader that merges defaults, home, project, local configs, env vars, and
// CLI flags. It also provides theme loading, first-run detection, and a
// ConfigSaver for persisting runtime changes.
package config
