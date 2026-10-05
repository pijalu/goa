// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import "time"

// DefaultSessionRetentionDays is how long a session log is kept when no layer
// states otherwise. A session is the whole transcript of a run, so the window is
// generous enough to recover last week's work and short enough that a project's
// .goa/sessions cannot grow without bound.
const DefaultSessionRetentionDays = 7

// SessionsConfig controls the durable session log directory
// (<project>/.goa/sessions/*.jsonl).
type SessionsConfig struct {
	Retention SessionsRetentionConfig `yaml:"retention,omitempty"`
}

// SessionsRetentionConfig controls how long session logs are kept.
//
// Both fields are tri-state on purpose. The embedded default is "on, 7 days",
// and a higher layer must be able to say "no" — which a plain bool cannot do
// through the config cascade, where a zero value means "inherit" (bugs.md B15
// and the plain-struct retention flags it was written against).
//
//	enabled unset (nil) → inherit (default: on)
//	enabled false       → keep every session log forever
//	days unset (nil)    → inherit (default: 7)
//	days 0              → keep every session log forever
//	days N              → keep N days, counted from the file's last write
type SessionsRetentionConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"`
	Days    *int  `yaml:"days,omitempty"`
}

// SessionWindow resolves the configured session-log retention: the window
// duration and whether pruning is on at all. Retention counts from a file's last
// write, so a session still being appended to is never a candidate.
func (c SessionsConfig) SessionWindow() (time.Duration, bool) {
	if c.Retention.Enabled != nil && !*c.Retention.Enabled {
		return 0, false
	}
	days := DefaultSessionRetentionDays
	if c.Retention.Days != nil {
		days = *c.Retention.Days
	}
	if days <= 0 {
		// An explicit 0 (or a negative, which has no meaning for a window)
		// means "keep every session log".
		return 0, false
	}
	return time.Duration(days) * 24 * time.Hour, true
}
