// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// SessionReferenceMention is one parsed @[label](goa-session:<id>) mention or
// bare goa-session:<id> URI.
type SessionReferenceMention struct {
	Label string
	ID    string
}

// sessionReferenceMessage is the serialized form of one projected message
// inside a reference snapshot envelope.
type sessionReferenceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// sessionReferenceJSON is the serialized envelope of one referenced session
// (dsh sessionReference context source reference record).
type sessionReferenceJSON struct {
	ID             string                    `json:"id"`
	Label          string                    `json:"label"`
	CaptureSeq     int                       `json:"capture_seq"`
	Compact        bool                      `json:"compact"`
	Retained       int                       `json:"retained_messages"`
	Omitted        int                       `json:"omitted_messages"`
	OmittedBytes   int                       `json:"omitted_bytes"`
	Truncated      bool                      `json:"truncated"`
	Messages       []sessionReferenceMessage `json:"messages"`
	MessageKept    []bool                    `json:"-"`
}

// sessionReferenceEnvelope is the top-level snapshot document wrapped by the
// <referenced-sessions> framing tags.
type sessionReferenceEnvelope struct {
	Kind       string                `json:"kind"`
	Version    int                   `json:"version"`
	References []sessionReferenceJSON `json:"references"`
}

const (
	sessionReferenceScheme  = "goa-session:"
	sessionReferenceKind    = "session-reference"
	sessionReferenceVersion = 1
)

// sessionMentionRe matches explicit markdown mentions @[label](uri).
var sessionMentionRe = regexp.MustCompile(`@\[([^\]]*)\]\(([^)\s]+)\)`)

// bareSessionRefRe matches a bare goa-session:<id> URI in text (id is
// non-empty and free of whitespace/parens).
var bareSessionRefRe = regexp.MustCompile(`(^|[^A-Za-z0-9_])` + sessionReferenceScheme + `([A-Za-z0-9][A-Za-z0-9_.-]*)`)

// ParseSessionReferenceMentions parses cross-session mention syntax from a
// prompt:
//
//	@[label](goa-session:<id>)
//
// plus bare goa-session:<id> URIs (label falls back to the id). It returns the
// structured references in first-mention order, deduplicated by id, and the
// input with each mention replaced by its readable @label. Explicit markdown
// mentions carrying a malformed goa-session URI reject the parse; empty or
// punctuation-only bare mentions remain ordinary text. Referencing more than
// SessionReferenceMaxReferences distinct sources is rejected.
func ParseSessionReferenceMentions(input string) ([]SessionReferenceMention, string, error) {
	parser := &mentionParser{seen: make(map[string]bool)}
	rewritten, err := parser.rewrite(input)
	if err != nil {
		return nil, "", err
	}
	return parser.refs, rewritten, nil
}

// mentionParser accumulates parsed references while rewriting mentions to
// readable @label text.
type mentionParser struct {
	refs []SessionReferenceMention
	seen map[string]bool
}

// add records one reference (deduplicated by id) and enforces the
// distinct-source limit. Explicit mentions must carry a valid id; bare URIs
// were already validated by the regex.
func (p *mentionParser) add(label, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty session id", agentic.ErrSessionReferenceInvalid)
	}
	if !validSessionRefID(id) {
		return fmt.Errorf("%w: %q", agentic.ErrSessionReferenceInvalid, id)
	}
	if p.seen[id] {
		return nil
	}
	if len(p.refs) >= agentic.SessionReferenceMaxReferences {
		return fmt.Errorf("%w: %d references exceed the limit of %d",
			agentic.ErrSessionReferenceLimit, len(p.refs)+1, agentic.SessionReferenceMaxReferences)
	}
	p.seen[id] = true
	p.refs = append(p.refs, SessionReferenceMention{Label: label, ID: id})
	return nil
}

