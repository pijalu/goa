// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package usage records cumulative LLM token usage into a global SQLite
// database (~/.goa/usage.db) so the /usage command can report stats across
// projects, providers, and models — like opencode-stats.
package usage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Record is one token-usage observation (a single model turn).
type Record struct {
	Project    string
	Provider   string
	Model      string
	PromptN    int
	PredictedN int
	CacheRead  int
	CacheWrite int
	At         time.Time
}

// Stat is an aggregated usage row for one grouping dimension value.
type Stat struct {
	Key        string
	Turns      int
	PromptN    int
	PredictedN int
	CacheRead  int
	CacheWrite int
}

// Total returns prompt+predicted tokens (the headline number).
func (s Stat) Total() int { return s.PromptN + s.PredictedN }

// Store is a SQLite-backed usage recorder/querier.
type Store struct {
	db *sql.DB
}

// schema is intentionally a single denormalized events table; aggregations
// are done with GROUP BY at query time (volume is low — one row per turn).
const schema = `
CREATE TABLE IF NOT EXISTS usage_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	provider TEXT NOT NULL,
	model TEXT NOT NULL,
	prompt INTEGER NOT NULL DEFAULT 0,
	predicted INTEGER NOT NULL DEFAULT 0,
	cache_read INTEGER NOT NULL DEFAULT 0,
	cache_write INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_usage_project ON usage_events(project);
CREATE INDEX IF NOT EXISTS idx_usage_provider ON usage_events(provider);
CREATE INDEX IF NOT EXISTS idx_usage_model ON usage_events(model);
`

// Open opens (creating if needed) the usage store at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("usage: mkdir %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("usage: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("usage: schema: %w", err)
	}
	return &Store{db: db}, nil
}

// DefaultPath returns ~/.goa/usage.db.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("usage: home dir: %w", err)
	}
	return filepath.Join(home, ".goa", "usage.db"), nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Add records one usage event.
func (s *Store) Add(r Record) error {
	at := r.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO usage_events (project, provider, model, prompt, predicted, cache_read, cache_write, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		r.Project, r.Provider, r.Model, r.PromptN, r.PredictedN, r.CacheRead, r.CacheWrite, at.Unix(),
	)
	return err
}

// Dimension selects the grouping for an aggregation query.
type Dimension int

const (
	// ByProject groups usage by project directory.
	ByProject Dimension = iota
	// ByProvider groups usage by provider id.
	ByProvider
	// ByModel groups usage by model id.
	ByModel
)

func (d Dimension) column() string {
	switch d {
	case ByProvider:
		return "provider"
	case ByModel:
		return "model"
	default:
		return "project"
	}
}

// Query aggregates usage grouped by dim. project != "" filters to one project
// (for per-project views); empty means global across all projects. since
// restricts to events recorded at or after that time; zero means all time.
func (s *Store) Query(dim Dimension, project string, since time.Time) ([]Stat, error) {
	where, args := usageWhere(project, since)
	q := `SELECT ` + dim.column() + `, COUNT(*), COALESCE(SUM(prompt),0), COALESCE(SUM(predicted),0),
		COALESCE(SUM(cache_read),0), COALESCE(SUM(cache_write),0)
		FROM usage_events` + where + ` GROUP BY ` + dim.column() +
		` ORDER BY (COALESCE(SUM(prompt),0)+COALESCE(SUM(predicted),0)) DESC`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stat
	for rows.Next() {
		var st Stat
		if err := rows.Scan(&st.Key, &st.Turns, &st.PromptN, &st.PredictedN, &st.CacheRead, &st.CacheWrite); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Sum returns the grand total across an optional project/time filter.
func (s *Store) Sum(project string, since time.Time) (Stat, error) {
	where, args := usageWhere(project, since)
	q := `SELECT COUNT(*), COALESCE(SUM(prompt),0), COALESCE(SUM(predicted),0),
		COALESCE(SUM(cache_read),0), COALESCE(SUM(cache_write),0) FROM usage_events` + where
	var st Stat
	st.Key = "total"
	err := s.db.QueryRow(q, args...).Scan(&st.Turns, &st.PromptN, &st.PredictedN, &st.CacheRead, &st.CacheWrite)
	return st, err
}

// usageWhere builds the WHERE clause shared by all aggregations: an optional
// project equality filter and an optional created_at lower bound.
func usageWhere(project string, since time.Time) (string, []any) {
	var clauses []string
	var args []any
	if project != "" {
		clauses = append(clauses, `project = ?`)
		args = append(args, project)
	}
	if !since.IsZero() {
		clauses = append(clauses, `created_at >= ?`)
		args = append(args, since.Unix())
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return ` WHERE ` + strings.Join(clauses, ` AND `), args
}

// DayCount is one UTC calendar day of activity.
type DayCount struct {
	Day    time.Time // UTC midnight
	Turns  int
	Tokens int // prompt + predicted
}

// DailyCounts returns per-day activity for the last n days ending today
// (UTC), oldest first. Days with no events are included with zero values so
// heatmap rendering needs no gap handling. n <= 0 defaults to 365.
func (s *Store) DailyCounts(project string, days int) ([]DayCount, error) {
	if days <= 0 {
		days = 365
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := today.AddDate(0, 0, -(days - 1))

	where, args := usageWhere(project, start)
	q := `SELECT (created_at / 86400) * 86400, COUNT(*),
		COALESCE(SUM(prompt),0) + COALESCE(SUM(predicted),0)
		FROM usage_events` + where + ` GROUP BY created_at / 86400`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[int64]DayCount{}
	for rows.Next() {
		var unixDay, turns, tokens int64
		if err := rows.Scan(&unixDay, &turns, &tokens); err != nil {
			return nil, err
		}
		byDay[unixDay] = DayCount{
			Day:    time.Unix(unixDay, 0).UTC(),
			Turns:  int(turns),
			Tokens: int(tokens),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DayCount, 0, days)
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		if dc, ok := byDay[d.Unix()]; ok {
			out = append(out, dc)
		} else {
			out = append(out, DayCount{Day: d})
		}
	}
	return out, nil
}
