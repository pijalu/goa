// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// SessionStoreReader is the read-only session store surface used by the
// session query tools. *core.SessionStore satisfies it; the interface keeps
// the tools package decoupled from core (which transitively imports tools).
type SessionStoreReader interface {
	// SessionID returns the active session id, or "" when none is running.
	SessionID() string
	// ListSessionIDs returns all persisted session ids, newest first.
	ListSessionIDs() ([]string, error)
	// ScanSessionEvents streams the events of a session file in order. visit
	// receives the 1-based event seq (line number) and the parsed event.
	// The scan stops early when visit returns false.
	ScanSessionEvents(sessionID string, visit func(seq int, ev agentic.OutputEvent) bool) (int, error)
	// SessionModifiedTime returns the modification time of a persisted
	// session file, or false when the session does not exist.
	SessionModifiedTime(sessionID string) (time.Time, bool)
}

// Session query tool bounds. Results are deliberately capped so a single
// call cannot flood the model context.
const (
	// sessionSearchDefaultMax is the result cap when max_results is omitted.
	sessionSearchDefaultMax = 10
	// sessionSearchHardMax is the absolute result cap.
	sessionSearchHardMax = 50
	// sessionSearchSnippetBytes caps each hit's snippet.
	sessionSearchSnippetBytes = 400
	// sessionSearchOutputBytes caps the total rendered output.
	sessionSearchOutputBytes = 64 * 1024
	// sessionReadWindowMax caps before/after neighbor counts.
	sessionReadWindowMax = 20
	// sessionReadEventBytes caps the serialized target event JSON.
	sessionReadEventBytes = 32 * 1024
	// sessionReadNeighborBytes caps each neighbor summary line.
	sessionReadNeighborBytes = 300
	// sessionReadOutputBytes caps the total rendered output.
	sessionReadOutputBytes = 64 * 1024
	// sessionScanMaxEvents caps how many events are scanned per session
	// during a search, protecting against pathological session files.
	sessionScanMaxEvents = 200000
)

// ── session_search ──────────────────────────────────────────────────────────

// SessionSearchTool searches persisted session logs with a full-text query
// and returns the strongest matching event per session.
type SessionSearchTool struct {
	Store SessionStoreReader
}

type sessionSearchParams struct {
	Query      string   `json:"query"`
	SessionIDs []string `json:"session_ids,omitempty"`
	MaxResults int      `json:"max_results,omitempty"`
}

// Schema returns the tool schema for session_search.
func (t *SessionSearchTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "session_search",
		Description: "Full-text search over prior persisted sessions. Returns the strongest matching event per session with a snippet and the event's sequence number for session_event_read. Read-only: never modifies sessions.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Full-text query over prior session history (case-insensitive, all terms must match).",
				},
				"session_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional session ids to restrict the search to.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum sessions to return (default %d, hard cap %d).", sessionSearchDefaultMax, sessionSearchHardMax),
				},
			},
			"required": []string{"query"},
		},
	}
}

// Execute runs the search.
func (t *SessionSearchTool) Execute(input string) (string, error) {
	p, err := parseSessionSearchParams(input)
	if err != nil {
		return "", err
	}
	terms, err := parseSearchQuery(p.Query)
	if err != nil {
		return "", &internal.ToolError{
			Tool: "session_search", Type: "invalid_query",
			Detail:   err.Error(),
			HintText: "Provide a non-empty, whitespace-separated query.",
		}
	}

	ids, err := t.Store.ListSessionIDs()
	if err != nil {
		return "", &internal.ToolError{
			Tool: "session_search", Type: "store_error",
			Detail:   fmt.Sprintf("Cannot list sessions: %v", err),
			HintText: "Retry; if the problem persists the session store may be corrupted.",
		}
	}

	ids = filterSessionIDs(ids, p.SessionIDs)
	maxResults := effectiveMaxResults(p.MaxResults)
	hits := t.collectSearchHits(ids, terms)

	// Sort by score desc, then by modification time desc (newest first).
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Created.After(hits[j].Created)
	})

	if len(hits) > maxResults {
		hits = hits[:maxResults]
		return renderSessionSearch(hits, true), nil
	}

	return renderSessionSearch(hits, false), nil
}

