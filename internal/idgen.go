// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// RandomString returns a cryptographically random string of the given length
// drawn from a lowercase-alphanumeric alphabet. It panics only if the system's
// CSPRNG is unavailable, which is treated as a fatal environment error.
//
// Use this instead of time-seeded LCGs (see review CORE-BUG-6): IDs generated
// from time.Now() alone collide when two calls land in the same nanosecond and
// are trivially predictable.
func RandomString(length int) string {
	if length <= 0 {
		return ""
	}
	out := make([]byte, length)
	max := big.NewInt(int64(len(idAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand should never fail on a healthy system; surface it.
			panic(fmt.Sprintf("internal: crypto/rand failed: %v", err))
		}
		out[i] = idAlphabet[n.Int64()]
	}
	return string(out)
}

// PrefixedHexID returns "<prefix>-<unix-nano>-<hex>" where hex is 2*n bytes of
// cryptographic randomness. This is the shared shape already used for goal and
// queue IDs (see core/goal_queue.go generateQueueID, core/goal/mode.go
// generateGoalID); centralizing it here keeps the format consistent and the
// randomness source uniform.
func PrefixedHexID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("internal: crypto/rand failed: %v", err))
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), hex.EncodeToString(b))
}

// anonymousUserIDFile is the bare-line identity file inside the goa home
// (~/.goa/.anonymous-user-id), mirroring dsh's
// @deepseek-ai/dsh-anonymous-user-id storage contract.
const anonymousUserIDFile = ".anonymous-user-id"

var (
	anonymousUserIDMu   sync.Mutex
	anonymousUserIDMemo = map[string]string{}
)

// AnonymousUserID returns the stable anonymous identifier for this goa
// install, used to correlate provider requests across sessions without
// revealing identity (the P13/CA2 correlation header x-goa-user-id). It is
// a UUID v4 persisted as a bare line at <goa-home>/.anonymous-user-id,
// memoized per resolved home for the process lifetime.
//
// The identity is never derived from the hostname, network address, git
// remote, or another identifying source (dsh anonymous-user-id contract).
// Deleting the file mints a new identity on the next process launch.
// Persistence is best-effort: an unwritable home still yields a process-local
// UUID rather than blocking the caller.
func AnonymousUserID() string {
	dir := GoaHomeDir()
	anonymousUserIDMu.Lock()
	defer anonymousUserIDMu.Unlock()
	if id, ok := anonymousUserIDMemo[dir]; ok {
		return id
	}
	id := loadOrCreateAnonymousUserID(dir)
	anonymousUserIDMemo[dir] = id
	return id
}

// loadOrCreateAnonymousUserID returns the persisted id at
// <dir>/.anonymous-user-id, or mints and persists a new one. An empty dir
// (no resolvable goa home) returns a process-local UUID without persistence.
func loadOrCreateAnonymousUserID(dir string) string {
	if dir == "" {
		return uuidV4()
	}
	path := filepath.Join(dir, anonymousUserIDFile)
	if id := readAnonymousUserID(path); id != "" {
		return id
	}
	id := uuidV4()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return id // best-effort: process-local UUID
	}
	// Exclusive creation: a concurrent first writer wins and the loser
	// adopts the persisted winner.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			// A concurrent winner persisted first — adopt it. A corrupt
			// pre-existing file is replaced with our fresh id.
			if winner := readAnonymousUserID(path); winner != "" {
				return winner
			}
			_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
			return id
		}
		return id // unwritable home: process-local UUID
	}
	if _, werr := f.WriteString(id + "\n"); werr != nil {
		_ = f.Close()
		return id
	}
	if cerr := f.Close(); cerr != nil {
		return id
	}
	return id
}

// readAnonymousUserID returns the valid UUID v4 stored at path, or "" when
// the file is missing, empty, or corrupt (a corrupt file is replaced by
// loadOrCreateAnonymousUserID).
func readAnonymousUserID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(b))
	if !isUUIDv4(id) {
		return ""
	}
	return id
}

// uuidV4 returns a random RFC 4122 version-4 UUID (lowercase hex).
func uuidV4() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("internal: crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// isUUIDv4 reports whether s has the canonical 8-4-4-4-12 lowercase-hex UUID
// v4 shape.
func isUUIDv4(s string) bool {
	if len(s) != 36 {
		return false
	}
	for _, i := range []int{8, 13, 18, 23} {
		if s[i] != '-' {
			return false
		}
	}
	if s[14] != '4' {
		return false
	}
	for i := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !isHexDigit(s[i]) {
			return false
		}
	}
	return true
}

// isHexDigit reports whether c is an ASCII hexadecimal digit.
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

//go:embed data/goal_adjectives.txt
var goalAdjectivesRaw string

//go:embed data/goal_nouns.txt
var goalNounsRaw string

// friendlyAdjectives and friendlyNouns are parsed once from the embedded word
// lists at init time. They are short, memorable, lowercase words and combine
// to a large namespace (len(adjectives) * len(nouns) ~= 48*48 = 2304 unique
// pairs), ample for the small number of goals that coexist in a session
// (one active + a short queue). A friendly name is a human-friendly ALIAS
// only; the internal hex ID remains the persistence/lookup key.
var (
	friendlyAdjectives = splitWordList(goalAdjectivesRaw)
	friendlyNouns      = splitWordList(goalNounsRaw)
)

// splitWordList turns the embedded newline-separated word file into a slice,
// trimming whitespace and dropping empty lines.
func splitWordList(raw string) []string {
	var words []string
	for _, line := range strings.Split(raw, "\n") {
		w := strings.TrimSpace(line)
		if w != "" {
			words = append(words, w)
		}
	}
	return words
}

// FriendlyName returns a random "adjective.noun" name such as "happy.fox".
// It is intended as a human-friendly display alias for goals.
func FriendlyName() string {
	adj := pickWord(friendlyAdjectives)
	noun := pickWord(friendlyNouns)
	return adj + "." + noun
}

// FriendlyNameUnique returns a friendly name not present in taken. It tries a
// bounded number of random draws; on exhaustion it appends a short numeric
// suffix to guarantee uniqueness. This keeps the common case readable while
// staying collision-free.
func FriendlyNameUnique(taken map[string]bool) string {
	for i := 0; i < 16; i++ {
		name := FriendlyName()
		if !taken[name] {
			return name
		}
	}
	// Fallback: disambiguate with a short random suffix.
	for i := 0; i < 64; i++ {
		name := FriendlyName() + "." + RandomString(2)
		if !taken[name] {
			return name
		}
	}
	// Extremely unlikely: full hex suffix guarantees global uniqueness.
	return FriendlyName() + "." + PrefixedHexID("x", 2)
}

func pickWord(words []string) string {
	max := big.NewInt(int64(len(words)))
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		panic(fmt.Sprintf("internal: crypto/rand failed: %v", err))
	}
	return words[n.Int64()]
}

// SplitFriendlyName reports whether s looks like an "adjective.noun" friendly
// name. Used by callers that accept either a friendly name or raw text.
func SplitFriendlyName(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	return true
}

// IsValidRunName reports whether s is acceptable as a user-supplied custom
// orchestrator run name. Rules: non-empty, lowercase, alphanumeric plus . - _.
func IsValidRunName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