// rewrite replaces every mention with its readable @label form. Explicit
// markdown mentions are handled first; bare goa-session:<id> URIs left in the
// text are handled in a second pass.
func (p *mentionParser) rewrite(input string) (string, error) {
	var b strings.Builder
	b.Grow(len(input))
	last := 0
	for _, loc := range sessionMentionRe.FindAllStringSubmatchIndex(input, -1) {
		start, end := loc[0], loc[1]
		label := input[loc[2]:loc[3]]
		uri := input[loc[4]:loc[5]]

		if !strings.HasPrefix(uri, sessionReferenceScheme) {
			// Ordinary markdown link (@[text](https://…)) — leave untouched.
			continue
		}
		id := strings.TrimPrefix(uri, sessionReferenceScheme)
		if id == "" {
			return "", fmt.Errorf("%w: malformed mention @[%s](%s)", agentic.ErrSessionReferenceInvalid, label, uri)
		}
		if err := p.add(label, id); err != nil {
			return "", err
		}
		if label == "" {
			label = id
		}
		b.WriteString(input[last:start])
		b.WriteByte('@')
		b.WriteString(label)
		last = end
	}
	b.WriteString(input[last:])

	// Second pass: bare goa-session:<id> URIs left in the text (label falls
	// back to the id). The lookbehind class keeps this from matching inside
	// @[label](goa-session:id) links replaced above — those no longer contain
	// the scheme, so the pass is safe.
	return p.rewriteBare(b.String())
}

// rewriteBare handles the bare-URI pass with explicit error propagation.
func (p *mentionParser) rewriteBare(s string) (string, error) {
	var err error
	out := bareSessionRefRe.ReplaceAllStringFunc(s, func(m string) string {
		if err != nil {
			return m
		}
		prefix := ""
		body := m
		if idx := strings.Index(m, sessionReferenceScheme); idx > 0 {
			prefix, body = m[:idx], m[idx:]
		}
		id := strings.TrimPrefix(body, sessionReferenceScheme)
		if !validSessionRefID(id) {
			return m
		}
		if e := p.add(id, id); e != nil {
			err = e
			return m
		}
		return prefix + "@" + id
	})
	return out, err
}

// validSessionRefID reports whether id is a legal session id token: non-empty,
// no path separators or traversal, no whitespace or parentheses.
func validSessionRefID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r == '/' || r == '\\' || r == '(' || r == ')' || r == ' ':
			return false
		case r < 0x20 || r == 0x7f:
			return false
		}
	}
	return true
}

// resolveSessionReference reads one referenced session as a bounded,
// read-only, checkpoint-aware snapshot and returns its serialized reference
// envelope, enforcing the per-reference byte budget.
func (s *SessionStore) resolveSessionReference(m SessionReferenceMention) (sessionReferenceJSON, error) {
	proj := agentic.NewSurfaceProjector(agentic.SessionReferenceMaxBytes)
	captureSeq := 0
	_, err := s.ScanSessionEvents(m.ID, func(seq int, ev agentic.OutputEvent) bool {
		captureSeq = seq
		proj.Feed(ev)
		return true
	})
	if err != nil {
		if os.IsNotExist(err) {
			return sessionReferenceJSON{}, fmt.Errorf("%w: session %q does not exist", agentic.ErrSessionReferenceInvalid, m.ID)
		}
		return sessionReferenceJSON{}, fmt.Errorf("read session %q: %w", m.ID, err)
	}
	msgs, stats := proj.Surface()
	ref := sessionReferenceJSON{
		ID:           m.ID,
		Label:        m.Label,
		CaptureSeq:   captureSeq,
		Compact:      stats.Folded,
		Retained:     len(msgs),
		Omitted:      stats.OmittedMessages,
		OmittedBytes: stats.OmittedBytes,
		Truncated:    stats.Truncated,
		MessageKept:  make([]bool, len(msgs)),
	}
	for i, m := range msgs {
		ref.Messages = append(ref.Messages, sessionReferenceMessage{Role: string(m.Role), Content: m.Content})
		ref.MessageKept[i] = m.Checkpoint
	}
	if err := trimReferenceToBudget(&ref); err != nil {
		return sessionReferenceJSON{}, err
	}
	return ref, nil
}