// parseSessionSearchParams decodes the tool input.
func parseSessionSearchParams(input string) (sessionSearchParams, error) {
	var p sessionSearchParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, &internal.ToolError{
			Tool: "session_search", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide a JSON object with a non-empty query string.",
		}
	}
	return p, nil
}

// effectiveMaxResults clamps the requested result cap into the valid range.
func effectiveMaxResults(requested int) int {
	if requested <= 0 {
		return sessionSearchDefaultMax
	}
	if requested > sessionSearchHardMax {
		return sessionSearchHardMax
	}
	return requested
}

// filterSessionIDs restricts ids to the explicit session_ids list when
// supplied, preserving order.
func filterSessionIDs(ids, wantIDs []string) []string {
	if len(wantIDs) == 0 {
		return ids
	}
	want := make(map[string]bool, len(wantIDs))
	for _, id := range wantIDs {
		want[id] = true
	}
	filtered := ids[:0]
	for _, id := range ids {
		if want[id] {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// collectSearchHits searches every id (except the current session) and
// returns the strongest matching event per session.
func (t *SessionSearchTool) collectSearchHits(ids []string, terms []string) []sessionSearchHit {
	current := t.Store.SessionID()
	hits := make([]sessionSearchHit, 0)
	for _, id := range ids {
		if id == current {
			// Search prior sessions only; the current session's content is
			// already in context.
			continue
		}
		if hit, ok := t.searchSession(id, terms); ok {
			hits = append(hits, hit)
		}
	}
	return hits
}

type sessionSearchHit struct {
	SessionID string
	Seq       int
	Type      agentic.EventType
	Role      agentic.Role
	Snippet   string
	Score     int
	Created   time.Time
}

// searchSession scans one session file for the strongest matching event.
func (t *SessionSearchTool) searchSession(sessionID string, terms []string) (sessionSearchHit, bool) {
	best := sessionSearchHit{SessionID: sessionID}
	found := false
	created, _ := t.Store.SessionModifiedTime(sessionID)

	t.Store.ScanSessionEvents(sessionID, func(seq int, ev agentic.OutputEvent) bool {
		if seq > sessionScanMaxEvents {
			return false
		}
		score := scoreEvent(ev, terms)
		if score > 0 && (!found || score > best.Score) {
			best.Seq = seq
			best.Type = ev.Type
			best.Role = ev.Role
			best.Snippet = eventSnippet(ev, terms)
			best.Score = score
			found = true
		}
		return true
	})
	if found {
		best.Created = created
	}
	return best, found
}

// parseSearchQuery normalizes a query into unique lowercase terms.
func parseSearchQuery(q string) ([]string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, fmt.Errorf("query must contain non-whitespace text")
	}
	if strings.ContainsRune(q, '\x00') {
		return nil, fmt.Errorf("query must not contain NUL")
	}
	fields := strings.Fields(strings.ToLower(q))
	terms := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			terms = append(terms, f)
		}
	}
	return terms, nil
}

// eventSearchText is the searchable text of an event.
func eventSearchText(ev agentic.OutputEvent) string {
	parts := []string{string(ev.Role), ev.Text, ev.ToolName, ev.ToolInput, ev.ToolResult}
	return strings.Join(parts, " ")
}

// scoreEvent returns the number of query term occurrences in an event when
// every term is present, or 0 when the event does not match all terms.
func scoreEvent(ev agentic.OutputEvent, terms []string) int {
	text := strings.ToLower(eventSearchText(ev))
	score := 0
	for _, term := range terms {
		n := strings.Count(text, term)
		if n == 0 {
			return 0
		}
		score += n
	}
	return score
}

