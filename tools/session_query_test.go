// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// fakeSessionStore implements SessionStoreReader with in-memory data.
type fakeSessionStore struct {
	current  string
	sessions map[string][]agentic.OutputEvent
	mtimes   map[string]time.Time
	ids      []string
}

func (f *fakeSessionStore) SessionID() string { return f.current }

func (f *fakeSessionStore) ListSessionIDs() ([]string, error) {
	return append([]string(nil), f.ids...), nil
}

func (f *fakeSessionStore) ScanSessionEvents(sessionID string, visit func(seq int, ev agentic.OutputEvent) bool) (int, error) {
	events := f.sessions[sessionID]
	for i, ev := range events {
		if !visit(i+1, ev) {
			return i + 1, nil
		}
	}
	return len(events), nil
}

func (f *fakeSessionStore) SessionModifiedTime(sessionID string) (time.Time, bool) {
	mt, ok := f.mtimes[sessionID]
	return mt, ok
}

func newFakeStore() *fakeSessionStore {
	return &fakeSessionStore{
		sessions: make(map[string][]agentic.OutputEvent),
		mtimes:   make(map[string]time.Time),
	}
}

// sessionWithDecision builds a session containing a user question followed by
// an assistant decision reply.
func sessionWithDecision(decision string) []agentic.OutputEvent {
	return []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, Text: "What DB should we use?"},
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: decision},
		{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"echo done"}`},
		{Type: agentic.EventToolResult, ToolName: "bash", ToolResult: "done\n"},
	}
}

func TestSessionSearch_FindsPriorDecision(t *testing.T) {
	store := newFakeStore()
	store.current = "current_session"
	store.sessions["current_session"] = sessionWithDecision("placeholder")
	store.ids = []string{"old_session", "current_session"}
	store.sessions["old_session"] = sessionWithDecision("We decided to use PostgreSQL with WAL archiving for the analytics pipeline.")
	store.mtimes["old_session"] = time.Now().Add(-time.Hour)

	tool := &SessionSearchTool{Store: store}
	out, err := tool.Execute(`{"query": "postgresql wal archiving"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "old_session") {
		t.Errorf("expected old_session in results, got:\n%s", out)
	}
	if !strings.Contains(out, "seq 2") {
		t.Errorf("expected best match at seq 2 (assistant decision), got:\n%s", out)
	}
	if !strings.Contains(out, "PostgreSQL") {
		t.Errorf("expected snippet to quote the decision, got:\n%s", out)
	}
	if strings.Contains(out, "current_session") {
		t.Errorf("current session must not appear in prior-session search, got:\n%s", out)
	}
}