// ResolveSessionReferenceMentions parses session-reference mentions in input,
// resolves each referenced session to a bounded, read-only, checkpoint-aware
// snapshot, and returns the rewritten input (mentions replaced by @label) plus
// the untrusted <referenced-sessions> warning frame to prepend to the model
// message. currentSessionID identifies the active session; referencing it is
// rejected (self-reference). Referenced content is UNTRUSTED: it is always
// wrapped in the warning frame by the caller.
func (s *SessionStore) ResolveSessionReferenceMentions(input, currentSessionID string) (rewritten, frame string, err error) {
	mentions, rewritten, err := ParseSessionReferenceMentions(input)
	if err != nil {
		return "", "", err
	}
	if len(mentions) == 0 {
		return input, "", nil
	}
	if s == nil {
		return "", "", errors.New("session store unavailable")
	}
	refs := make([]sessionReferenceJSON, 0, len(mentions))
	for _, m := range mentions {
		if currentSessionID != "" && m.ID == currentSessionID {
			return "", "", fmt.Errorf("%w: %s", agentic.ErrSessionReferenceSelf, m.ID)
		}
		ref, err := s.resolveSessionReference(m)
		if err != nil {
			return "", "", err
		}
		refs = append(refs, ref)
	}
	envelope := sessionReferenceEnvelope{
		Kind:       sessionReferenceKind,
		Version:    sessionReferenceVersion,
		References: refs,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return "", "", err
	}
	frame = agentic.FrameSessionReferenceSnapshot(data)
	return rewritten, frame, nil
}

// trimReferenceToBudget shrinks a serialized reference until it fits
// SessionReferenceMaxBytes: the oldest non-checkpoint message is dropped
// first (checkpoints and the newest message are kept), then the largest
// remaining message is head/tail-truncated. A reference whose fixed envelope
// cannot fit is rejected with ErrSessionReferenceBudget instead of returning
// a partial context.
func trimReferenceToBudget(ref *sessionReferenceJSON) error {
	maxBytes := agentic.SessionReferenceMaxBytes
	for {
		data, err := json.Marshal(ref)
		if err != nil {
			return err
		}
		if len(data) <= maxBytes {
			return nil
		}
		if dropOldestNonCheckpoint(ref) {
			continue
		}
		if !truncateLargestMessage(ref) {
			return fmt.Errorf("%w: reference cannot fit in %d bytes", agentic.ErrSessionReferenceBudget, maxBytes)
		}
	}
}

// dropOldestNonCheckpoint drops the oldest droppable non-checkpoint message
// (never the newest message, never a checkpoint) and reports whether a
// message was dropped.
func dropOldestNonCheckpoint(ref *sessionReferenceJSON) bool {
	for i := 0; i < len(ref.Messages)-1; i++ {
		if ref.MessageKept[i] {
			continue
		}
		omitted := len(ref.Messages[i].Content)
		ref.Messages = append(ref.Messages[:i], ref.Messages[i+1:]...)
		ref.MessageKept = append(ref.MessageKept[:i], ref.MessageKept[i+1:]...)
		ref.Omitted++
		ref.OmittedBytes += omitted
		ref.Retained = len(ref.Messages)
		return true
	}
	return false
}

// truncateLargestMessage head/tail-truncates the largest remaining message to
// make room in the serialized reference. It reports whether a message was
// truncated.
func truncateLargestMessage(ref *sessionReferenceJSON) bool {
	idx := -1
	maxLen := 0
	for i := range ref.Messages {
		if len(ref.Messages[i].Content) > maxLen {
			idx, maxLen = i, len(ref.Messages[i].Content)
		}
	}
	if idx < 0 || maxLen == 0 {
		return false
	}
	before := ref.Messages[idx].Content
	content, ok := agentic.HeadTailTruncate(before, max(0, len(before)/2))
	if !ok {
		return false
	}
	ref.Messages[idx].Content = content
	ref.OmittedBytes += len(before) - len(content)
	ref.Truncated = true
	return true
}