// eventSnippet extracts a bounded window around the first matching term.
func eventSnippet(ev agentic.OutputEvent, terms []string) string {
	text := eventSearchText(ev)
	lower := strings.ToLower(text)
	first := -1
	for _, term := range terms {
		if idx := strings.Index(lower, term); idx >= 0 && (first < 0 || idx < first) {
			first = idx
		}
	}
	if first < 0 {
		return truncateBytes(text, sessionSearchSnippetBytes)
	}
	// Convert the match position to a rune index, then take a rune window.
	// Byte positions from the lowercased text can diverge from the original
	// for non-ASCII lowercasing, so slicing must happen on runes.
	runes := []rune(text)
	matchRune := runeLen(text[:first])
	startRune := matchRune - 80
	if startRune < 0 {
		startRune = 0
	}
	endRune := matchRune + 220
	if endRune > len(runes) {
		endRune = len(runes)
	}
	snippet := string(runes[startRune:endRune])
	if startRune > 0 {
		snippet = "…" + snippet
	}
	if endRune < len(runes) {
		snippet = snippet + "…"
	}
	return truncateBytes(snippet, sessionSearchSnippetBytes)
}

// runeLen counts the runes in a byte prefix.
func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

// renderSessionSearch formats the search result for the model.
func renderSessionSearch(hits []sessionSearchHit, capped bool) string {
	if len(hits) == 0 {
		return "No prior session matches found."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Session search results (%d):\n", len(hits))
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. Session %s — %s\n", i+1, hit.SessionID, hit.Created.Format(time.RFC3339))
		fmt.Fprintf(&b, "   Best match: seq %d | %s | %s\n", hit.Seq, hit.Type, hit.Role)
		fmt.Fprintf(&b, "   Snippet: %s\n", hit.Snippet)
	}
	if capped {
		b.WriteString("\nResult cap reached. Narrow the query or add filters to find additional matches.")
	}
	out := b.String()
	if len(out) > sessionSearchOutputBytes {
		return truncateBytes(out, sessionSearchOutputBytes) + "\n[output truncated]"
	}
	return out
}

// truncateBytes cuts a string at a byte budget without splitting a UTF-8
// rune, reserving space for the trailing ellipsis when truncation happened so
// the result never exceeds the budget.
func truncateBytes(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	cut := budget - 3
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// ── session_event_read ──────────────────────────────────────────────────────

// SessionEventReadTool reads one full event plus optional neighboring event
// summaries from a persisted session (bounded window).
type SessionEventReadTool struct {
	Store SessionStoreReader
}

type sessionEventReadParams struct {
	SessionID string `json:"session_id,omitempty"`
	Seq       int    `json:"seq"`
	Before    int    `json:"before,omitempty"`
	After     int    `json:"after,omitempty"`
}

// Schema returns the tool schema for session_event_read.
func (t *SessionEventReadTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "session_event_read",
		Description: "Read one full event and optional neighboring event summaries from a session. Omit session_id to read the current session. Read-only: never modifies sessions.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Target session id. Omit for the current session.",
				},
				"seq": map[string]any{
					"type":        "integer",
					"description": "Target event sequence number (1-based line number in the session file).",
				},
				"before": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Number of preceding raw events to summarize (default 0, max %d).", sessionReadWindowMax),
				},
				"after": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Number of following raw events to summarize (default 0, max %d).", sessionReadWindowMax),
				},
			},
			"required": []string{"seq"},
		},
	}
}

// Execute reads the event window.
func (t *SessionEventReadTool) Execute(input string) (string, error) {
	var p sessionEventReadParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "session_event_read", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide a JSON object with a numeric seq field.",
		}
	}
	if p.Seq <= 0 {
		return "", &internal.ToolError{
			Tool: "session_event_read", Type: "invalid_input",
			Detail:   "seq must be a positive integer (1-based event sequence number).",
			HintText: "Use the seq reported by session_search.",
		}
	}

	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = t.Store.SessionID()
		if sessionID == "" {
			return "", &internal.ToolError{
				Tool: "session_event_read", Type: "no_active_session",
				Detail:   "No active session and no session_id supplied.",
				HintText: "Supply a session_id from session_search results.",
			}
		}
	}

	if _, ok := t.Store.SessionModifiedTime(sessionID); !ok {
		return "", &internal.ToolError{
			Tool: "session_event_read", Type: "session_not_found",
			Detail:   fmt.Sprintf("Session %q does not exist.", sessionID),
			HintText: "List sessions with session_search or supply an existing session id.",
		}
	}

	before := p.Before
	if before < 0 {
		before = 0
	}
	if before > sessionReadWindowMax {
		before = sessionReadWindowMax
	}
	after := p.After
	if after < 0 {
		after = 0
	}
	if after > sessionReadWindowMax {
		after = sessionReadWindowMax
	}

	window := t.readWindow(sessionID, p.Seq, before, after)
	if !window.found {
		return "", &internal.ToolError{
			Tool: "session_event_read", Type: "event_not_found",
			Detail:   fmt.Sprintf("Session %q has no event at seq %d.", sessionID, p.Seq),
			HintText: "Check the seq reported by session_search; events are 1-based.",
		}
	}
	return renderEventRead(sessionID, window), nil
}

