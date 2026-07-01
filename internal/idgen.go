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
	"strings"
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
