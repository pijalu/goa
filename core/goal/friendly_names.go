// SPDX-License-Identifier: GPL-3.0-or-later
package goal

// Friendly goal-name resolution for display surfaces (bugs.md 2026-08-26):
// the stats cache view must label per-goal sections with the user-friendly
// alias (e.g. "cheery.swan") instead of the opaque "goal-<hex>" ID. The
// alias lives in the goal event log (create records carry both fields), so
// this file distills that log into a plain ID→name map any renderer can
// consume without importing goal internals.

// FriendlyNamesFromEventLog distills raw event-log records into a
// goal-ID → friendly-alias map. Create records carry both the ID and its
// name; a later record naming the same ID wins (last write), so replayed
// sessions keep their newest alias across create/clear/re-create cycles.
// Records without an ID or an empty name are ignored.
func FriendlyNamesFromEventLog(records []GoalEventRecord) map[string]string {
	out := make(map[string]string, len(records))
	for _, r := range records {
		if r.GoalID == nil || r.Name == nil || *r.Name == "" {
			continue
		}
		out[*r.GoalID] = *r.Name
	}
	return out
}

// GoalFriendlyNames resolves every goal ID this session ever recorded —
// active, completed, or cancelled — to its friendly alias for display
// surfaces like /stats:cache. A store-less mode or an unreadable log yields
// an empty mapping: callers fall back to opaque IDs rather than failing the
// report.
func (m *GoalMode) GoalFriendlyNames() map[string]string {
	records, err := m.EventLog()
	if err != nil {
		return nil
	}
	return FriendlyNamesFromEventLog(records)
}