type sessionEventWindow struct {
	// target carries the full parsed event so the JSON body is unabridged.
	target agentic.OutputEvent
	targetSeq int
	before []sessionWindowEvent
	after  []sessionWindowEvent
	found  bool
}

type sessionWindowEvent struct {
	Seq  int
	Type agentic.EventType
	Role agentic.Role
	Text string
}

// readWindow scans the session once, collecting the target event and the
// requested before/after neighbors.
func (t *SessionEventReadTool) readWindow(sessionID string, seq, before, after int) sessionEventWindow {
	var w sessionEventWindow
	t.Store.ScanSessionEvents(sessionID, func(n int, ev agentic.OutputEvent) bool {
		if n < seq-before {
			return true
		}
		if n > seq+after {
			return false
		}
		switch {
		case n == seq:
			w.target = ev
			w.targetSeq = n
			w.found = true
		case n < seq:
			w.before = append(w.before, sessionWindowEvent{Seq: n, Type: ev.Type, Role: ev.Role, Text: ev.Text})
		default:
			w.after = append(w.after, sessionWindowEvent{Seq: n, Type: ev.Type, Role: ev.Role, Text: ev.Text})
		}
		return true
	})
	return w
}

// renderEventRead formats the window for the model. The target event is
// rendered as full JSON (bounded); neighbors are text summaries.
func renderEventRead(sessionID string, w sessionEventWindow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session %s\n", sessionID)
	fmt.Fprintf(&b, "Target event seq %d (%s, %s):\n", w.targetSeq, w.target.Type, w.target.Role)
	evJSON, _ := json.MarshalIndent(w.target, "", "  ")
	if len(evJSON) > sessionReadEventBytes {
		evJSON = []byte(truncateBytes(string(evJSON), sessionReadEventBytes))
	}
	b.WriteString("```json\n")
	b.Write(evJSON)
	b.WriteString("\n```")

	if len(w.before) > 0 {
		b.WriteString("\nBefore:")
		for _, e := range w.before {
			b.WriteString(renderNeighbor(e))
		}
	}
	if len(w.after) > 0 {
		b.WriteString("\nAfter:")
		for _, e := range w.after {
			b.WriteString(renderNeighbor(e))
		}
	}

	out := b.String()
	if len(out) > sessionReadOutputBytes {
		return truncateBytes(out, sessionReadOutputBytes) + "\n[output truncated]"
	}
	return out
}

func renderNeighbor(e sessionWindowEvent) string {
	text := strings.TrimSpace(e.Text)
	if text == "" {
		return fmt.Sprintf("\n- seq %d | %s | %s | (no text)", e.Seq, e.Type, e.Role)
	}
	text = truncateBytes(text, sessionReadNeighborBytes)
	return fmt.Sprintf("\n- seq %d | %s | %s\n  %s", e.Seq, e.Type, e.Role, text)
}

// IsRetryable returns false — session reads are deterministic.
func (t *SessionSearchTool) IsRetryable(err error) bool { return false }

// IsRetryable returns false — session reads are deterministic.
func (t *SessionEventReadTool) IsRetryable(err error) bool { return false }

// Interface assertions.
var (
	_ agentic.Tool = (*SessionSearchTool)(nil)
	_ agentic.Tool = (*SessionEventReadTool)(nil)
)