func TestSessionSearch_ExcludesCurrentSession(t *testing.T) {
	store := newFakeStore()
	store.current = "only_session"
	store.sessions["only_session"] = sessionWithDecision("We decided to use Redis.")
	store.ids = []string{"only_session"}

	tool := &SessionSearchTool{Store: store}
	out, err := tool.Execute(`{"query": "redis"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "No prior session matches found") {
		t.Errorf("expected empty result (only current session exists), got:\n%s", out)
	}
}

func TestSessionSearch_NoMatches(t *testing.T) {
	store := newFakeStore()
	store.sessions["s1"] = sessionWithDecision("We chose SQLite.")
	store.ids = []string{"s1"}
	store.mtimes["s1"] = time.Now()

	tool := &SessionSearchTool{Store: store}
	out, err := tool.Execute(`{"query": "kafka"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "No prior session matches found") {
		t.Errorf("expected no matches, got:\n%s", out)
	}
}

func TestSessionSearch_MaxResultsCapsOutput(t *testing.T) {
	store := newFakeStore()
	store.current = "current"
	store.ids = nil
	for i := 0; i < 5; i++ {
		id := "session_" + string(rune('a'+i))
		store.ids = append(store.ids, id)
		store.sessions[id] = sessionWithDecision("Decision number " + id + " about postgres and caching.")
		store.mtimes[id] = time.Now().Add(time.Duration(-i) * time.Minute)
	}

	tool := &SessionSearchTool{Store: store}
	out, err := tool.Execute(`{"query": "postgres", "max_results": 2}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Session search results (2):") {
		t.Errorf("expected 2 results, got:\n%s", out)
	}
	if !strings.Contains(out, "Result cap reached") {
		t.Errorf("expected cap note, got:\n%s", out)
	}
}

func TestSessionSearch_HardMaxCap(t *testing.T) {
	store := newFakeStore()
	store.current = "current"
	store.ids = nil
	for i := 0; i < 3; i++ {
		id := "h" + string(rune('a'+i))
		store.ids = append(store.ids, id)
		store.sessions[id] = sessionWithDecision("Postgres decision " + id)
		store.mtimes[id] = time.Now()
	}

	tool := &SessionSearchTool{Store: store}
	// Requesting more than the hard cap must clamp, not error.
	out, err := tool.Execute(`{"query": "postgres", "max_results": 9999}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Session search results (3):") {
		t.Errorf("expected 3 results, got:\n%s", out)
	}
}

func TestSessionSearch_InvalidQuery(t *testing.T) {
	store := newFakeStore()
	tool := &SessionSearchTool{Store: store}

	_, err := tool.Execute(`{"query": "   "}`)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
	var te *internal.ToolError
	if !asToolError(err, &te) || te.Type != "invalid_query" {
		t.Fatalf("expected invalid_query ToolError, got %v", err)
	}

	_, err = tool.Execute(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !asToolError(err, &te) || te.Type != "invalid_input" {
		t.Fatalf("expected invalid_input ToolError, got %v", err)
	}
}

func TestSessionSearch_SessionIDsFilter(t *testing.T) {
	store := newFakeStore()
	store.ids = []string{"a", "b"}
	store.sessions["a"] = sessionWithDecision("Postgres for a")
	store.sessions["b"] = sessionWithDecision("Postgres for b")
	store.mtimes["a"] = time.Now()
	store.mtimes["b"] = time.Now()

	tool := &SessionSearchTool{Store: store}
	out, err := tool.Execute(`{"query": "postgres", "session_ids": ["a"]}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Session a") || strings.Contains(out, "Session b") {
		t.Errorf("expected only session a, got:\n%s", out)
	}
}

func TestSessionSearch_SnippetBounded(t *testing.T) {
	store := newFakeStore()
	long := "We decided to use postgres. " + strings.Repeat("lorem ipsum dolor sit amet ", 500)
	store.ids = []string{"long"}
	store.sessions["long"] = sessionWithDecision(long)
	store.mtimes["long"] = time.Now()

	tool := &SessionSearchTool{Store: store}
	out, err := tool.Execute(`{"query": "postgres"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Snippet is bounded to sessionSearchSnippetBytes; verify the raw long
	// text is not emitted in full.
	if strings.Contains(out, strings.Repeat("lorem ipsum dolor sit amet ", 500)) {
		t.Error("expected snippet to be truncated, got full text")
	}
	// The rendered output must respect the byte budget.
	if len(out) > sessionSearchOutputBytes {
		t.Errorf("output exceeds %d bytes: %d", sessionSearchOutputBytes, len(out))
	}
}

// ── session_event_read ──────────────────────────────────────────────────────

func TestSessionEventRead_SelfSessionDefault(t *testing.T) {
	store := newFakeStore()
	store.current = "self"
	store.sessions["self"] = sessionWithDecision("We decided to use PostgreSQL.")
	store.mtimes["self"] = time.Now()

	tool := &SessionEventReadTool{Store: store}
	out, err := tool.Execute(`{"seq": 2}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Session self") {
		t.Errorf("expected current session header, got:\n%s", out)
	}
	if !strings.Contains(out, "PostgreSQL") {
		t.Errorf("expected the decision text in the event JSON, got:\n%s", out)
	}
	if !strings.Contains(out, "```json") {
		t.Errorf("expected JSON code fence, got:\n%s", out)
	}
}

func TestSessionEventRead_OtherSessionReadOnly(t *testing.T) {
	store := newFakeStore()
	store.current = "self"
	store.sessions["self"] = sessionWithDecision("self decision")
	store.sessions["other"] = sessionWithDecision("other decision about postgres")
	store.mtimes["other"] = time.Now()

	tool := &SessionEventReadTool{Store: store}
	out, err := tool.Execute(`{"session_id": "other", "seq": 2}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Session other") {
		t.Errorf("expected other session header, got:\n%s", out)
	}
	if !strings.Contains(out, "other decision about postgres") {
		t.Errorf("expected other session content, got:\n%s", out)
	}
	// Session files must be unchanged — the tool is read-only.
	if len(store.sessions["other"]) != 4 {
		t.Errorf("other session must not be modified, got %d events", len(store.sessions["other"]))
	}
}

func TestSessionEventRead_WindowNeighbors(t *testing.T) {
	store := newFakeStore()
	store.current = "self"
	store.sessions["self"] = sessionWithDecision("decision")
	store.mtimes["self"] = time.Now()

	tool := &SessionEventReadTool{Store: store}
	out, err := tool.Execute(`{"seq": 3, "before": 2, "after": 1}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Before:") || !strings.Contains(out, "After:") {
		t.Errorf("expected before/after sections, got:\n%s", out)
	}
	if !strings.Contains(out, "seq 1") || !strings.Contains(out, "seq 2") {
		t.Errorf("expected both before neighbors, got:\n%s", out)
	}
	if !strings.Contains(out, "seq 4") {
		t.Errorf("expected after neighbor, got:\n%s", out)
	}
	// Target must be the full event JSON with tool call fields.
	if !strings.Contains(out, `"ToolName": "bash"`) {
		t.Errorf("expected full target JSON with ToolName, got:\n%s", out)
	}
}

func TestSessionEventRead_WindowBounds(t *testing.T) {
	store := newFakeStore()
	store.current = "self"
	store.sessions["self"] = sessionWithDecision("decision")
	store.mtimes["self"] = time.Now()

	tool := &SessionEventReadTool{Store: store}
	// before/after larger than the hard cap clamp to sessionReadWindowMax
	// without error.
	out, err := tool.Execute(`{"seq": 2, "before": 9999, "after": 9999}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out) > sessionReadOutputBytes {
		t.Errorf("output exceeds %d bytes: %d", sessionReadOutputBytes, len(out))
	}
}

func TestSessionEventRead_SeqOutOfRange(t *testing.T) {
	store := newFakeStore()
	store.current = "self"
	store.sessions["self"] = sessionWithDecision("decision")
	store.mtimes["self"] = time.Now()

	tool := &SessionEventReadTool{Store: store}
	_, err := tool.Execute(`{"seq": 99}`)
	if err == nil {
		t.Fatal("expected error for out-of-range seq")
	}
	var te *internal.ToolError
	if !asToolError(err, &te) || te.Type != "event_not_found" {
		t.Fatalf("expected event_not_found ToolError, got %v", err)
	}
}

func TestSessionEventRead_InvalidSeq(t *testing.T) {
	store := newFakeStore()
	tool := &SessionEventReadTool{Store: store}

	_, err := tool.Execute(`{"seq": 0}`)
	if err == nil {
		t.Fatal("expected error for seq 0")
	}
	var te *internal.ToolError
	if !asToolError(err, &te) || te.Type != "invalid_input" {
		t.Fatalf("expected invalid_input ToolError, got %v", err)
	}

	_, err = tool.Execute(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSessionEventRead_NoSessionAndNoneActive(t *testing.T) {
	store := newFakeStore() // no current session
	tool := &SessionEventReadTool{Store: store}

	_, err := tool.Execute(`{"seq": 1}`)
	if err == nil {
		t.Fatal("expected error when no session_id and no active session")
	}
	var te *internal.ToolError
	if !asToolError(err, &te) || te.Type != "no_active_session" {
		t.Fatalf("expected no_active_session ToolError, got %v", err)
	}
}

func TestSessionEventRead_MissingSession(t *testing.T) {
	store := newFakeStore()
	store.current = "self"
	tool := &SessionEventReadTool{Store: store}

	_, err := tool.Execute(`{"session_id": "ghost", "seq": 1}`)
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	var te *internal.ToolError
	if !asToolError(err, &te) || te.Type != "session_not_found" {
		t.Fatalf("expected session_not_found ToolError, got %v", err)
	}
}

func TestSessionEventRead_EventJSONBounded(t *testing.T) {
	store := newFakeStore()
	store.current = "self"
	big := strings.Repeat("x", 50000)
	store.sessions["self"] = []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: big},
	}
	store.mtimes["self"] = time.Now()

	tool := &SessionEventReadTool{Store: store}
	out, err := tool.Execute(`{"seq": 1}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, big) {
		t.Error("expected target event JSON to be bounded")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func asToolError(err error, target **internal.ToolError) bool {
	te, ok := err.(*internal.ToolError)
	if ok {
		*target = te
	}
	return ok
}

// TestSessionStoreReader_InterfaceSatisfied ensures the fake satisfies the
// interface used by both tools.
func TestSessionStoreReader_InterfaceSatisfied(t *testing.T) {
	var _ SessionStoreReader = (*fakeSessionStore)(nil)
}

// TestSessionQueryTools_Schema checks both schemas are well-formed.
func TestSessionQueryTools_Schema(t *testing.T) {
	search := (&SessionSearchTool{}).Schema()
	if search.Name != "session_search" || search.Description == "" {
		t.Errorf("bad session_search schema: %+v", search)
	}
	read := (&SessionEventReadTool{}).Schema()
	if read.Name != "session_event_read" || read.Description == "" {
		t.Errorf("bad session_event_read schema: %+v", read)
	}

	// seq must be in the required list for the read tool.
	required, ok := read.Schema["required"].([]string)
	if !ok {
		t.Fatalf("schema has no required list: %+v", read.Schema)
	}
	found := false
	for _, r := range required {
		if r == "seq" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected seq in required, got %v", required)
	}
}

func TestParseSearchQuery(t *testing.T) {
	terms, err := parseSearchQuery("  PostgreSQL WAL archiving  ")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(terms) != 3 || terms[0] != "postgresql" || terms[1] != "wal" || terms[2] != "archiving" {
		t.Fatalf("unexpected terms: %v", terms)
	}

	// Duplicate terms are deduped.
	terms, err = parseSearchQuery("foo foo bar")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(terms) != 2 {
		t.Fatalf("expected dedup to 2 terms, got %v", terms)
	}

	if _, err := parseSearchQuery(""); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestScoreEvent_RequiresAllTerms(t *testing.T) {
	ev := agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Use postgres for storage and postgres for cache."}
	if score := scoreEvent(ev, []string{"postgres"}); score != 2 {
		t.Fatalf("expected score 2, got %d", score)
	}
	// Missing term → no match.
	if score := scoreEvent(ev, []string{"postgres", "kafka"}); score != 0 {
		t.Fatalf("expected 0 for missing term, got %d", score)
	}
	// Tool result text is searchable.
	ev2 := agentic.OutputEvent{Type: agentic.EventToolResult, ToolResult: "migration complete"}
	if score := scoreEvent(ev2, []string{"migration"}); score != 1 {
		t.Fatalf("expected tool result searchable, got %d", score)
	}
}

func TestEventSnippet_RuneSafeWindow(t *testing.T) {
	// Non-ASCII text where byte and rune offsets differ. The snippet must be
	// valid UTF-8 and contain the matched term.
	text := strings.Repeat("İşlem ", 50) + "postgres karar " + strings.Repeat("İşlem ", 50)
	ev := agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: text}
	snippet := eventSnippet(ev, []string{"postgres"})
	if !strings.Contains(snippet, "postgres") {
		t.Errorf("snippet lost the match: %q", snippet)
	}
	if !json.Valid([]byte(`"` + snippet + `"`)) {
		t.Errorf("snippet produced invalid UTF-8: %q", snippet)
	}
	if len(snippet) > sessionSearchSnippetBytes {
		t.Errorf("snippet exceeds %d bytes: %d", sessionSearchSnippetBytes, len(snippet))
	}
}

func TestTruncateBytes_DoesNotSplitRune(t *testing.T) {
	s := "héllo wörld"
	cut := truncateBytes(s, 3)
	if !strings.HasSuffix(cut, "…") {
		t.Errorf("expected ellipsis, got %q", cut)
	}
	// The result never exceeds the budget, even with the ellipsis.
	if len(cut) > 3 {
		t.Errorf("expected cut within budget, got %d bytes: %q", len(cut), cut)
	}
	// Ensure the cut is valid UTF-8.
	if !json.Valid([]byte(`"` + cut + `"`)) {
		t.Errorf("cut produced invalid UTF-8: %q", cut)
	}

	// A multi-byte rune at the boundary must not be split.
	cut2 := truncateBytes("éééé", 5)
	if !strings.HasSuffix(cut2, "…") {
		t.Errorf("expected ellipsis, got %q", cut2)
	}
	if len(cut2) > 5 {
		t.Errorf("expected cut2 within budget, got %d bytes: %q", len(cut2), cut2)
	}
	if !json.Valid([]byte(`"` + cut2 + `"`)) {
		t.Errorf("cut2 produced invalid UTF-8: %q", cut2)
	}
}
